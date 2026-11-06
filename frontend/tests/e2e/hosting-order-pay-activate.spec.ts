import { expect, test } from './fixtures';
import { randomUUID } from 'node:crypto';
import { withAuthRateLimitRetry } from './helpers';

/**
 * Flow 2 (PRD §13.2 / docs/E2E.md §5.2): browse the seeded hosting product,
 * add to cart, checkout as a freshly registered + verified client, land on
 * the invoice, pay via a Duitku mock method, follow the mock payment page,
 * get redirected back, and confirm the invoice reaches `paid` and the
 * service reaches `active` (the worker process provisions it for real).
 *
 * Runs against the already-running live stack (mockserver:9090, api:8080,
 * worker, frontend:5173) - see docs/E2E.md. Uses unique email/domain per run
 * so it is safe to run concurrently with the other flow specs against the
 * same shared backend + database.
 */

const MOCK_BASE = 'http://localhost:9090';
const API_BASE = 'http://localhost:8080';

// Seeded fixture (backend/cmd/seed): shared_hosting product bound to a cpanel
// server group, auto_setup=on_payment, monthly pricing only.
const PRODUCT_SLUG = 'e2e-shared-hosting';

test.describe('hosting order → pay → activate', () => {
	test('register, order hosting, pay via Duitku mock, service activates', async ({ page, request }) => {
		test.setTimeout(220_000);

		const suffix = `${Date.now()}${Math.floor(Math.random() * 10_000)}`;
		const email = `e2e-hosting-${suffix}@example.test`;
		const password = 'Sup3rSecret!23';
		// cPanel usernames are derived from the domain's first label, lowercased
		// and truncated to 8 chars (domain.UsernameFromDomain) - the mock cPanel
		// server is a single shared, never-reset process across every concurrent
		// E2E spec/run, so a timestamp-suffixed label (where only the trailing,
		// truncated-away digits differ between runs) collides constantly ("account
		// exists" 409s that never resolve to active). A short random hex label
		// keeps the whole 8-char username space effectively unique run-to-run.
		const domain = `${randomUUID().replace(/-/g, '').slice(0, 10)}.test`;

		// --- 1. Register a fresh client ------------------------------------------------
		await page.goto('/register');
		// SvelteKit hydration race: the SSR'd form fields are plain HTML until the
		// client JS bundle finishes hydrating (which re-renders inputs from their
		// $state initial values). Filling before that completes can get silently
		// wiped by hydration. Wait for the network to go quiet first.
		await page.waitForLoadState('networkidle');
		await page.locator('#field-email').fill(email);
		await page.locator('#field-password').fill(password);
		await page.locator('#field-confirm_password').fill(password);
		await page.locator('#field-first_name').fill('E2E');
		await page.locator('#field-last_name').fill('Hosting');
		await page.locator('#field-address1').fill('Jl. Testing No. 1');
		await page.locator('#field-city').fill('Jakarta');
		await page.locator('#field-state').fill('DKI Jakarta');
		await page.locator('#field-postcode').fill('12345');
		await page.locator('#field-country').selectOption('ID');
		await page.locator('#field-phone').fill('+6281234567890');
		await page.getByTestId('register-submit').click();
		await expect(page.getByTestId('register-success')).toBeVisible();

		// --- 2. Pull the verification link out of the mock mail sink -------------------
		let verifyLink = '';
		await expect
			.poll(
				async () => {
					const res = await request.get(`${MOCK_BASE}/mail/messages`, { params: { to: email } });
					if (!res.ok()) return '';
					const messages = (await res.json()) as Array<{ to: string; html: string; text: string }>;
					const msg = messages.find((m) => m.to.toLowerCase() === email.toLowerCase());
					if (!msg) return '';
					const src = msg.html || msg.text || '';
					const match =
						src.match(/href="([^"]*verify-email\?token=[^"]*)"/) ??
						src.match(/(https?:\/\/\S*verify-email\?token=\S+)/);
					verifyLink = match ? match[1].replace(/&amp;/g, '&') : '';
					return verifyLink;
				},
				{ timeout: 20_000, message: 'waiting for the verification email to arrive' }
			)
			.not.toBe('');

		await page.goto(verifyLink);
		await expect(page).toHaveURL(/\/verify-email\?verified=1/);

		// --- 3. Log in -------------------------------------------------------------------
		// /auth/login shares one IP-keyed fixed-window rate-limit bucket (30
		// req/min, backend/internal/modules/auth/handler.go publicLimit) with
		// every other concurrently-running E2E spec, so retry the submission a
		// few times (spanning more than one fixed window) before failing for
		// good - a transient 429 here is cross-test interference, not a bug.
		let loggedIn = false;
		for (let attempt = 1; attempt <= 8 && !loggedIn; attempt++) {
			await page.goto('/login');
			await page.waitForLoadState('networkidle');
			await page.locator('input[name="email"]').fill(email);
			await page.locator('input[name="password"]').fill(password);
			await page.locator('button[type="submit"]').click();
			try {
				await expect(page).toHaveURL(/\/dashboard/, { timeout: 20_000 });
				loggedIn = true;
			} catch (err) {
				if (attempt === 8) throw err;
			}
		}

		// --- 4. Browse the seeded hosting product + add to cart -------------------------
		await page.goto(`/order/product/${PRODUCT_SLUG}`);
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('product-title')).toBeVisible();
		await page.getByTestId('product-cycle-monthly').check();
		// "own domain" mode: this product isn't a domain purchase (flow 3 covers
		// that), but the product page still requires a syntactically valid
		// domain name to be entered regardless of mode.
		await page.getByTestId('domain-mode-own').check();
		await page.getByTestId('domain-search-input').fill(domain);
		await page.getByTestId('add-to-cart').click();

		// --- 5. Checkout -------------------------------------------------------------------
		await expect(page).toHaveURL(/\/order\/cart/);
		await expect(page.getByTestId('cart-items')).toBeVisible();
		await expect(page.getByTestId('checkout-submit')).toBeEnabled();
		await page.getByTestId('checkout-submit').click();

		await expect(page).toHaveURL(/\/billing\/invoices\/\d+/);
		const invoiceId = Number(page.url().match(/invoices\/(\d+)/)?.[1]);
		expect(invoiceId).toBeGreaterThan(0);

		// --- 6. Pick a Duitku payment method + pay ----------------------------------------
		// "VC" (credit card) has no VA/QRIS raw payload to render inline, so it's
		// the one channel guaranteed to still redirect to the gateway's hosted
		// page - see payments-instructions.spec.ts for the VA/QRIS inline-render
		// coverage this flow deliberately doesn't exercise.
		await expect(page.getByTestId('payment-methods')).toBeVisible();
		// Credit Card sits behind the "show more" toggle (only major VA
		// banks + QRIS are shown expanded by default).
		await page.getByTestId('payment-methods-toggle').click();
		await page.getByTestId('payment-method-VC').click();
		await page.getByTestId('pay-button').click();

		// Server action redirects (303) straight to the mock's hosted payment page.
		await page.waitForURL(/^http:\/\/localhost:9090\/payment\//, { timeout: 15_000 });
		await page.click('#pay-now');

		// Mock marks the tx paid, delivers the signed callback, then redirects the
		// browser back to FRONTEND_URL/payments/return - which itself redirects
		// straight to the invoice once it sees status=paid.
		await page.waitForURL(new RegExp(`/billing/invoices/${invoiceId}`), { timeout: 20_000 });
		await expect(page.getByTestId('invoice-status-banner')).toBeVisible();

		// --- 7. Confirm invoice paid + service active, straight from the backend --------
		// (locale-independent - the UI's status text is translated, so assert on
		// the raw API fields rather than parsing rendered copy.)
		const loginRes = await withAuthRateLimitRetry(() =>
			request.post(`${API_BASE}/api/v1/auth/login`, { data: { email, password, captcha_token: 'e2e-dummy-captcha-token' } })
		);
		expect(loginRes.ok()).toBeTruthy();
		const loginBody = await loginRes.json();
		const token = loginBody.data.access_token as string;
		const authHeader = { Authorization: `Bearer ${token}` };

		await expect
			.poll(
				async () => {
					const res = await request.get(`${API_BASE}/api/v1/invoices/${invoiceId}`, {
						headers: authHeader
					});
					if (!res.ok()) return null;
					const body = await res.json();
					const invoice = body.data?.invoice ?? body.data;
					return invoice?.status ?? null;
				},
				{ timeout: 20_000, message: 'waiting for the invoice to be marked paid' }
			)
			.toBe('paid');

		let serviceId: number | null = null;
		await expect
			.poll(
				async () => {
					const res = await request.get(`${API_BASE}/api/v1/services`, {
						headers: authHeader,
						params: { search: domain }
					});
					if (!res.ok()) return null;
					const body = await res.json();
					const services = (body.data ?? []) as Array<{ id: number; domain: string; status: string }>;
					const svc = services.find((s) => s.domain === domain);
					serviceId = svc?.id ?? null;
					return svc?.status ?? null;
				},
				{ timeout: 60_000, message: 'waiting for the worker to provision the service to active' }
			)
			.toBe('active');

		// --- 8. Confirm the same thing through the client UI -----------------------------
		expect(serviceId).not.toBeNull();
		await page.goto(`/services/${serviceId}`);
		await expect(page.getByTestId('service-domain')).toHaveText(domain);
		// Only rendered when service.status === 'active' - a locale-independent
		// proxy for "the service reached active" via the UI itself.
		await expect(page.getByTestId('service-change-password-button')).toBeVisible();
	});
});
