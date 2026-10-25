import { expect, test, type APIRequestContext } from './fixtures';
import {
	API_BASE,
	MOCK_BASE,
	adminFindClientId,
	adminToken,
	authHeaders,
	loginApi,
	newApi,
	payInvoiceViaMock,
	registerLogin,
	registerVerifyLogin,
	setSessionCookies,
	unique,
	uniqueDomainLabel,
	waitForMailMatch,
	withAuthRateLimitRetry,
	type Client
} from './helpers';

/**
 * Domain sniper - the client side (paid up front).
 *
 * A client picks a plan, sees the price breakdown, pays ONE invoice (service
 * fee + the domain's registration price) and the hunt starts the moment it is
 * paid. Losing credits the domain price (plus any success fee) back to account
 * credit; the service fee is kept. The mockserver's RDash hooks play the
 * registrar (see admin-snipes.spec.ts); hunt timings are tuned down through the
 * admin settings, restored in afterAll.
 *
 * Money (IDR, no tax in the e2e settings): plan "Std" = fee 50,000 + a 150,000
 * domain = 200,000 invoice, 150,000 credited back on a loss.
 */

const FAST = {
	'snipe.burst_interval_ms': 30,
	'snipe.burst_jitter_ms': 0,
	'snipe.patient_interval_seconds': 1,
	'snipe.burst_lead_seconds': 0,
	'snipe.pause_probe_seconds': 2
};
const DEFAULTS = {
	'snipe.burst_interval_ms': 200,
	'snipe.burst_jitter_ms': 100,
	'snipe.patient_interval_seconds': 10,
	'snipe.burst_lead_seconds': 60,
	'snipe.pause_probe_seconds': 60
};

interface SnipeRow {
	id: number;
	domain_name: string;
	status: string;
	invoice_id: number | null;
	refund_amount: number;
	refundable_domain: number;
	refundable_fee: number;
	attempts: number;
}

