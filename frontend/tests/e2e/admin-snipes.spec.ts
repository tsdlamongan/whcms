import { expect, test, type APIRequestContext } from './fixtures';
import {
	API_BASE,
	MOCK_BASE,
	adminFindClientId,
	adminToken,
	authHeaders,
	newApi,
	registerVerifyLogin,
	setSessionCookies,
	unique,
	uniqueDomainLabel,
	waitForMailMatch,
	type Client
} from './helpers';

/**
 * Domain sniper - the admin side (Phase 1: free hunts started by an admin).
 *
 * A hunt is a background job in the WORKER process that keeps trying to
 * register a dropping domain at the registrar. The mockserver's RDash hooks
 * stand in for the registrar:
 *
 *   POST /mock/rdash/script  {domain, fail_times}  taken for N attempts, then free
 *   GET  /mock/rdash/attempts?domain=              how many register calls it saw
 *   POST /mock/rdash/balance {amount}              the reseller balance
 *
 * The worker's hunt timings are tuned down through the admin settings so a
 * hunt reacts in a second or two instead of the production-safe 10s/200ms
 * (the worker re-reads settings every ~10s, so polls below are generous).
 * Settings are restored in afterAll: they persist in whmcs_e2e across runs.
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
	phase: string;
	attempts: number;
	source: string;
}

test.describe('admin domain snipes', () => {
	let api: APIRequestContext;
	let token: string;
	let client: Client;
	let clientId: number;
	let tld: string;
	const created: number[] = [];

	const adminApi = (path: string) => `${API_BASE}/api/v1/admin${path}`;

	async function mockScript(domain: string, failTimes: number): Promise<void> {
		const res = await api.post(`${MOCK_BASE}/mock/rdash/script`, {
			data: { domain, fail_times: failTimes }
		});
		expect(res.ok(), `script ${domain}: ${await res.text()}`).toBeTruthy();
	}

	async function mockAttempts(domain: string): Promise<number> {
		const res = await api.get(`${MOCK_BASE}/mock/rdash/attempts`, { params: { domain } });
		expect(res.ok()).toBeTruthy();
		return ((await res.json()) as { attempts: number }).attempts;
	}

	async function setMockBalance(amount: number): Promise<void> {
		const res = await api.post(`${MOCK_BASE}/mock/rdash/balance`, { data: { amount } });
		expect(res.ok(), `balance: ${await res.text()}`).toBeTruthy();
	}

	async function putSettings(values: Record<string, number | boolean>): Promise<void> {
		const res = await api.put(adminApi('/settings'), { headers: authHeaders(token), data: values });
		expect(res.ok(), `settings: ${await res.text()}`).toBeTruthy();
	}

	async function createSnipe(
		domain: string,
		extra: Record<string, unknown> = {}
	): Promise<SnipeRow> {
		const res = await api.post(adminApi('/snipes'), {
			headers: authHeaders(token),
			data: { client_id: clientId, domain, ...extra }
		});
		expect(res.status(), `create snipe ${domain}: ${await res.text()}`).toBe(201);
		const row = ((await res.json()).data ?? {}) as SnipeRow;
		created.push(row.id);
		return row;
	}

	async function getSnipe(id: number): Promise<SnipeRow> {
		const res = await api.get(adminApi(`/snipes/${id}`), { headers: authHeaders(token) });
		expect(res.ok()).toBeTruthy();
		return ((await res.json()).data as { snipe: SnipeRow }).snipe;
	}

	async function waitForStatus(id: number, status: string, timeout = 90_000): Promise<SnipeRow> {
		await expect
			.poll(async () => (await getSnipe(id)).status, {
				timeout,
				intervals: [500, 1000, 2000],
				message: `snipe ${id} should become ${status}`
			})
			.toBe(status);
		return getSnipe(id);
	}

	const escaped = (domain: string) => domain.replace(/\./g, '\\.');

	/** A name the RDash mock treats as free unless scripted ("taken" would never drop). */
	const domainName = (prefix: string) => `${unique(prefix)}.${tld}`.toLowerCase();

	test.beforeAll(async () => {
		test.setTimeout(180_000);
		api = await newApi();
		token = await adminToken(api);
		client = await registerVerifyLogin(api, 'snipe');
		clientId = await adminFindClientId(api, token, client.email);

		// A private, priced TLD so the specs never touch another spec's pricing.
		tld = uniqueDomainLabel();
		const regs = await api.get(adminApi('/registrars'), { headers: authHeaders(token) });
		const registrar = ((await regs.json()).data as Array<{ id: number; name: string }>).find(
			(r) => r.name === 'rdash'
		);
		expect(registrar, 'seeded rdash registrar').toBeTruthy();
		const price = await api.post(adminApi('/tld-pricing'), {
			headers: authHeaders(token),
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

		await putSettings({ 'snipe.enabled': true, ...FAST });
	});

	test.afterAll(async () => {
		// Stop anything still hunting, drop the private TLD, restore the knobs.
		for (const id of created) {
			await api
				.post(adminApi(`/snipes/${id}/cancel`), { headers: authHeaders(token), data: {} })
				.catch(() => {});
		}
		const list = await api
			.get(adminApi('/tld-pricing'), { headers: authHeaders(token) })
			.catch(() => null);
		const row = list?.ok()
			? ((await list.json()).data as Array<{ id: number; tld: string }>).find((r) => r.tld === tld)
			: undefined;
		if (row) {
			await api.delete(adminApi(`/tld-pricing/${row.id}`), { headers: authHeaders(token) });
		}
		await putSettings(DEFAULTS).catch(() => {});
		await setMockBalance(10_000_000).catch(() => {});
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, token);
	});

	test('a hunt started in the UI probes while the domain is taken, then wins when it drops', async ({
		page
	}) => {
		test.setTimeout(150_000);
		const domain = domainName('drop');
		await mockScript(domain, 1); // taken, and a probing hunt never changes that

		await page.goto('/admin/snipes');
		await page.waitForLoadState('networkidle');
		await page.getByTestId('snipe-add-new').click();
		await expect(page).toHaveURL(/\/admin\/snipes\/new$/);
		await expect(page.getByTestId('snipe-create-form')).toBeVisible();

		// The client picker is search driven: narrow it to our client first.
		await page.getByTestId('snipe-create-client-search').fill(client.email);
		await page.getByTestId('snipe-create-client-search-submit').click();
		await page.waitForLoadState('networkidle');
		await page.getByTestId('snipe-create-client').selectOption(String(clientId));
		await page.getByTestId('snipe-create-domain').fill(domain);
		await page.getByTestId('snipe-create-submit').click();

		await expect(page).toHaveURL(/\/admin\/snipes\/\d+$/, { timeout: 20_000 });
		const id = Number(page.url().split('/').pop());
		created.push(id);
		await expect(page.getByTestId('snipe-status')).toHaveText(/active/i);
		await expect(page.getByText(domain).first()).toBeVisible();

		// While the name is taken the hunt only probes: no register call at all.
		await page.waitForTimeout(4_000);
		expect(await mockAttempts(domain)).toBe(0);
		expect((await getSnipe(id)).status).toBe('active');

		// The domain drops.
		await mockScript(domain, 0);
		const won = await waitForStatus(id, 'won');
		expect(won.attempts).toBeGreaterThanOrEqual(1);
		expect(await mockAttempts(domain)).toBe(1);

		await page.reload();
		await expect(page.getByTestId('snipe-status')).toHaveText(/won/i);
		await expect(page.getByTestId('snipe-events')).toContainText('won');
		await expect(page.getByTestId('snipe-cancel-button')).toHaveCount(0);

		// The won domain exists under the client, already active.
		await page.getByTestId('snipe-view-domain').click();
		await expect(page).toHaveURL(/\/admin\/domains\/\d+$/);
		await expect(page.getByTestId('domain-status')).toHaveText(/active/i);
		await expect(page.getByText(domain).first()).toBeVisible();

		// The client was told.
		const mailed = await waitForMailMatch(api, client.email, new RegExp(`(${escaped(domain)})`));
		expect(mailed).toBe(domain);

		// And the list shows it won.
		await page.goto(`/admin/snipes?search=${encodeURIComponent(domain)}`);
		await expect(page.getByTestId(`snipe-row-status-${id}`)).toHaveText(/won/i);
	});

	test('inside the drop window the hunt bursts register calls until the registrar says yes', async () => {
		test.setTimeout(120_000);
		const domain = domainName('burst');
		await mockScript(domain, 3); // three 409s, then it registers

		const now = Date.now();
		const snipe = await createSnipe(domain, {
			hunt_from: new Date(now - 60_000).toISOString(),
			hunt_until: new Date(now + 3_600_000).toISOString()
		});
		const won = await waitForStatus(snipe.id, 'won');

		// 3 refusals + the winning call, and the final count survives the finish.
		expect(await mockAttempts(domain)).toBe(4);
		expect(won.attempts).toBe(4);

		// Exactly one domain row, active, owned by the client.
		const doms = await api.get(adminApi('/domains'), {
			headers: authHeaders(token),
			params: { search: domain }
		});
		const rows = ((await doms.json()).data ?? []) as Array<{
			name: string;
			status: string;
			client_id: number;
		}>;
		expect(rows).toHaveLength(1);
		expect(rows[0]).toMatchObject({ name: domain, status: 'active', client_id: clientId });
	});

	test('cancelling stops the hunt for good', async ({ page }) => {
		test.setTimeout(120_000);
		const domain = domainName('cancel');
		await mockScript(domain, 1);
		const snipe = await createSnipe(domain);

		await page.goto(`/admin/snipes/${snipe.id}`);
		await expect(page.getByTestId('snipe-status')).toHaveText(/active/i);
		await page.getByTestId('snipe-cancel-button').click();
		await page.getByTestId('snipe-cancel-confirm').click();
		await expect(page.getByTestId('snipe-status')).toHaveText(/cancelled/i, { timeout: 15_000 });
		await expect(page.getByTestId('snipe-cancel-button')).toHaveCount(0);
		await expect(page.getByTestId('snipe-events')).toContainText('cancelled');

		// The cancellation mail names this domain (id or en wording, whichever locale the client has).
		await waitForMailMatch(
			api,
			client.email,
			new RegExp(`${escaped(domain)}[\\s\\S]{0,80}(dibatalkan|cancelled)`)
		);

		// The worker learns of the cancellation on its next heartbeat (every 2s tick):
		// give it that, THEN drop the domain - nothing may register it.
		await page.waitForTimeout(6_000);
		await mockScript(domain, 0);
		await page.waitForTimeout(5_000);
		expect(await mockAttempts(domain)).toBe(0);
		expect((await getSnipe(snipe.id)).status).toBe('cancelled');

		// A second cancel is a clean conflict, not a crash.
		const again = await api.post(adminApi(`/snipes/${snipe.id}/cancel`), {
			headers: authHeaders(token),
			data: {}
		});
		expect(again.status()).toBe(409);
	});

	test('guards: unpriced TLD, duplicate hunt, incomplete profile and bad windows are rejected', async ({
		page
	}) => {
		test.setTimeout(120_000);
		const taken = domainName('dup');
		await mockScript(taken, 1);
		await createSnipe(taken);

		// UI: a duplicate open hunt for the same name is refused with a visible error.
		await page.goto('/admin/snipes/new');
		await page.waitForLoadState('networkidle');
		await page.getByTestId('snipe-create-client-search').fill(client.email);
		await page.getByTestId('snipe-create-client-search-submit').click();
		await page.waitForLoadState('networkidle');
		await page.getByTestId('snipe-create-client').selectOption(String(clientId));
		await page.getByTestId('snipe-create-domain').fill(taken);
		await page.getByTestId('snipe-create-submit').click();
		await expect(page.getByTestId('snipe-create-error')).toContainText(/already open/i);

		// UI: a TLD nobody priced cannot be hunted.
		await page
			.getByTestId('snipe-create-domain')
			.fill(`${unique('np')}.unpriced-${uniqueDomainLabel()}`);
		await page.getByTestId('snipe-create-submit').click();
		await expect(page.getByTestId('snipe-create-error')).toContainText(/pricing/i);

		// API: bad windows and nameservers.
		const post = (data: Record<string, unknown>) =>
			api.post(adminApi('/snipes'), {
				headers: authHeaders(token),
				data: { client_id: clientId, domain: domainName('bad'), ...data }
			});
		const past = new Date(Date.now() - 60_000).toISOString();
		const future = new Date(Date.now() + 3_600_000).toISOString();
		expect((await post({ deadline_at: past })).status()).toBe(422);
		expect((await post({ hunt_until: future })).status()).toBe(422);
		expect((await post({ hunt_from: future, hunt_until: past })).status()).toBe(422);
		expect((await post({ nameservers: ['ns1.example.net'] })).status()).toBe(422);
		expect((await post({ domain: 'not a domain' })).status()).toBe(422);

		// API: a client whose profile lacks the registrar's required address
		// (registration insists on one, an admin-created client need not have it).
		const bareRes = await api.post(adminApi('/clients'), {
			headers: authHeaders(token),
			data: {
				email: `${unique('noaddr')}@e2e.test`,
				password: 'Cl1entPass!2026',
				first_name: 'No',
				last_name: 'Address'
			}
		});
		expect(bareRes.status(), await bareRes.text()).toBe(201);
		const bareClientId = ((await bareRes.json()).data as { id: number }).id;
		const bare = await post({ client_id: bareClientId });
		expect(bare.status()).toBe(422);
		expect(await bare.text()).toMatch(/registrant details/i);
	});

	test('the list filters by domain and status and offers the page-size selector', async ({
		page
	}) => {
		test.setTimeout(120_000);
		const a = domainName('lista');
		const b = domainName('listb');
		await mockScript(a, 1);
		await mockScript(b, 1);
		const sa = await createSnipe(a);
		const sb = await createSnipe(b);
		await api.post(adminApi(`/snipes/${sb.id}/cancel`), { headers: authHeaders(token), data: {} });

		await page.goto('/admin/snipes');
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

		// Search narrows to exactly the one row.
		await page.goto(`/admin/snipes?search=${encodeURIComponent(a)}`);
		await expect(
			page.getByTestId('snipes-table').locator('[data-testid^="row-snipe-"]')
		).toHaveCount(1);
		await expect(page.getByTestId(`row-snipe-${sa.id}`)).toHaveText(a);

		// Status filter: the cancelled one is found under "cancelled", not "active".
		await page.goto(`/admin/snipes?search=${encodeURIComponent(b)}&status=cancelled`);
		await expect(page.getByTestId(`snipe-row-status-${sb.id}`)).toHaveText(/cancelled/i);
		await page.goto(`/admin/snipes?search=${encodeURIComponent(b)}&status=active`);
		await expect(page.getByTestId(`row-snipe-${sb.id}`)).toHaveCount(0);

		// The filter form drives the same query string.
		await page.goto('/admin/snipes');
		await page.getByRole('button', { name: 'Search/Filter' }).click();
		await page.getByTestId('snipe-search-input').fill(a);
		await page.getByTestId('snipe-status-filter').selectOption('active');
		await page.getByTestId('snipe-filter-submit').click();
		await expect(page).toHaveURL(/status=active/);
		await expect(page.getByTestId(`row-snipe-${sa.id}`)).toBeVisible();
	});

	test('overview and hunt settings are editable from the page', async ({ page }) => {
		test.setTimeout(90_000);
		await page.goto('/admin/snipes');
		await page.waitForLoadState('networkidle');

		await expect(page.getByTestId('snipes-overview')).toBeVisible();
		await expect(page.getByTestId('snipe-supervisor-state')).toHaveText(/running/i);
		await expect(page.getByTestId('snipe-stat-balance')).toContainText('Rp');
		await expect(page.getByTestId('snipe-stat-hunting')).toBeVisible();

		await page.getByTestId('snipe-settings-toggle').click();
		const form = page.getByTestId('snipe-settings-form');
		await expect(form).toBeVisible();
		const field = page.getByTestId('snipe-setting-max_active_per_client');
		await expect(field).toHaveValue('5');
		await field.fill('6');
		await page.getByTestId('snipe-settings-save').click();
		await expect(page.getByText('Hunt settings saved')).toBeVisible({ timeout: 10_000 });

		const settings = await api.get(adminApi('/settings'), { headers: authHeaders(token) });
		const group = ((await settings.json()).data as { snipe: Record<string, unknown> }).snipe;
		expect(group.max_active_per_client).toBe(6);

		// A blank box is rejected, not silently saved as 0.
		await page.getByTestId('snipe-setting-max_active_per_client').fill('');
		await page.getByTestId('snipe-settings-save').click();
		await expect(page.getByText(/must be a number/i)).toBeVisible({ timeout: 10_000 });
		const kept = await api.get(adminApi('/settings'), { headers: authHeaders(token) });
		expect(
			((await kept.json()).data as { snipe: Record<string, unknown> }).snipe.max_active_per_client
		).toBe(6);

		// Put it back (settings persist across e2e runs).
		await page.getByTestId('snipe-setting-max_active_per_client').fill('5');
		await page.getByTestId('snipe-settings-save').click();
		await expect
			.poll(
				async () => {
					const again = await api.get(adminApi('/settings'), { headers: authHeaders(token) });
					return ((await again.json()).data as { snipe: Record<string, unknown> }).snipe
						.max_active_per_client;
				},
				{ timeout: 10_000 }
			)
			.toBe(5);
	});

	test('an empty reseller balance pauses every hunt, keeps the snipe active, and resumes after a top-up', async ({
		page
	}) => {
		test.setTimeout(180_000);
		const domain = domainName('broke');
		try {
			await setMockBalance(0);
			const now = Date.now();
			const snipe = await createSnipe(domain, {
				hunt_from: new Date(now - 60_000).toISOString(),
				hunt_until: new Date(now + 3_600_000).toISOString()
			});

			// The register call is refused for lack of money: the supervisor pauses.
			await expect
				.poll(
					async () => {
						const res = await api.get(adminApi('/snipes/status'), { headers: authHeaders(token) });
						return ((await res.json()).data as { pause_reason: string }).pause_reason;
					},
					{ timeout: 60_000, intervals: [500, 1000, 2000], message: 'the supervisor should pause' }
				)
				.not.toBe('');

			await page.goto('/admin/snipes');
			await expect(page.getByTestId('snipe-paused')).toBeVisible();
			await expect(page.getByTestId('snipe-supervisor-state')).toHaveText(/paused/i);
			// The snipe itself is untouched: it is the reseller account that is broken.
			expect((await getSnipe(snipe.id)).status).toBe('active');
			expect(await mockAttempts(domain)).toBeGreaterThanOrEqual(1);

			// A top-up ends the pause (the probe only resumes once the balance has risen)
			// and the hunt picks up where it left off and wins.
			await setMockBalance(10_000_000);
			await waitForStatus(snipe.id, 'won', 120_000);
			const res = await api.get(adminApi('/snipes/status'), { headers: authHeaders(token) });
			expect(((await res.json()).data as { pause_reason: string }).pause_reason).toBe('');
			await page.goto('/admin/snipes');
			await expect(page.getByTestId('snipe-paused')).toHaveCount(0);
		} finally {
			await setMockBalance(10_000_000);
		}
	});
});
