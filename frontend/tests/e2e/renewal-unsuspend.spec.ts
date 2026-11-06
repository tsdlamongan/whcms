import { execFileSync } from 'node:child_process';
import { expect, test, type APIRequestContext } from './fixtures';
import { withAuthRateLimitRetry } from './helpers';

/**
 * Flow 5 (PRD §13.2): pay a renewal invoice on a suspended service and
 * confirm it comes back active (unsuspended).
 *
 * Real calendar time can't pass in a test, so per docs/E2E.md §5.5 this spec
 * fabricates the "overdue, about-to-be-suspended" state itself:
 *   - suspending the service is done through the REAL admin HTTP endpoint
 *     (POST /admin/services/:id/suspend) - a genuine admin affordance exists,
 *     so no DB write is needed for that part.
 *   - creating the *renewal* invoice cannot go through any HTTP endpoint:
 *     the only invoice-creation endpoint (POST /admin/invoices,
 *     ManualInvoiceInput) has no related_type/related_id fields, so an
 *     invoice created that way would never be recognised by
 *     billing.ProcessPaid's `case domain.RelatedServiceRenewal` and paying it
 *     would never unsuspend anything. The real GenerateRenewalInvoices cron
 *     produces that shape but only runs on its own schedule (can't be
 *     triggered on demand). So this spec inserts the invoice + invoice_item
 *     rows directly via `psql`, mirroring exactly the columns/values that
 *     cron would have written (see billing/service.go GenerateRenewalInvoices).
 *
 * Everything else (registering the client, ordering + paying for the
 * hosting product to get a real *active* service in the first place) is
 * driven through the real API so this spec is fast and self-contained; the
 * part the flow is actually about - paying the renewal invoice - is driven
 * through the client UI exactly like a real customer would.
 */

const API_BASE = 'http://localhost:8080';
const MOCK_BASE = 'http://localhost:9090';
const DATABASE_URL =
	process.env.DATABASE_URL ?? 'postgres://root:postgres@localhost:5432/whmcs_e2e?sslmode=disable';

const ADMIN_EMAIL = 'admin@e2e.test';
const ADMIN_PASSWORD = 'AdminE2E!2026';

const runId = `${Date.now()}-${Math.floor(Math.random() * 100000)}`;
const clientEmail = `e2e-renewal-${runId}@example.test`;
const clientPassword = 'RenewalE2E!2026';

// domain.UsernameFromDomain() keeps only the first 8 alnum chars before the
// first "." of the domain, so a unique *suffix* elsewhere in the domain name
// (like the email above) would still collide on username - this label must
// itself be unique within its first 8 chars, and start with a letter so
// UsernameFromDomain doesn't prepend its "starts with a digit" fallback "u".
const hostingDomain = `e${Math.random().toString(36).slice(2, 9)}.test`;

/**
 * Runs a SQL script against the live E2E Postgres via the local `psql` CLI
 * and returns the id from a trailing `RETURNING id` (if any). Even with -tA
 * (tuples-only, unaligned), psql still interleaves command tags like
 * "UPDATE 1" / "INSERT 0 1" with the actual tuple output, so pull out the
 * last line that is purely digits rather than trusting the last line as-is.
 */
function runSql(sql: string): number {
	const out = execFileSync('psql', [DATABASE_URL, '-v', 'ON_ERROR_STOP=1', '-tA'], {
		input: sql,
		encoding: 'utf-8'
	});
	const idLines = out.split('\n').filter((line) => /^\d+$/.test(line.trim()));
	return idLines.length > 0 ? Number(idLines[idLines.length - 1]) : NaN;
}

async function apiJSON<T = unknown>(
	request: APIRequestContext,
	method: 'GET' | 'POST',
	url: string,
	opts: { data?: unknown; token?: string } = {}
): Promise<{ status: number; data: T; error: { code?: string; message?: string } | null }> {
	const headers: Record<string, string> = {};
	if (opts.token) headers['Authorization'] = `Bearer ${opts.token}`;
	const res =
		method === 'GET'
			? await request.get(url, { headers })
			: await request.post(url, { headers, data: opts.data });
	const body = await res.json();
	return { status: res.status(), data: body.data as T, error: body.error ?? null };
}

