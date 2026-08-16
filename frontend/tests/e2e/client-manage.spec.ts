import { expect, request, test, type APIRequestContext } from './fixtures';
import { withAuthRateLimitRetry } from './helpers';

/**
 * Flow 4 (PRD §13.2 / docs/E2E.md §5.4): client manage service & domain.
 *
 * Setup (register -> verify -> login -> order+pay hosting -> order+pay a
 * domain registration) is driven directly against the live backend + mock
 * registrar/gateway via API calls (faster & more reliable than re-driving
 * flows 1-3 through the UI, and this spec may run before/after/concurrently
 * with those specs against the SAME shared backend+DB). The actual flow this
 * spec is responsible for verifying - changing a service's password and a
 * domain's nameservers via the client UI - is driven through real browser
 * interactions against the SvelteKit frontend.
 *
 * All test data (email, domain name) is timestamp+random suffixed so this
 * spec never collides with other specs/runs sharing the same backend + DB.
 */

const API_BASE = 'http://localhost:8080';
const MOCK_BASE = 'http://localhost:9090';

// `mode: 'serial'` orders the two tests; `timeout` extends the per-test
// budget (the two UI tests) to tolerate a shared, sometimes-busy dev stack.
// NOTE: this does NOT extend `beforeAll`'s own timeout - that budget is
// independent and is bumped separately inside the hook via test.setTimeout().
test.describe.configure({ mode: 'serial', timeout: 90_000 });