test.describe('client domain snipes', () => {
	let api: APIRequestContext;
	let admin: string;
	let client: Client;
	let tld: string;
	let planStd: { id: number };
	let planFree: { id: number };
	let planOff: { id: number };
	const adminApi = (path: string) => `${API_BASE}/api/v1/admin${path}`;
	const escaped = (s: string) => s.replace(/\./g, '\\.');
	const domainName = (prefix: string) => `${unique(prefix)}.${tld}`.toLowerCase();

	async function mockScript(domain: string, failTimes: number): Promise<void> {
		const res = await api.post(`${MOCK_BASE}/mock/rdash/script`, {
			data: { domain, fail_times: failTimes }
		});
		expect(res.ok(), `script ${domain}: ${await res.text()}`).toBeTruthy();
	}

	async function putSettings(values: Record<string, number | boolean>): Promise<void> {
		const res = await api.put(adminApi('/settings'), { headers: authHeaders(admin), data: values });
		expect(res.ok(), `settings: ${await res.text()}`).toBeTruthy();
	}

	async function createPlan(data: Record<string, unknown>): Promise<{ id: number }> {
		const res = await api.post(adminApi('/snipe-plans'), { headers: authHeaders(admin), data });
		expect(res.status(), `plan: ${await res.text()}`).toBe(201);
		return (await res.json()).data as { id: number };
	}

	/**
	 * POST /snipes as `who`; returns the raw response for status assertions.
	 * Creating is rate limited per IP (10/min) and this spec makes more hunts
	 * than that from one address, so a RATE_LIMITED reply is retried after the
	 * window rather than treated as a failure.
	 */
	function postSnipe(who: Client, data: Record<string, unknown>) {
		return withAuthRateLimitRetry(() =>
			api.post(`${API_BASE}/api/v1/snipes`, { headers: authHeaders(who.accessToken), data })
		);
	}

	async function startSnipe(
		who: Client,
		domain: string,
		extra: Record<string, unknown> = {}
	): Promise<SnipeRow> {
		const res = await postSnipe(who, { domain, plan_id: planStd.id, ...extra });
		expect(res.status(), `create snipe ${domain}: ${await res.text()}`).toBe(201);
		return (await res.json()).data as SnipeRow;
	}

	async function getSnipe(who: Client, id: number): Promise<SnipeRow> {
		const res = await api.get(`${API_BASE}/api/v1/snipes/${id}`, {
			headers: authHeaders(who.accessToken)
		});
		expect(res.ok(), await res.text()).toBeTruthy();
		return ((await res.json()).data as { snipe: SnipeRow }).snipe;
	}

	async function waitForStatus(
		who: Client,
		id: number,
		status: string,
		timeout = 90_000
	): Promise<SnipeRow> {
		await expect
			.poll(async () => (await getSnipe(who, id)).status, {
				timeout,
				intervals: [500, 1000, 2000],
				message: `snipe ${id} should become ${status}`
			})
			.toBe(status);
		return getSnipe(who, id);
	}

	async function credit(who: Client): Promise<number> {
		const res = await api.get(`${API_BASE}/api/v1/account/credit`, {
			headers: authHeaders(who.accessToken)
		});
		expect(res.ok()).toBeTruthy();
		return ((await res.json()).data as { balance: number }).balance;
	}

	async function payAndActivate(who: Client, snipe: SnipeRow): Promise<void> {
		await payInvoiceViaMock(api, who.accessToken, snipe.invoice_id!);
		await waitForStatus(who, snipe.id, 'active');
	}

	test.beforeAll(async () => {
		test.setTimeout(180_000);
		api = await newApi();
		admin = await adminToken(api);
		client = await registerVerifyLogin(api, 'snipeclient');

		tld = uniqueDomainLabel();
		const regs = await api.get(adminApi('/registrars'), { headers: authHeaders(admin) });
		const registrar = ((await regs.json()).data as Array<{ id: number; name: string }>).find(
			(r) => r.name === 'rdash'
		);
		expect(registrar, 'seeded rdash registrar').toBeTruthy();
		const price = await api.post(adminApi('/tld-pricing'), {
			headers: authHeaders(admin),
			data: {
				tld,
				registrar_id: registrar!.id,
				active: true,
				min_years: 1,
				max_years: 3,
				register_prices: { '1': 150000, '2': 290000, '3': 420000 },
				renew_prices: { '1': 160000 },
				transfer_price: 150000
			}
		});
		expect(price.ok(), `tld pricing: ${await price.text()}`).toBeTruthy();

		planStd = await createPlan({
			name: `Std ${tld}`,
			window_days: 30,
			fee: 50000,
			success_fee: 0,
			sort: 0,
			active: true
		});
		planFree = await createPlan({
			name: `Free ${tld}`,
			window_days: 7,
			fee: 0,
			success_fee: 0,
			sort: 1,
			active: true
		});
		planOff = await createPlan({
			name: `Old ${tld}`,
			window_days: 30,
			fee: 1000,
			success_fee: 0,
			sort: 2,
			active: false
		});

		// Roomy cap so the tests do not trip over each other's open hunts; the
		// cap test lowers it on purpose.
		await putSettings({ 'snipe.enabled': true, 'snipe.max_active_per_client': 20, ...FAST });
	});

	test.afterAll(async () => {
		// Stop leftovers, drop the plans' availability and the private TLD, restore the knobs.
		const mine = await api
			.get(`${API_BASE}/api/v1/snipes`, {
				headers: authHeaders(client.accessToken),
				params: { per_page: 100 }
			})
			.catch(() => null);
		if (mine?.ok()) {
			for (const s of ((await mine.json()).data ?? []) as SnipeRow[]) {
				await api
					.post(`${API_BASE}/api/v1/snipes/${s.id}/cancel`, {
						headers: authHeaders(client.accessToken)
					})
					.catch(() => {});
			}
		}
		for (const p of [planStd, planFree, planOff]) {
			if (!p) continue;
			await api
				.put(adminApi(`/snipe-plans/${p.id}`), {
					headers: authHeaders(admin),
					data: {
						name: `retired-${p.id}`,
						window_days: 1,
						fee: 0,
						success_fee: 0,
						sort: 999,
						active: false
					}
				})
				.catch(() => {});
		}
		const list = await api
			.get(adminApi('/tld-pricing'), { headers: authHeaders(admin) })
			.catch(() => null);
		const row = list?.ok()
			? ((await list.json()).data as Array<{ id: number; tld: string }>).find((r) => r.tld === tld)
			: undefined;
		if (row) await api.delete(adminApi(`/tld-pricing/${row.id}`), { headers: authHeaders(admin) });
		await putSettings({ ...DEFAULTS, 'snipe.max_active_per_client': 5 }).catch(() => {});
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, client.accessToken, client.refreshToken);
	});

	test('a client prices a hunt, pays one invoice, the hunt starts, wins when the domain drops, and the client is told', async ({
		page
	}) => {
		test.setTimeout(180_000);
		const domain = domainName('win');
		await mockScript(domain, 1); // taken now; a probing hunt never changes that

		// A brand-new account has no hunts: the list invites the first one.
		const fresh = await registerVerifyLogin(api, 'snipefresh');
		await setSessionCookies(page.context(), fresh.accessToken, fresh.refreshToken);
		await page.goto('/snipes');
		await expect(page.getByTestId('snipe-empty-cta')).toBeVisible();
		await setSessionCookies(page.context(), client.accessToken, client.refreshToken);

		await page.goto('/snipes');
		await page.getByTestId('snipe-new-cta').click();
		await expect(page).toHaveURL(/\/snipes\/new$/);

		// Only active plans are offered.
		await expect(page.getByTestId(`snipe-plan-${planStd.id}`)).toBeVisible();
		await expect(page.getByTestId(`snipe-plan-${planFree.id}`)).toBeVisible();
		await expect(page.getByTestId(`snipe-plan-${planOff.id}`)).toHaveCount(0);

		// The price preview follows the form.
		await page.getByTestId('snipe-domain-input').fill(domain);
		await page.getByTestId(`snipe-plan-${planStd.id}`).check();
		await expect(page.getByTestId('snipe-quote-total')).toHaveText('Rp200.000,-');
		await expect(page.getByTestId('snipe-quote-refundable')).toHaveText('Rp150.000,-');
		await page.getByTestId('snipe-years-select').selectOption('2');
		await expect(page.getByTestId('snipe-quote-total')).toHaveText('Rp340.000,-'); // 50,000 + 290,000
		await page.getByTestId('snipe-years-select').selectOption('1');
		await expect(page.getByTestId('snipe-quote-total')).toHaveText('Rp200.000,-');
		await page.getByTestId(`snipe-plan-${planFree.id}`).check();
		await expect(page.getByTestId('snipe-quote-total')).toHaveText('Rp150.000,-'); // no service fee
		await page.getByTestId(`snipe-plan-${planStd.id}`).check();
		await expect(page.getByTestId('snipe-quote-total')).toHaveText('Rp200.000,-');

		// Submitting creates the hunt and lands on the invoice to pay.
		await page.getByTestId('snipe-submit').click();
		await expect(page).toHaveURL(/\/billing\/invoices\/\d+$/, { timeout: 20_000 });
		const invoiceId = Number(page.url().split('/').pop());

		const listed = await api.get(`${API_BASE}/api/v1/snipes`, {
			headers: authHeaders(client.accessToken),
			params: { search: domain }
		});
		const created = ((await listed.json()).data as SnipeRow[])[0];
		expect(created.status).toBe('pending_payment');
		expect(created.invoice_id).toBe(invoiceId);
		expect(created.refundable_domain + created.refundable_fee).toBe(200000);

		// Before payment the hunt is not running and offers Pay Now.
		await page.goto(`/snipes/${created.id}`);
		await expect(page.getByTestId('snipe-status')).toContainText('Awaiting payment');
		await expect(page.getByTestId('snipe-pay')).toBeVisible();
		expect(
			await api
				.get(`${MOCK_BASE}/mock/rdash/attempts`, { params: { domain } })
				.then((r) => r.json())
		).toMatchObject({ attempts: 0 });

		// Pay: the hunt starts by itself.
		await payInvoiceViaMock(api, client.accessToken, invoiceId);
		await waitForStatus(client, created.id, 'active');
		await page.reload();
		await expect(page.getByTestId('snipe-status')).toContainText('Hunting');
		await expect(page.getByTestId('snipe-phase')).toBeVisible();
		await expect(page.getByTestId('snipe-cancel')).toBeVisible();
		await expect(page.getByTestId('snipe-events')).toContainText('Payment received');

		// The domain drops.
		await mockScript(domain, 0);
		const won = await waitForStatus(client, created.id, 'won');
		expect(won.attempts).toBeGreaterThanOrEqual(1);

		await page.reload();
		await expect(page.getByTestId('snipe-status')).toContainText('Secured');
		await expect(page.getByTestId('snipe-events')).toContainText('registered');
		await expect(page.getByTestId('snipe-cancel')).toHaveCount(0);
		// The money is spent on the domain: no "credited back if not won" line.
		await expect(page.getByTestId('snipe-refundable')).toHaveCount(0);
		await page.getByTestId('snipe-view-domain').click();
		await expect(page).toHaveURL(/\/domains\/\d+$/);
		await expect(page.getByText(domain).first()).toBeVisible();

		const mailed = await waitForMailMatch(
			api,
			client.email,
			new RegExp(`(${escaped(domain)})[\\s\\S]{0,120}(registered|didaftarkan)`)
		);
		expect(mailed).toBe(domain);
		// Winning consumes the money: nothing is credited back.
		const detail = await getSnipe(client, created.id);
		expect(detail.refund_amount).toBe(0);

		await page.goto(`/snipes?search=${encodeURIComponent(domain)}`);
		await expect(page.getByTestId(`snipe-row-status-${created.id}`)).toContainText('Secured');
	});

	test('cancelling a paid hunt credits the domain price back and keeps the service fee', async ({
		page
	}) => {
		test.setTimeout(150_000);
		const domain = domainName('refund');
		await mockScript(domain, 1);
		const snipe = await startSnipe(client, domain);
		await payAndActivate(client, snipe);
		const before = await credit(client);

		await page.goto(`/snipes/${snipe.id}`);
		await expect(page.getByTestId('snipe-refundable')).toHaveText('Rp150.000,-');
		await page.getByTestId('snipe-cancel').click();
		await page.getByTestId('snipe-cancel-confirm').click();
		await expect(page.getByTestId('snipe-status')).toContainText('Cancelled', { timeout: 15_000 });
		await expect(page.getByTestId('snipe-refunded')).toHaveText('Rp150.000,-');
		await expect(page.getByTestId('snipe-cancel')).toHaveCount(0);

		expect(await credit(client)).toBe(before + 150000);
		const after = await getSnipe(client, snipe.id);
		expect(after.refund_amount).toBe(150000);
		await waitForMailMatch(
			api,
			client.email,
			new RegExp(`(${escaped(domain)})[\\s\\S]{0,160}(dibatalkan|cancelled)`)
		);

		// A cancelled hunt never registers the domain, even if it drops. (The worker
		// notices the cancellation on its next 2s heartbeat: let it, then drop it.)
		await page.waitForTimeout(6_000);
		await mockScript(domain, 0);
		await page.waitForTimeout(4_000);
		expect((await getSnipe(client, snipe.id)).status).toBe('cancelled');
		const attempts = await api.get(`${MOCK_BASE}/mock/rdash/attempts`, { params: { domain } });
		expect(((await attempts.json()) as { attempts: number }).attempts).toBe(0);

		// Cancelling twice is a clean conflict, not a second refund.
		const again = await api.post(`${API_BASE}/api/v1/snipes/${snipe.id}/cancel`, {
			headers: authHeaders(client.accessToken)
		});
		expect(again.status()).toBe(409);
		expect(await credit(client)).toBe(before + 150000);
	});

	test('cancelling an unpaid hunt voids its invoice', async ({ page }) => {
		test.setTimeout(90_000);
		const domain = domainName('unpaid');
		await mockScript(domain, 1);
		const snipe = await startSnipe(client, domain);

		await page.goto(`/snipes/${snipe.id}`);
		await expect(page.getByTestId('snipe-pay')).toBeVisible();
		await page.getByTestId('snipe-cancel').click();
		await page.getByTestId('snipe-cancel-confirm').click();
		await expect(page.getByTestId('snipe-status')).toContainText('Cancelled', { timeout: 15_000 });
		await expect(page.getByTestId('snipe-pay')).toHaveCount(0);
		await expect(page.getByTestId('snipe-refunded')).toHaveCount(0);

		const inv = await api.get(`${API_BASE}/api/v1/invoices/${snipe.invoice_id}`, {
			headers: authHeaders(client.accessToken)
		});
		const body = (await inv.json()).data as { invoice?: { status?: string }; status?: string };
		expect(body.invoice?.status ?? body.status).toBe('cancelled');

		// The name is free again: the same client can start a fresh hunt for it.
		const again = await startSnipe(client, domain);
		expect(again.id).not.toBe(snipe.id);
	});

	test('a payment already in flight when the hunt is cancelled is credited back, not lost', async () => {
		test.setTimeout(120_000);
		const domain = domainName('inflight');
		await mockScript(domain, 1);
		const snipe = await startSnipe(client, domain);
		const before = await credit(client);

		// The client opens a gateway payment (a pending transaction now exists) ...
		const pay = await api.post(`${API_BASE}/api/v1/invoices/${snipe.invoice_id}/pay`, {
			headers: authHeaders(client.accessToken),
			data: { method: 'BC' }
		});
		expect(pay.ok(), `start payment: ${await pay.text()}`).toBeTruthy();
		const { reference } = (await pay.json()).data as { reference?: string };
		expect(reference, 'the mock gateway hands out a payment reference').toBeTruthy();

		// ... then cancels the hunt. Voiding the invoice under that payment would
		// strand the money, so the invoice is left payable.
		const cancel = await api.post(`${API_BASE}/api/v1/snipes/${snipe.id}/cancel`, {
			headers: authHeaders(client.accessToken)
		});
		expect(cancel.ok(), `cancel: ${await cancel.text()}`).toBeTruthy();
		expect((await getSnipe(client, snipe.id)).status).toBe('cancelled');
		const open = await api.get(`${API_BASE}/api/v1/invoices/${snipe.invoice_id}`, {
			headers: authHeaders(client.accessToken)
		});
		const openBody = (await open.json()).data as { invoice?: { status?: string }; status?: string };
		expect(openBody.invoice?.status ?? openBody.status).not.toBe('cancelled');

		// The payment lands anyway: everything paid (fee included - the hunt never
		// ran) goes to account credit, once.
		const confirm = await api.post(`${MOCK_BASE}/mock/duitku/pay/${reference}`);
		expect(confirm.ok(), `mock duitku pay: ${await confirm.text()}`).toBeTruthy();
		await expect.poll(() => credit(client), { timeout: 30_000 }).toBe(before + 200000);
		const after = await getSnipe(client, snipe.id);
		expect(after.status).toBe('cancelled');
		expect(after.refund_amount).toBe(200000);
		const paid = await api.get(`${API_BASE}/api/v1/invoices/${snipe.invoice_id}`, {
			headers: authHeaders(client.accessToken)
		});
		const paidBody = (await paid.json()).data as { invoice?: { status?: string }; status?: string };
		expect(paidBody.invoice?.status ?? paidBody.status).toBe('paid');
	});

	test('a hunt nobody paid for is cancelled, with its invoice, once the invoice is a day overdue', async () => {
		test.setTimeout(180_000);
		const domain = domainName('stale');
		await mockScript(domain, 1);
		const snipe = await startSnipe(client, domain);

		// An unpaid invoice that is only due is still payable ...
		await new Promise((r) => setTimeout(r, 2_000));
		expect((await getSnipe(client, snipe.id)).status).toBe('pending_payment');

		// ... a day past due it is not: the sweep (once a minute) releases the name.
		const yesterday = new Date(Date.now() - 2 * 24 * 3600 * 1000).toISOString().slice(0, 10);
		const patch = await api.patch(adminApi(`/invoices/${snipe.invoice_id}`), {
			headers: authHeaders(admin),
			data: { due_date: yesterday }
		});
		expect(patch.ok(), `patch invoice: ${await patch.text()}`).toBeTruthy();

		await waitForStatus(client, snipe.id, 'cancelled', 150_000);
		const inv = await api.get(`${API_BASE}/api/v1/invoices/${snipe.invoice_id}`, {
			headers: authHeaders(client.accessToken)
		});
		const body = (await inv.json()).data as { invoice?: { status?: string }; status?: string };
		expect(body.invoice?.status ?? body.status).toBe('cancelled');
	});

	test('guards: a registrable domain, duplicates, bad plans, unverified email, incomplete profile', async ({
		page
	}) => {
		test.setTimeout(150_000);

		// UI: a name anyone can register right now is not worth a hunt.
		await page.goto('/snipes/new');
		await page.getByTestId('snipe-domain-input').fill(domainName('free'));
		await page.getByTestId(`snipe-plan-${planStd.id}`).check();
		await expect(page.getByTestId('snipe-quote-total')).toBeVisible();
		await page.getByTestId('snipe-submit').click();
		await expect(page.getByTestId('snipe-create-error')).toContainText(/available/i);

		// UI: a TLD nobody priced cannot be quoted.
		await page
			.getByTestId('snipe-domain-input')
			.fill(`${unique('np')}.unpriced-${uniqueDomainLabel()}`);
		await expect(page.getByTestId('snipe-quote-error')).toContainText(/pricing/i, {
			timeout: 10_000
		});

		// API: duplicate open hunt for the same name.
		const dup = domainName('dup');
		await mockScript(dup, 1);
		await startSnipe(client, dup);
		const second = await postSnipe(client, { domain: dup, plan_id: planStd.id });
		expect(second.status()).toBe(409);

		// API: inactive / unknown plan, bad years, bad windows, bad nameservers.
		const taken = domainName('bad');
		await mockScript(taken, 1);
		expect((await postSnipe(client, { domain: taken, plan_id: planOff.id })).status()).toBe(422);
		expect((await postSnipe(client, { domain: taken, plan_id: 999999999 })).status()).toBe(422);
		expect(
			(await postSnipe(client, { domain: taken, plan_id: planStd.id, years: 4 })).status()
		).toBe(422); // tld max is 3
		expect(
			(
				await postSnipe(client, {
					domain: taken,
					plan_id: planStd.id,
					hunt_until: new Date(Date.now() - 60_000).toISOString()
				})
			).status()
		).toBe(422);
		// A drop window needs both ends and may span at most 6 hours: inside it the
		// hunt fires a request every few hundred ms, so it must not be open-ended.
		const windowStart = Date.now() + 3_600_000;
		expect(
			(
				await postSnipe(client, {
					domain: taken,
					plan_id: planStd.id,
					hunt_from: new Date(windowStart).toISOString()
				})
			).status()
		).toBe(422);
		expect(
			(
				await postSnipe(client, {
					domain: taken,
					plan_id: planStd.id,
					hunt_from: new Date(windowStart).toISOString(),
					hunt_until: new Date(windowStart + 6 * 3_600_000 + 60_000).toISOString()
				})
			).status()
		).toBe(422);
		expect(
			(
				await postSnipe(client, {
					domain: taken,
					plan_id: planStd.id,
					nameservers: ['ns1.example.net']
				})
			).status()
		).toBe(422);
		expect(
			(await postSnipe(client, { domain: 'not a domain', plan_id: planStd.id })).status()
		).toBe(422);

		// API: an unverified email may not buy a hunt (same gate as checkout).
		const unverified = await registerLogin(api, 'snipeunver');
		expect((await postSnipe(unverified, { domain: taken, plan_id: planStd.id })).status()).toBe(
			403
		);

		// API: a profile without the address the registrar needs.
		const bare = {
			email: `${unique('snipenoaddr')}@e2e.test`,
			password: 'Cl1entPass!2026'
		};
		const made = await api.post(adminApi('/clients'), {
			headers: authHeaders(admin),
			data: { ...bare, first_name: 'No', last_name: 'Address' }
		});
		expect(made.status(), await made.text()).toBe(201);
		const { accessToken } = await loginApi(api, bare.email, bare.password);
		// An admin-created client is unverified, and the verification gate comes
		// first; lift it for this one call so the PROFILE check is what answers.
		await putSettings({ 'security.require_email_verification': false });
		let noAddr;
		try {
			noAddr = await postSnipe(
				{ ...bare, accessToken, refreshToken: '' },
				{ domain: taken, plan_id: planStd.id }
			);
		} finally {
			await putSettings({ 'security.require_email_verification': true });
		}
		expect(noAddr.status()).toBe(422);
		expect(await noAddr.text()).toMatch(/profile is missing details/i);
	});

	test('a client sees only their own hunts, and is limited to the configured number of open ones', async () => {
		test.setTimeout(150_000);
		const owner = await registerVerifyLogin(api, 'snipeown');
		const other = await registerVerifyLogin(api, 'snipeoth');
		const domain = domainName('own');
		await mockScript(domain, 1);
		const mine = await startSnipe(owner, domain);

		// Another client gets a 404, never a 403: the snipe's existence is not revealed.
		expect(
			(
				await api.get(`${API_BASE}/api/v1/snipes/${mine.id}`, {
					headers: authHeaders(other.accessToken)
				})
			).status()
		).toBe(404);
		expect(
			(
				await api.post(`${API_BASE}/api/v1/snipes/${mine.id}/cancel`, {
					headers: authHeaders(other.accessToken)
				})
			).status()
		).toBe(404);
		const theirs = await api.get(`${API_BASE}/api/v1/snipes`, {
			headers: authHeaders(other.accessToken),
			params: { per_page: 100 }
		});
		expect(((await theirs.json()).data as SnipeRow[]).map((s) => s.id)).not.toContain(mine.id);
		// The id in the query string is ignored: the client comes from the token.
		const spoof = await api.get(`${API_BASE}/api/v1/snipes`, {
			headers: authHeaders(other.accessToken),
			params: { client_id: await adminFindClientId(api, admin, owner.email), per_page: 100 }
		});
		expect(((await spoof.json()).data as SnipeRow[]).map((s) => s.id)).not.toContain(mine.id);
		// And the owner's view never carries operator fields.
		const view = await api.get(`${API_BASE}/api/v1/snipes/${mine.id}`, {
			headers: authHeaders(owner.accessToken)
		});
		const text = await view.text();
		expect(text).not.toContain('last_error');
		expect(text).not.toContain('lease_');

		try {
			await putSettings({ 'snipe.max_active_per_client': 1 });
			const another = domainName('cap');
			await mockScript(another, 1);
			const over = await postSnipe(owner, { domain: another, plan_id: planStd.id });
			expect(over.status()).toBe(409);
			expect(await over.text()).toMatch(/open snipes/i);
			// Cancelling the open one frees the slot.
			expect(
				(
					await api.post(`${API_BASE}/api/v1/snipes/${mine.id}/cancel`, {
						headers: authHeaders(owner.accessToken)
					})
				).ok()
			).toBeTruthy();
			expect((await postSnipe(owner, { domain: another, plan_id: planStd.id })).status()).toBe(201);
		} finally {
			await putSettings({ 'snipe.max_active_per_client': 20 });
		}
	});

	test('the hunts list filters by status and domain and offers the page-size selector', async ({
		page
	}) => {
		test.setTimeout(150_000);
		const a = domainName('lista');
		const b = domainName('listb');
		await mockScript(a, 1);
		await mockScript(b, 1);
		const sa = await startSnipe(client, a);
		const sb = await startSnipe(client, b);
		await api.post(`${API_BASE}/api/v1/snipes/${sb.id}/cancel`, {
			headers: authHeaders(client.accessToken)
		});

		await page.goto('/snipes');
		await page.waitForLoadState('networkidle');
		const select = page.getByTestId('page-size-select');
		await expect(select).toBeVisible();
		await expect(select).toHaveValue('10');
		expect(await select.locator('option').allTextContents()).toEqual([
			'10',
			'25',
			'50',
			'100',
			'500',
			'1000'
		]);
		await select.selectOption('50');
		await expect(page).toHaveURL(/per_page=50/);
		await expect(page).toHaveURL(/page=1\b/);

		await page.goto(`/snipes?search=${encodeURIComponent(a)}`);
		await expect(
			page.getByTestId('snipes-table').locator('[data-testid^="row-snipe-"]')
		).toHaveCount(1);
		await expect(page.getByTestId(`row-snipe-${sa.id}`)).toContainText(a);
		await expect(page.getByTestId(`snipe-row-status-${sa.id}`)).toContainText('Awaiting payment');

		await page.goto(`/snipes?search=${encodeURIComponent(b)}&status=cancelled`);
		await expect(page.getByTestId(`snipe-row-status-${sb.id}`)).toContainText('Cancelled');
		await page.goto(`/snipes?search=${encodeURIComponent(b)}&status=active`);
		await expect(page.getByTestId(`row-snipe-${sb.id}`)).toHaveCount(0);

		// The filter form drives the same query string, and a row opens its hunt.
		await page.goto('/snipes');
		await page.getByTestId('snipe-search-input').fill(a);
		await page.getByTestId('snipe-status-filter').selectOption('pending_payment');
		await page.getByTestId('snipe-filter-submit').click();
		await expect(page).toHaveURL(/status=pending_payment/);
		await page.getByTestId(`row-snipe-${sa.id}`).click();
		await expect(page).toHaveURL(new RegExp(`/snipes/${sa.id}$`));
	});

	test('the client nav links to the hunts list and the start form', async ({ page }) => {
		await page.goto('/dashboard');
		// The links may sit in a closed dropdown: assert where they point, not that they are open.
		await expect(page.getByTestId('nav-snipes').first()).toHaveAttribute('href', '/snipes');
		await expect(page.locator('.ca-nav a[href="/snipes/new"]').first()).toBeAttached();
		await page.goto('/snipes');
		await expect(page.getByTestId('snipe-new-cta')).toBeVisible();
	});
});