/** Extracts the `token=` query value out of a verify-email link body. */
function extractToken(body: string): string {
	const match = body.match(/token=([^"'&\s<]+)/);
	if (!match) throw new Error(`no verification token found in mail body: ${body.slice(0, 300)}`);
	return decodeURIComponent(match[1]);
}

/**
 * Other E2E specs run concurrently against this same live backend, and the
 * `/auth/*` endpoints share one IP-keyed rate-limit bucket (30 req/min), so
 * this spec's own register/verify/login calls can get 429'd purely by
 * neighbouring spec traffic. Delegates to the shared helper while keeping
 * this spec's original behavior exactly: apiJSON's parsed-envelope results
 * (not APIResponse), 15 attempts, 4s backoff, and one final fresh call after
 * exhausting the attempts.
 */
async function withRateLimitRetry<T extends { status: number; error: { code?: string } | null }>(
	fn: () => Promise<T>
): Promise<T> {
	return withAuthRateLimitRetry(fn, {
		attempts: 15,
		delayMs: 4000,
		isRateLimited: (res) => res.status === 429 || res.error?.code === 'RATE_LIMITED',
		finalFreshAttempt: true
	});
}

test('overdue renewal invoice paid via client UI unsuspends the service', async ({
	page,
	request
}) => {
	test.setTimeout(240_000);

	// -------------------------------------------------------------------
	// 1. Fixture setup via the real API: register + verify + login a fresh
	//    client, order the seeded hosting product, and pay the initial
	//    invoice through the mock Duitku "Bayar Sekarang" endpoint so the
	//    worker provisions a real *active* service to suspend later.
	// -------------------------------------------------------------------
	let clientToken = '';
	let clientId = 0;
	let serviceId = 0;
	let serviceRecurringAmount = 0;

	await test.step('register + verify + login a fresh client', async () => {
		const reg = await withRateLimitRetry(() =>
			apiJSON(request, 'POST', `${API_BASE}/api/v1/auth/register`, {
				data: {
					email: clientEmail,
					password: clientPassword,
					captcha_token: 'e2e-dummy-captcha-token',
					first_name: 'E2E',
					last_name: 'Renewal',
					address1: 'Jl. E2E No. 5',
					city: 'Jakarta',
					state: 'DKI Jakarta',
					postcode: '10110',
					country: 'ID',
					phone: '081200000000'
				}
			})
		);
		expect(reg.error, JSON.stringify(reg.error)).toBeNull();

		// Pull the verification link out of the mock mail sink.
		let mailBody = '';
		await expect
			.poll(
				async () => {
					const res = await request.get(
						`${MOCK_BASE}/mail/messages?to=${encodeURIComponent(clientEmail)}`
					);
					const messages = (await res.json()) as Array<{ html?: string; text?: string }>;
					if (messages.length === 0) return '';
					mailBody = messages[0].html || messages[0].text || '';
					return mailBody;
				},
				{ message: 'waiting for the verification email to land in the mock mail sink', timeout: 20_000 }
			)
			.not.toBe('');

		const token = extractToken(mailBody);
		const verify = await withRateLimitRetry(() =>
			apiJSON(request, 'POST', `${API_BASE}/api/v1/auth/verify-email`, { data: { token } })
		);
		expect(verify.error, JSON.stringify(verify.error)).toBeNull();

		const login = await withRateLimitRetry(() =>
			apiJSON<{ access_token: string; user: { client_id: number } }>(
				request,
				'POST',
				`${API_BASE}/api/v1/auth/login`,
				{ data: { email: clientEmail, password: clientPassword, captcha_token: 'e2e-dummy-captcha-token' } }
			)
		);
		expect(login.error, JSON.stringify(login.error)).toBeNull();
		clientToken = login.data.access_token;
		clientId = login.data.user.client_id;
		expect(clientToken).toBeTruthy();
		expect(clientId).toBeGreaterThan(0);
	});

	await test.step('order the seeded hosting product and pay via mock Duitku', async () => {
		const product = await apiJSON<{ id: number; pricing: Array<{ cycle: string; price: number }> }>(
			request,
			'GET',
			`${API_BASE}/api/v1/products/e2e-shared-hosting`
		);
		expect(product.error, JSON.stringify(product.error)).toBeNull();
		const productId = product.data.id;

		// A domain is required: the cPanel adapter's Create() rejects an empty
		// domain (createacct needs one), so a domain-less hosting order would
		// never leave `pending` - give the item a unique throwaway domain.
		const order = await apiJSON<{ order: { id: number }; invoice: { id: number } }>(
			request,
			'POST',
			`${API_BASE}/api/v1/orders`,
			{
				token: clientToken,
				data: {
					captcha_token: 'e2e-dummy-captcha-token',
					items: [
						{ item_type: 'product', product_id: productId, cycle: 'monthly', domain: hostingDomain }
					]
				}
			}
		);
		expect(order.error, JSON.stringify(order.error)).toBeNull();
		const invoiceId = order.data.invoice.id;

		const pay = await apiJSON<{ reference: string; payment_url: string }>(
			request,
			'POST',
			`${API_BASE}/api/v1/invoices/${invoiceId}/pay`,
			{ token: clientToken, data: { method: 'BC' } }
		);
		expect(pay.error, JSON.stringify(pay.error)).toBeNull();
		expect(pay.data.reference).toBeTruthy();

		// Simulate the customer clicking "Bayar Sekarang" on the mock hosted
		// payment page - this is the same mock endpoint the real UI drives in
		// flow 2, called directly here since this step is just fixture setup.
		const mockPay = await request.post(`${MOCK_BASE}/mock/duitku/pay/${pay.data.reference}`);
		expect(mockPay.ok()).toBeTruthy();

		// Wait for the webhook-driven ProcessPaid -> ActivateOrder -> worker
		// provisioning to land the service in `active`.
		await expect
			.poll(
				async () => {
					const svc = await apiJSON<
						Array<{ id: number; status: string; recurring_amount: number; product_id: number }>
					>(request, 'GET', `${API_BASE}/api/v1/services?per_page=50`, { token: clientToken });
					const found = (svc.data ?? []).find((s) => s.product_id === productId);
					if (found) {
						serviceId = found.id;
						serviceRecurringAmount = found.recurring_amount;
					}
					return found?.status ?? '';
				},
				{ message: 'waiting for the ordered service to become active', timeout: 60_000 }
			)
			.toBe('active');

		expect(serviceId).toBeGreaterThan(0);
		expect(serviceRecurringAmount).toBeGreaterThan(0);
	});

	// -------------------------------------------------------------------
	// 2. Fabricate the "overdue renewal, suspended" state.
	// -------------------------------------------------------------------
	let renewalInvoiceId = 0;
	const invoiceNumber = `E2E-RENEWAL-${runId}`;

	await test.step('fabricate an open renewal invoice directly in Postgres (see file header comment)', async () => {
		// Nudge next_due_date into the past so the fixture is internally
		// consistent with a service that is actually overdue for renewal.
		runSql(`UPDATE services SET next_due_date = CURRENT_DATE - INTERVAL '5 days' WHERE id = ${serviceId};`);

		renewalInvoiceId = runSql(`
			INSERT INTO invoices
				(invoice_number, client_id, status, subtotal, discount, tax_rate, tax_total, credit_applied, total, currency, due_date, notes)
			VALUES
				('${invoiceNumber}', ${clientId}, 'unpaid', ${serviceRecurringAmount}, 0, 0, 0, 0, ${serviceRecurringAmount}, 'IDR', CURRENT_DATE - INTERVAL '5 days', 'E2E fabricated renewal invoice (see renewal-unsuspend.spec.ts)')
			RETURNING id;
		`);
		expect(renewalInvoiceId).toBeGreaterThan(0);

		runSql(`
			INSERT INTO invoice_items (invoice_id, description, amount, taxed, related_type, related_id)
			VALUES (${renewalInvoiceId}, 'E2E renewal: service #${serviceId}', ${serviceRecurringAmount}, true, 'service_renewal', ${serviceId});
		`);
	});

	await test.step('suspend the service via the real admin API', async () => {
		const adminLogin = await withRateLimitRetry(() =>
			apiJSON<{ access_token: string }>(request, 'POST', `${API_BASE}/api/v1/auth/login`, {
				data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD, captcha_token: 'e2e-dummy-captcha-token' }
			})
		);
		expect(adminLogin.error, JSON.stringify(adminLogin.error)).toBeNull();
		const adminToken = adminLogin.data.access_token;

		const suspend = await apiJSON(
			request,
			'POST',
			`${API_BASE}/api/v1/admin/services/${serviceId}/suspend`,
			{ token: adminToken, data: { reason: 'e2e: overdue renewal (fabricated for renewal-unsuspend spec)' } }
		);
		expect(suspend.error, JSON.stringify(suspend.error)).toBeNull();

		// AdminAction runs synchronously (async defaults to false), so the
		// service should already be suspended, but confirm via the API before
		// moving to the UI portion of the flow.
		const svc = await apiJSON<{ status: string }>(
			request,
			'GET',
			`${API_BASE}/api/v1/admin/services/${serviceId}`,
			{ token: adminToken }
		);
		expect(svc.data.status).toBe('suspended');
	});

	// -------------------------------------------------------------------
	// 3. Client UI: log in, see the suspended service, pay the renewal
	//    invoice, and confirm the service returns to active.
	// -------------------------------------------------------------------
	await test.step('client UI: log in and see the service suspended', async () => {
		// /auth/login shares one IP-keyed fixed-window rate-limit bucket (30
		// req/min, backend/internal/modules/auth/handler.go publicLimit) with
		// every other concurrently-running E2E spec, so retry the submission a
		// few times (spanning more than one fixed window) before failing for
		// good - a transient 429 here is cross-test interference, not a bug.
		let loggedIn = false;
		for (let attempt = 1; attempt <= 8 && !loggedIn; attempt++) {
			await page.goto('/login');
			// SvelteKit hydration race: filling before client JS hydration wires
			// up bind:value can get silently wiped by the hydration effect
			// snapping the field back to its initial empty state - widens under
			// concurrent-suite load, so always wait for network-idle first.
			await page.waitForLoadState('networkidle');
			await page.locator('input[name="email"]').fill(clientEmail);
			await page.locator('input[name="password"]').fill(clientPassword);
			await page.locator('button[type="submit"]').click();
			try {
				await expect(page).toHaveURL(/\/dashboard/, { timeout: 20_000 });
				loggedIn = true;
			} catch (err) {
				if (attempt === 8) throw err;
			}
		}

		await page.goto(`/services/${serviceId}`);
		await expect(page.getByTestId('service-status')).toContainText(/suspended|ditangguhkan/i);
	});

	await test.step('client UI: pay the renewal invoice', async () => {
		await page.goto(`/billing/invoices/${renewalInvoiceId}`);
		// SvelteKit hydration race: a click can silently no-op if it lands
		// before client JS finishes hydrating and wiring up the button's
		// handler - widens under concurrent-suite load, so wait first.
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('invoice-status')).toContainText(/unpaid|belum dibayar/i);

		// "VC" (credit card) has no VA/QRIS raw payload to render inline, so
		// it's the channel guaranteed to redirect to the mock's hosted payment
		// page below - see payments-instructions.spec.ts for VA/QRIS
		// inline-render coverage this flow deliberately doesn't exercise.
		// Credit Card sits behind the "show more" toggle (only major VA
		// banks + QRIS are shown expanded by default).
		await page.getByTestId('payment-methods-toggle').click();
		const methodButton = page.getByTestId('payment-method-VC');
		await expect(methodButton).toBeVisible();
		await methodButton.click();

		await page.locator('[data-testid="pay-button"] button').click();

		// The pay action redirects the browser to the mock Duitku hosted
		// payment page (a different origin: localhost:9090).
		await page.waitForURL(/localhost:9090\/payment\//, { timeout: 15_000 });
		await page.locator('#pay-now').click();

		// Mock server redirects back through /payments/return to the invoice
		// page; wait for that round trip to land us back on it.
		await page.waitForURL(new RegExp(`/billing/invoices/${renewalInvoiceId}`), { timeout: 20_000 });

		await expect
			.poll(
				async () => {
					await page.reload();
					return page.getByTestId('invoice-status').innerText();
				},
				{ message: 'waiting for the renewal invoice to show as paid', timeout: 30_000 }
			)
			.toMatch(/paid|lunas/i);
	});

	await test.step('client UI: confirm the service is active again (unsuspended)', async () => {
		await expect
			.poll(
				async () => {
					await page.goto(`/services/${serviceId}`);
					return page.getByTestId('service-status').innerText();
				},
				{
					message: 'waiting for the worker to process provision:unsuspend and flip the service active',
					timeout: 60_000
				}
			)
			.toMatch(/active|aktif/i);
	});
});