test.describe('client-manage: service password + domain nameservers', () => {
	const suffix = `${Date.now()}-${Math.floor(Math.random() * 1_000_000)}`;
	const email = `client-manage-${suffix}@e2e.test`;
	const password = 'ClientManage!2026';
	const domainName = `e2e-manage-${suffix}.com`;

	let api: APIRequestContext;
	let accessToken = '';
	let refreshToken = '';
	let serviceId = 0;
	let domainId = 0;

	/** Click a button and retry until a resulting element becomes visible.
	 *  Needed because the "open modal" buttons on these pages are plain
	 *  `<button type="button" onclick=...>` elements with no href/form fallback
	 *  - the click only does anything once Svelte has hydrated and attached
	 *  the listener. Playwright's own actionability checks only require the
	 *  element to be visible/stable/enabled, not that hydration has completed,
	 *  so a single click can silently no-op on a freshly-loaded page under a
	 *  busy dev server. Retrying the click is the standard workaround. */
	async function clickUntilVisible(
		page: import('@playwright/test').Page,
		buttonTestId: string,
		resultTestId: string
	): Promise<void> {
		await expect(async () => {
			await page.getByTestId(buttonTestId).click();
			await expect(page.getByTestId(resultTestId)).toBeVisible({ timeout: 2_000 });
		}).toPass({ timeout: 30_000 });
	}

	function authHeaders(): Record<string, string> {
		return { authorization: `Bearer ${accessToken}` };
	}

	/** Poll the mock mail sink until a message to `email` shows up, then pull the
	 *  verify-email token out of its body (the email links to
	 *  `${FRONTEND_URL}/verify-email?token=<token>`). */
	async function extractVerifyToken(): Promise<string> {
		let token: string | null = null;
		await expect
			.poll(
				async () => {
					const res = await api.get(`${MOCK_BASE}/mail/messages`, { params: { to: email } });
					const msgs = (await res.json()) as Array<{ html?: string; text?: string }>;
					if (msgs.length === 0) return null;
					const blob = `${msgs[0].html ?? ''} ${msgs[0].text ?? ''}`;
					const m = blob.match(/token=([^"'&\s<]+)/);
					token = m ? decodeURIComponent(m[1]) : null;
					return token;
				},
				{ timeout: 20_000, message: 'waiting for verification email to arrive' }
			)
			.not.toBeNull();
		if (!token) throw new Error('verification token not found in mail body');
		return token;
	}

	/** Pay an invoice via the mock Duitku gateway end-to-end: request a payment
	 *  instruction from the real backend, then hit the mock's "Bayar Sekarang"
	 *  action directly (same effect as clicking through the hosted mock payment
	 *  page), which delivers the callback to the backend synchronously. Polls
	 *  until the backend reflects the invoice as paid. */
	async function payInvoice(invoiceId: number): Promise<void> {
		const payRes = await api.post(`${API_BASE}/api/v1/invoices/${invoiceId}/pay`, {
			headers: authHeaders(),
			data: { method: 'BC' }
		});
		expect(payRes.ok(), `pay invoice ${invoiceId}: ${await payRes.text()}`).toBeTruthy();
		const payBody = (await payRes.json()).data as { reference?: string; status?: string };

		if (payBody.status !== 'paid') {
			expect(payBody.reference, 'expected a gateway reference for a pending payment').toBeTruthy();
			const confirmRes = await api.post(`${MOCK_BASE}/mock/duitku/pay/${payBody.reference}`);
			expect(confirmRes.ok(), `mock duitku pay: ${await confirmRes.text()}`).toBeTruthy();
		}

		await expect
			.poll(
				async () => {
					const res = await api.get(`${API_BASE}/api/v1/invoices/${invoiceId}`, {
						headers: authHeaders()
					});
					const body = (await res.json()).data as { invoice?: { status?: string }; status?: string };
					return body.invoice?.status ?? body.status ?? null;
				},
				{ timeout: 30_000, message: `waiting for invoice ${invoiceId} to be paid` }
			)
			.toBe('paid');
	}

	/** Poll the client's services list until one reaches `active` (provisioning
	 *  runs async on the worker once the invoice is paid). */
	async function pollForActiveService(): Promise<number> {
		let id = 0;
		await expect
			.poll(
				async () => {
					const res = await api.get(`${API_BASE}/api/v1/services`, { headers: authHeaders() });
					const rows = ((await res.json()).data ?? []) as Array<{ id: number; status: string }>;
					const active = rows.find((r) => r.status === 'active');
					if (active) id = active.id;
					return active?.status ?? rows[0]?.status ?? null;
				},
				{ timeout: 45_000, message: 'waiting for the hosting service to become active' }
			)
			.toBe('active');
		return id;
	}

	/** Poll the client's domains list until the freshly-ordered domain reaches
	 *  `active` (registrar registration also runs async on the worker). */
	async function pollForActiveDomain(): Promise<number> {
		let id = 0;
		await expect
			.poll(
				async () => {
					const res = await api.get(`${API_BASE}/api/v1/domains`, { headers: authHeaders() });
					const rows = ((await res.json()).data ?? []) as Array<{
						id: number;
						name: string;
						status: string;
					}>;
					const mine = rows.find((r) => r.name === domainName);
					if (mine) id = mine.id;
					return mine?.status ?? null;
				},
				{ timeout: 45_000, message: `waiting for domain ${domainName} to become active` }
			)
			.toBe('active');
		return id;
	}

	test.beforeAll(async () => {
		// beforeAll has its own timeout budget (default 30s from playwright.config),
		// separate from the per-test `timeout` set via describe.configure above.
		// This hook does two full order+pay+poll round trips against a live,
		// possibly-shared/busy backend + async worker, so give it a lot of room.
		test.setTimeout(180_000);

		api = await request.newContext();

		// 1. Register a brand-new client (unique email - safe under concurrent specs).
		const regRes = await withAuthRateLimitRetry(() =>
			api.post(`${API_BASE}/api/v1/auth/register`, {
				data: {
					email,
					password,
					captcha_token: 'e2e-dummy-captcha-token',
					first_name: 'Client',
					last_name: 'Manage',
					address1: 'Jl. E2E Testing No. 1',
					city: 'Jakarta',
					state: 'DKI Jakarta',
					postcode: '12110',
					country: 'ID',
					phone: '081200000000'
				}
			})
		);
		expect(regRes.ok(), `register: ${await regRes.text()}`).toBeTruthy();

		// 2. Verify email via the mock mail sink + real verify-email endpoint.
		const token = await extractVerifyToken();
		const verifyRes = await withAuthRateLimitRetry(() =>
			api.post(`${API_BASE}/api/v1/auth/verify-email`, { data: { token } })
		);
		expect(verifyRes.ok(), `verify-email: ${await verifyRes.text()}`).toBeTruthy();

		// 3. Log in for real tokens.
		const loginRes = await withAuthRateLimitRetry(() =>
			api.post(`${API_BASE}/api/v1/auth/login`, { data: { email, password, captcha_token: 'e2e-dummy-captcha-token' } })
		);
		expect(loginRes.ok(), `login: ${await loginRes.text()}`).toBeTruthy();
		const loginBody = (await loginRes.json()).data as {
			access_token: string;
			refresh_token: string;
		};
		accessToken = loginBody.access_token;
		refreshToken = loginBody.refresh_token;
		expect(accessToken).toBeTruthy();

		// 4. Find the seeded shared-hosting product (seed's slug, falling back to
		//    any visible product priced monthly so this spec survives reseeding).
		// NOTE: GET /api/v1/products actually returns product *groups*, each with
		// a nested `products: [...]` array (`{data:[{id,name,slug,products:[...]}]}`)
		// - not a flat product array. The service upgrade picker in
		// services/[id]/+page.server.ts used to assume the flat shape (silently
		// getting an empty list) - now fixed to flatten via CatalogGroup - but
		// this setup step still parses the real nested shape directly since it
		// doesn't need the loader.
		const productsRes = await api.get(`${API_BASE}/api/v1/products`);
		expect(productsRes.ok(), `list products: ${await productsRes.text()}`).toBeTruthy();
		const groups = ((await productsRes.json()).data ?? []) as Array<{
			products?: Array<{
				id: number;
				slug: string;
				hidden?: boolean;
				pricing?: Array<{ cycle: string }>;
			}>;
		}>;
		const products = groups.flatMap((g) => g.products ?? []);
		const hosting =
			products.find((p) => p.slug === 'e2e-shared-hosting') ??
			products.find((p) => !p.hidden && p.pricing?.some((pr) => pr.cycle === 'monthly'));
		expect(hosting, 'seeded hosting product should exist').toBeTruthy();

		// 5. Order the hosting product and pay for it. A domain is required on
		//    the item: internal/modules/orders/activate.go derives the service's
		//    `domain`/`username` from it, and the cpanel provisioning adapter
		//    (internal/integration/cpanel/operations.go Create) hard-requires a
		//    non-empty domain - without one, provision:create fails forever
		//    ("invalid cpanel account parameters") and the service never leaves
		//    `pending`.
		// The label before the first dot must be unique on its own within its
		// first 8 chars: internal/domain/generate.go's UsernameFromDomain derives
		// the cPanel account username by lowercasing it, stripping non-alnum, and
		// truncating to 8 chars - a `hosting-<timestamp>` prefix collides across
		// runs started in the same second (all runs today share the same leading
		// digits), which the mock WHM then rejects as "account exists".
		const hostingDomain = `${Math.random().toString(36).slice(2, 10)}.hosting.e2e.test`;
		const orderRes = await api.post(`${API_BASE}/api/v1/orders`, {
			headers: authHeaders(),
			data: {
				captcha_token: 'e2e-dummy-captcha-token',
				items: [
					{
						item_type: 'product',
						product_id: hosting!.id,
						cycle: 'monthly',
						domain: hostingDomain
					}
				]
			}
		});
		expect(orderRes.ok(), `create hosting order: ${await orderRes.text()}`).toBeTruthy();
		const orderBody = (await orderRes.json()).data as { invoice: { id: number } };
		await payInvoice(orderBody.invoice.id);
		serviceId = await pollForActiveService();
		expect(serviceId).toBeGreaterThan(0);

		// 6. Order a fresh domain registration and pay for it (name deliberately
		//    avoids the substring "taken" so the RDash mock reports it available).
		const domainOrderRes = await api.post(`${API_BASE}/api/v1/orders`, {
			headers: authHeaders(),
			data: { captcha_token: 'e2e-dummy-captcha-token', items: [{ item_type: 'domain_register', domain: domainName, domain_years: 1 }] }
		});
		expect(domainOrderRes.ok(), `create domain order: ${await domainOrderRes.text()}`).toBeTruthy();
		const domainOrderBody = (await domainOrderRes.json()).data as { invoice: { id: number } };
		await payInvoice(domainOrderBody.invoice.id);
		domainId = await pollForActiveDomain();
		expect(domainId).toBeGreaterThan(0);
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	/** Authenticate the browser context the same way the SvelteKit BFF does:
	 *  plain `access_token`/`refresh_token` cookies (see
	 *  frontend/src/lib/server/session.ts) - no UI login needed since the
	 *  tokens were already minted during setup. */
	test.beforeEach(async ({ context }) => {
		await context.addCookies([
			{
				name: 'access_token',
				value: accessToken,
				url: 'http://localhost:5173',
				httpOnly: true,
				sameSite: 'Lax'
			},
			{
				name: 'refresh_token',
				value: refreshToken,
				url: 'http://localhost:5173',
				httpOnly: true,
				sameSite: 'Lax'
			},
			// Force English strings so this spec's text assertions don't have to
			// match both locales (hooks.server.ts defaults to 'id' otherwise).
			{ name: 'locale', value: 'en', url: 'http://localhost:5173' }
		]);
	});

	test('change the active service password and see success feedback', async ({ page }) => {
		await page.goto(`/services/${serviceId}`);
		// Let Svelte finish hydrating before interacting - see clickUntilVisible's
		// doc comment for why a too-early click can silently no-op.
		await page.waitForLoadState('networkidle');

		await expect(page.getByTestId('service-status')).toContainText(/active/i);
		// Real display names from the ServiceView enrichment - never the
		// "Produk #<id>" / "#<server_id>" fallbacks.
		await expect(page.getByTestId('service-product')).toContainText('E2E Shared Hosting');
		await expect(page.getByTestId('service-server')).toContainText('localhost');

		await clickUntilVisible(
			page,
			'service-change-password-button',
			'service-change-password-form'
		);

		const newPassword = `NewSvcPass!${suffix.slice(-6)}`;
		await page.locator('#field-password').fill(newPassword);
		await page.locator('#field-password_confirm').fill(newPassword);
		await page.getByTestId('service-change-password-submit').click();

		// Success feedback: the toast fires and the modal closes.
		await expect(page.getByText(/kata sandi|password/i).last()).toBeVisible({ timeout: 15_000 });
		await expect(page.getByTestId('service-change-password-form')).toBeHidden({ timeout: 15_000 });
	});

	test("update the active domain's nameservers and see them reflected", async ({ page }) => {
		const ns1 = `ns1.e2e-${suffix}.test`;
		const ns2 = `ns2.e2e-${suffix}.test`;

		const nsURL = `/domains/${domainId}?tab=nameservers`;
		await page.goto(nsURL);
		// Let Svelte finish hydrating before interacting. This matters more here
		// than on the service page: `ns-save` is a real `<button type="submit">`
		// inside `<form action="?/nameservers">` - clicking it before use:enhance
		// attaches falls back to a native (non-JS) form submission, which POSTs
		// to the `action` URL verbatim and so drops the `?tab=nameservers` query
		// param from the resulting page (landing back on the Overview tab).
		await page.waitForLoadState('networkidle');

		await expect(page.getByTestId('domain-status')).toContainText(/active/i);

		await page.locator('#field-ns1').fill(ns1);
		await page.locator('#field-ns2').fill(ns2);
		await page.getByTestId('ns-save').click();

		// Success feedback (toast) after the SvelteKit form action round-trips.
		await expect(page.getByText(/nameserver|disimpan|saved/i).last()).toBeVisible({
			timeout: 15_000
		});

		// Re-navigate (rather than a bare reload, in case the URL drifted) to
		// confirm the mockserver registrar actually stored + returns the new
		// values (not just local UI state). Retried as a whole: under a busy
		// full-suite run this fresh GET can occasionally lag a beat behind the
		// save that just completed, so a single static read can catch it
		// mid-flight - re-navigating rather than just waiting longer picks up
		// the real value on the next fetch instead of masking a real bug.
		await expect(async () => {
			await page.goto(nsURL);
			await page.waitForLoadState('networkidle');
			await expect(page.locator('#field-ns1')).toHaveValue(ns1, { timeout: 2_000 });
			await expect(page.locator('#field-ns2')).toHaveValue(ns2, { timeout: 2_000 });
		}).toPass({ timeout: 20_000 });

		// Cross-check via the real API too (belt & suspenders on the "stored and
		// returned by the mockserver" requirement).
		const res = await api.get(`${API_BASE}/api/v1/domains/${domainId}`, { headers: authHeaders() });
		const body = (await res.json()).data as { nameservers?: unknown };
		const stored = Array.isArray(body.nameservers) ? (body.nameservers as string[]) : [];
		expect(stored).toContain(ns1);
		expect(stored).toContain(ns2);
	});
});
