import { expect, test, type APIRequestContext, type Page } from './fixtures';
import { withAuthRateLimitRetry } from './helpers';

/**
 * Flow 1 (PRD §13.2): register -> verify email via the mock mail sink -> login.
 * Flow 8 (PRD §13.2): RBAC - seeded staff (no "settings" permission) cannot reach
 * /admin/settings; a plain client cannot reach any /admin route.
 *
 * Runs against the already-live stack (frontend :5173, backend api :8080,
 * mockserver :9090). Uses timestamp+random-suffixed emails since other specs run
 * concurrently against the same backend/DB - never assert global counts.
 */

const MOCKSERVER_URL = 'http://localhost:9090';

function unique(prefix: string): string {
	return `${prefix}-${Date.now()}-${Math.floor(Math.random() * 1_000_000)}`;
}

interface MailMessage {
	id: number;
	to: string;
	from: string;
	subject: string;
	html: string;
	text: string;
	receivedAt: string;
}

/** Polls the mock mail sink for the first message addressed to `to`, then extracts
 * the `token=...` query value out of the verify-email link embedded in its body. */
async function getVerifyEmailToken(request: APIRequestContext, to: string): Promise<string> {
	let token: string | null = null;

	await expect
		.poll(
			async () => {
				const res = await request.get(
					`${MOCKSERVER_URL}/mail/messages?to=${encodeURIComponent(to)}`
				);
				if (!res.ok()) return null;
				const messages = (await res.json()) as MailMessage[];
				for (const msg of messages) {
					const body = `${msg.html ?? ''} ${msg.text ?? ''}`;
					const match = body.match(/verify-email\?token=([^"'&\s<]+)/);
					if (match) {
						token = decodeURIComponent(match[1]);
						return true;
					}
				}
				return null;
			},
			{ message: `waiting for verify-email mail to ${to}`, timeout: 20_000 }
		)
		.toBeTruthy();

	if (!token) throw new Error(`no verify-email token found for ${to}`);
	return token;
}

interface RegisterFields {
	email: string;
	password: string;
	firstName: string;
	lastName: string;
	address1: string;
	city: string;
	state: string;
	postcode: string;
	phone: string;
}

/**
 * Fills and submits the public register form via the real UI. /auth/register
 * shares one IP-keyed fixed-window rate-limit bucket (30 req/min,
 * backend/internal/modules/auth/handler.go publicLimit) with every OTHER
 * concurrently-running E2E spec's register/login/verify calls on this
 * machine (and any other agent session sharing this stack), so a rate-limit
 * hit here renders the generic `register-error` banner (not a field error -
 * that's reserved for real validation/conflict failures) instead of
 * advancing past the form. Retry the whole submission a few times (spanning
 * more than one fixed window) whenever that generic banner shows up before
 * giving up for good; genuine field-level errors (e.g. duplicate email) are
 * left alone since callers assert on those explicitly right after this call.
 */
async function submitRegisterForm(page: Page, fields: RegisterFields): Promise<void> {
	for (let attempt = 1; attempt <= 8; attempt++) {
		await page.goto('/register');
		await expect(page.getByTestId('register-form')).toBeVisible();
		// SvelteKit SSR-renders the form immediately, but client hydration
		// (which wires up each field's bind:value) settles slightly later -
		// filling fields before it finishes lets the hydration effect silently
		// snap the fastest-filled field (email, filled first below) back to its
		// initial empty $state. This race widens under concurrent-suite load
		// (more CPU/network contention slows hydration), so always wait for the
		// network to go quiet before touching any control.
		await page.waitForLoadState('networkidle');

		await page.locator('#field-email').fill(fields.email);
		await page.locator('#field-password').fill(fields.password);
		await page.locator('#field-confirm_password').fill(fields.password);
		await page.locator('#field-first_name').fill(fields.firstName);
		await page.locator('#field-last_name').fill(fields.lastName);
		await page.locator('#field-address1').fill(fields.address1);
		await page.locator('#field-city').fill(fields.city);
		await page.locator('#field-state').fill(fields.state);
		await page.locator('#field-postcode').fill(fields.postcode);
		await page.locator('#field-country').selectOption('ID');
		await page.locator('#field-phone').fill(fields.phone);

		await page.getByTestId('register-submit').locator('button[type="submit"]').click();

		const result = await Promise.race([
			page
				.getByTestId('register-success')
				.waitFor({ state: 'visible', timeout: 20_000 })
				.then(() => 'success' as const)
				.catch(() => 'timeout' as const),
			page
				.getByTestId('register-email')
				.getByRole('alert')
				.waitFor({ state: 'visible', timeout: 20_000 })
				.then(() => 'field-error' as const)
				.catch(() => 'timeout' as const),
			page
				.getByTestId('register-error')
				.waitFor({ state: 'visible', timeout: 20_000 })
				.then(() => 'general-error' as const)
				.catch(() => 'timeout' as const)
		]);

		// success, or a real field-level error (e.g. duplicate email) - return
		// either way and let the caller's own assertions judge the outcome.
		if (result !== 'general-error') return;
		if (attempt === 8) return; // exhausted retries; let the caller's own assertion report the failure
	}
}

/**
 * Fills and submits the login form via the real UI. /auth/login shares one
 * IP-keyed fixed-window rate-limit bucket (30 req/min,
 * backend/internal/modules/auth/handler.go publicLimit) with every OTHER
 * concurrently-running E2E spec's register/login/verify calls on this
 * machine, so an occasional transient rate-limit hiccup here is expected
 * cross-test interference, not a bug in this flow - retry the submission
 * a few times (spanning more than one fixed window) before giving up.
 */
async function loginViaUi(page: Page, email: string, password: string): Promise<void> {
	for (let attempt = 1; attempt <= 8; attempt++) {
		await page.goto('/login');
		// Same SvelteKit hydration race as submitRegisterForm above - wait for
		// the network to go quiet before filling the fastest-filled (email)
		// field, especially since this race widens under concurrent-suite load.
		await page.waitForLoadState('networkidle');
		await page.locator('input[name="email"]').fill(email);
		await page.locator('input[name="password"]').fill(password);
		await page.locator('button[type="submit"]').click();
		try {
			await page.waitForURL((url) => !url.pathname.startsWith('/login'), { timeout: 20_000 });
			return;
		} catch (err) {
			if (attempt === 8) throw err;
		}
	}
}

test.describe('Flow 1 — register, verify email, login', () => {
	test('a freshly registered client verifies via the mock mail sink and logs in', async ({
		page,
		request
	}) => {
		// loginViaUi retries across the shared /auth/* rate-limit bucket, which
		// can take up to ~80s worst case - well past the 30s default timeout.
		test.setTimeout(220_000);

		const email = `${unique('e2e-register')}@example.test`;
		const password = 'RegisterFlow!2026';

		await submitRegisterForm(page, {
			email,
			password,
			firstName: 'Budi',
			lastName: 'Santoso',
			address1: 'Jl. Merdeka No. 1',
			city: 'Jakarta',
			state: 'DKI Jakarta',
			postcode: '10110',
			phone: '+6281234567890'
		});

		// No auto-login: the register action returns a "check your email" panel.
		await expect(page.getByTestId('register-success')).toBeVisible({ timeout: 15_000 });

		// A second register attempt with the same email must be rejected as a
		// conflict (proves the account was actually persisted, not just queued).
		// The backend 409s and the form re-renders with a field-level error on
		// the email input (feauth.register.emailTaken), not the top banner.
		await submitRegisterForm(page, {
			email,
			password,
			firstName: 'Budi',
			lastName: 'Santoso',
			address1: 'Jl. Merdeka No. 1',
			city: 'Jakarta',
			state: 'DKI Jakarta',
			postcode: '10110',
			phone: '+6281234567890'
		});
		await expect(page.getByTestId('register-form')).toBeVisible({ timeout: 15_000 });
		await expect(page.getByTestId('register-email').getByRole('alert')).toBeVisible();

		// Capture the verification link the backend actually sent through the mock
		// mail HTTP driver, and extract its token.
		const token = await getVerifyEmailToken(request, email);

		// Visit the verify-email link exactly like a real inbox click would.
		await page.goto(`/verify-email?token=${encodeURIComponent(token)}`);
		await expect(page.getByTestId('verify-email-success')).toBeVisible({ timeout: 15_000 });

		// A reload of the post-verification landing page must stay success (token
		// is consumed, but the ?verified=1 marker keeps the page from re-POSTing it).
		await page.reload();
		await expect(page.getByTestId('verify-email-success')).toBeVisible();

		// Re-using the same (now consumed) token must fail as invalid/expired.
		await page.goto(`/verify-email?token=${encodeURIComponent(token)}`);
		await expect(page.getByTestId('verify-email-error')).toBeVisible({ timeout: 15_000 });

		// Now log in with the verified account and land on the client dashboard.
		await loginViaUi(page, email, password);
		await expect(page).toHaveURL(/\/dashboard/, { timeout: 15_000 });
		await expect(page.getByTestId('dashboard-welcome')).toBeVisible({ timeout: 15_000 });
	});
});

test.describe('Flow 8 — RBAC', () => {
	test('seeded staff (no settings permission) cannot reach /admin/settings', async ({ page }) => {
		// loginViaUi retries across the shared /auth/* rate-limit bucket, which
		// can take up to ~80s worst case - well past the 30s default timeout.
		test.setTimeout(220_000);

		// staff@e2e.test / StaffE2E!2026 seeded with every adminops permission
		// EXCEPT "settings" (see backend/cmd/seed).
		await loginViaUi(page, 'staff@e2e.test', 'StaffE2E!2026');
		await expect(page).toHaveURL(/\/admin$/, { timeout: 15_000 });

		// Staff DOES have the "clients" permission - sanity check the account
		// actually landed in the admin area and isn't just broadly locked out.
		await page.goto('/admin/clients');
		await expect(page).toHaveURL(/\/admin\/clients/);

		// Settings is the one module withheld from this staff account: the SSR
		// load hits GET /api/v1/admin/settings, gets 403 FORBIDDEN from
		// RequirePermission("settings"), and the page itself 403s.
		const response = await page.goto('/admin/settings');
		expect(response?.status()).toBe(403);
	});

	test('a plain client cannot reach any /admin route', async ({ page, request }) => {
		// loginViaUi retries across the shared /auth/* rate-limit bucket, which
		// can take up to ~80s worst case - well past the 30s default timeout.
		test.setTimeout(220_000);

		// Register a fresh client via the API directly (this flow is about the
		// RBAC guard, not the registration UX - that's covered by Flow 1 above).
		const email = `${unique('e2e-rbac-client')}@example.test`;
		const password = 'RbacClient!2026';
		const registerRes = await withAuthRateLimitRetry(() =>
			request.post('http://localhost:8080/api/v1/auth/register', {
				data: {
					email,
					password,
					captcha_token: 'e2e-dummy-captcha-token',
					first_name: 'Siti',
					last_name: 'Rahayu',
					address1: 'Jl. Sudirman No. 5',
					city: 'Bandung',
					state: 'Jawa Barat',
					postcode: '40111',
					country: 'ID',
					phone: '+6281298765432'
				}
			})
		);
		expect(registerRes.ok()).toBeTruthy();

		// New client accounts are created active (email verification gates
		// checkout, not login - see auth.Service.Login), so we can log straight in.
		await loginViaUi(page, email, password);
		await expect(page).toHaveURL(/\/dashboard/, { timeout: 15_000 });

		// Direct navigation to any /admin route must bounce a plain client away
		// (the (admin) layout guard redirects non-admin/staff roles to /dashboard).
		await page.goto('/admin');
		await expect(page).toHaveURL(/\/dashboard/, { timeout: 15_000 });

		await page.goto('/admin/settings');
		await expect(page).toHaveURL(/\/dashboard/, { timeout: 15_000 });

		await page.goto('/admin/clients');
		await expect(page).toHaveURL(/\/dashboard/, { timeout: 15_000 });
	});
});

test.describe('Open-redirect protection (safeRedirect)', () => {
	// A `redirect` query param like `/\evil.example` passes a naive
	// `startsWith('/') && !startsWith('//')` same-origin check (no leading
	// double-slash), but the WHATWG URL parser that browsers use to resolve a
	// `Location` header treats the leading backslash the same as a second
	// slash, turning it into a protocol-relative URL to a different origin.
	// Both call sites in +page.server.ts (the post-login action redirect and
	// the already-logged-in bounce in `load`) must reject such payloads and
	// fall back to the user's real home instead of navigating off-site.
	const evilRedirect = '/login?redirect=/\\evil.example';

	test('a backslash-prefixed redirect target cannot bounce the user off-site after login', async ({
		page
	}) => {
		// loginViaUi-style retry loop (see helper above) across the shared
		// /auth/* rate-limit bucket, which can take up to ~80s worst case.
		test.setTimeout(220_000);

		for (let attempt = 1; attempt <= 8; attempt++) {
			await page.goto(evilRedirect);
			await page.waitForLoadState('networkidle');
			await page.locator('input[name="email"]').fill('staff@e2e.test');
			await page.locator('input[name="password"]').fill('StaffE2E!2026');
			await page.locator('button[type="submit"]').click();
			try {
				await page.waitForURL((url) => !url.pathname.startsWith('/login'), { timeout: 20_000 });
				break;
			} catch (err) {
				if (attempt === 8) throw err;
			}
		}

		// Must land on the seeded staff account's real home (same-origin
		// fallback), never on the attacker host from the payload above.
		await expect(page).toHaveURL(/^http:\/\/localhost:5173\/admin/, { timeout: 15_000 });
	});

	test('a backslash-prefixed redirect target cannot bounce an already-logged-in user off-site', async ({
		page
	}) => {
		test.setTimeout(220_000);

		await loginViaUi(page, 'staff@e2e.test', 'StaffE2E!2026');
		await expect(page).toHaveURL(/\/admin$/, { timeout: 15_000 });

		// Re-visiting /login while already authenticated hits the `load`
		// bounce redirect - the other safeRedirect call site.
		await page.goto(evilRedirect);
		await expect(page).toHaveURL(/^http:\/\/localhost:5173\/admin/, { timeout: 15_000 });
	});
});
