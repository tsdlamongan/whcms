import { expect, test, type APIRequestContext } from './fixtures';
import {
	API_BASE,
	adminFindClientId,
	adminToken,
	authHeaders,
	clientWithActiveService,
	createOrder,
	newApi,
	seededHostingProductId,
	setSessionCookies,
	uniqueDomainLabel,
	type Client
} from './helpers';

/**
 * Admin list endpoints used to ignore several filters (client_id, dates,
 * invoice_id, ids...), cap per_page at 100 despite the 1000 selector, and the
 * tables printed bare `#<id>` instead of the client. These specs pin the fixed
 * behaviour at the API contract level and through the admin UI.
 */

interface Row {
	id: number;
	client_id?: number;
	invoice_id?: number;
	invoice_number?: string;
	status?: string;
}

interface Party {
	client: Client;
	clientId: number;
	serviceId: number;
	invoiceId: number;
	invoiceNumber: string;
	orderId: number;
}

test.describe('admin lists: filters, names and scoping', () => {
	let api: APIRequestContext;
	let token: string;
	let productId: number;
	let a: Party;
	let b: Party;

	async function adminList(path: string, params: Record<string, string | number> = {}) {
		const res = await api.get(`${API_BASE}/api/v1/admin/${path}`, { headers: authHeaders(token), params });
		const body = await res.json();
		return { status: res.status(), rows: (body.data ?? []) as Row[], meta: body.meta as { per_page: number; total: number } | null };
	}

	async function party(prefix: string): Promise<Party> {
		const { client, serviceId } = await clientWithActiveService(api, prefix);
		const clientId = await adminFindClientId(api, token, client.email);
		const headers = authHeaders(client.accessToken);
		const invoices = ((await (await api.get(`${API_BASE}/api/v1/invoices`, { headers })).json()).data ?? []) as Row[];
		const orders = ((await (await api.get(`${API_BASE}/api/v1/orders`, { headers })).json()).data ?? []) as Row[];
		expect(invoices.length, 'client should have an invoice').toBeGreaterThan(0);
		expect(orders.length, 'client should have an order').toBeGreaterThan(0);
		return { client, clientId, serviceId, invoiceId: invoices[0].id, invoiceNumber: invoices[0].invoice_number!, orderId: orders[0].id };
	}

	test.beforeAll(async () => {
		test.setTimeout(240_000);
		api = await newApi();
		token = await adminToken(api);
		productId = await seededHostingProductId(api);
		a = await party('lsta');
		b = await party('lstb');
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, token);
	});

	test('client_id scopes orders, invoices and services to that client only', async () => {
		for (const [path, own, other] of [
			['orders', a.orderId, b.orderId],
			['invoices', a.invoiceId, b.invoiceId],
			['services', a.serviceId, b.serviceId]
		] as const) {
			const { status, rows } = await adminList(path, { client_id: a.clientId, per_page: 1000 });
			expect(status, path).toBe(200);
			expect(rows.map((r) => r.id), `${path} must include the client's own row`).toContain(own);
			expect(rows.map((r) => r.id), `${path} must not leak another client's row`).not.toContain(other);
			expect(rows.every((r) => r.client_id === a.clientId), `${path}: every row belongs to client ${a.clientId}`).toBe(true);
		}
	});

	test('product, invoice and date filters narrow the result instead of being ignored', async () => {
		const byProduct = await adminList('services', { product_id: productId, per_page: 1000 });
		expect(byProduct.rows.map((r) => r.id)).toContain(a.serviceId);
		expect((await adminList('services', { product_id: 999_999_999 })).rows, 'unknown product matches nothing').toHaveLength(0);

		const tx = await adminList('transactions', { invoice_id: a.invoiceId, per_page: 1000 });
		expect(tx.rows.length).toBeGreaterThan(0);
		expect(tx.rows.every((r) => r.invoice_id === a.invoiceId)).toBe(true);

		expect((await adminList('invoices', { date_from: '2999-01-01' })).rows, 'invoices from the far future').toHaveLength(0);
		expect((await adminList('invoices', { date_to: '2000-01-01' })).rows, 'invoices before 2000').toHaveLength(0);
		const inRange = await adminList('invoices', { date_from: '2000-01-01', date_to: '2999-12-31', per_page: 1000 });
		expect(inRange.rows.map((r) => r.id)).toEqual(expect.arrayContaining([a.invoiceId, b.invoiceId]));
		expect((await adminList('transactions', { date_from: '2999-01-01' })).rows, 'transactions from the far future').toHaveLength(0);
	});

	test('ids= resolves exactly the requested clients and invoices', async () => {
		const clients = await adminList('clients', { ids: `${a.clientId},${b.clientId}` });
		expect(clients.rows.map((r) => r.id).sort()).toEqual([a.clientId, b.clientId].sort());
		const invoices = await adminList('invoices', { ids: `${a.invoiceId}, ${b.invoiceId}` });
		expect(invoices.rows.map((r) => r.id).sort()).toEqual([a.invoiceId, b.invoiceId].sort());
	});

	test('malformed filters are rejected rather than silently widening the list', async () => {
		for (const [path, params] of [
			['services', { client_id: 'abc' }],
			['orders', { client_id: '0' }],
			['invoices', { date_from: '31-01-2026' }],
			['invoices', { ids: '1,x' }],
			['transactions', { invoice_id: '-3' }]
		] as const) {
			const { status } = await adminList(path, params);
			expect(status, `${path}?${new URLSearchParams(params).toString()}`).toBe(422);
		}
	});

	test('per_page=1000 is honoured (not capped at 100) and larger values clamp to 1000', async () => {
		for (const path of ['clients', 'orders', 'invoices', 'services', 'domains', 'tickets', 'transactions', 'products']) {
			const exact = await adminList(path, { per_page: 1000 });
			expect(exact.meta?.per_page, `${path} per_page=1000`).toBe(1000);
			const over = await adminList(path, { per_page: 5000 });
			expect(over.meta?.per_page, `${path} per_page=5000`).toBe(1000);
		}
	});

	test('list tables show the client name, not a bare #id', async ({ page }) => {
		const name = 'E2E Client';

		await page.goto('/admin/orders?per_page=1000');
		const orderRow = page.locator('tr', { has: page.getByTestId(`row-order-${a.orderId}`) });
		await expect(orderRow).toContainText(name);
		await expect(orderRow).not.toContainText(`#${a.clientId}`);

		await page.goto('/admin/services?per_page=1000');
		const serviceRow = page.locator('tr', { has: page.getByTestId(`row-service-${a.serviceId}`) });
		await expect(serviceRow).toContainText(name);

		await page.goto('/admin/invoices?per_page=1000');
		const invoiceRow = page.locator('tr', { has: page.getByTestId(`row-invoice-${a.invoiceId}`) });
		await expect(invoiceRow).toContainText(name);

		// Transactions only know their invoice: the page resolves invoice # + client.
		const tx = await adminList('transactions', { invoice_id: a.invoiceId });
		await page.goto('/admin/transactions?per_page=1000');
		const txRow = page.locator('tr', { has: page.getByTestId(`row-transaction-${tx.rows[0].id}`) });
		await expect(txRow).toContainText(name);
		await expect(txRow).toContainText(a.invoiceNumber);
	});

	test('client detail tabs list only that client\'s own records', async ({ page }) => {
		await page.goto(`/admin/clients/${a.clientId}?tab=services`);
		await expect(page.getByTestId(`row-service-${a.serviceId}`)).toBeVisible();
		await expect(page.getByTestId(`row-service-${b.serviceId}`)).toHaveCount(0);
		await expect(page.getByTestId('client-tab-count-services')).toHaveText('1');

		await page.goto(`/admin/clients/${a.clientId}?tab=invoices`);
		await expect(page.getByTestId(`row-invoice-${a.invoiceId}`)).toBeVisible();
		await expect(page.getByTestId(`row-invoice-${b.invoiceId}`)).toHaveCount(0);

		await page.goto(`/admin/clients/${b.clientId}?tab=invoices`);
		await expect(page.getByTestId(`row-invoice-${b.invoiceId}`)).toBeVisible();
		await expect(page.getByTestId(`row-invoice-${a.invoiceId}`)).toHaveCount(0);
	});

	test('invoice list date filter applies; bulk actions cancel and settle selected invoices', async ({ page }) => {
		test.setTimeout(120_000);

		// Two fresh unpaid invoices for client B.
		const items = () => [{ item_type: 'product', product_id: productId, cycle: 'monthly', domain: `${uniqueDomainLabel()}.hosting.e2e.test` }];
		const toCancel = await createOrder(api, b.client.accessToken, items());
		const toPay = await createOrder(api, b.client.accessToken, items());
		const numbers = new Map<number, string>();
		for (const id of [toCancel, toPay]) {
			numbers.set(id, (await adminList('invoices', { ids: id })).rows[0].invoice_number!);
		}
		const statusOf = async (id: number) => (await adminList('invoices', { ids: id })).rows[0].status;
		expect(await statusOf(toCancel)).toBe('unpaid');

		// Date filter through the real form: far-future range is empty, wide range lists them.
		await page.goto('/admin/invoices?date_from=2999-01-01&per_page=1000');
		await expect(page.locator('[data-testid^="row-invoice-"]')).toHaveCount(0);
		await page.goto('/admin/invoices?date_from=2000-01-01&date_to=2999-12-31&status=unpaid&per_page=1000');
		await expect(page.getByTestId(`row-invoice-${toCancel}`)).toBeVisible();
		await expect(page.getByTestId(`row-invoice-${toPay}`)).toBeVisible();

		// Bulk: Mark Cancelled on the first (ConfirmDialog's confirm is its last button).
		await page.getByRole('checkbox', { name: `Select ${numbers.get(toCancel)}` }).check();
		await page.getByTestId('invoice-bulk-cancel').click();
		await page.getByRole('dialog').locator('button').last().click();
		await expect.poll(() => statusOf(toCancel), { timeout: 20_000, message: 'invoice cancelled' }).toBe('cancelled');
		expect(await statusOf(toPay), 'unselected invoice untouched').toBe('unpaid');

		// Bulk: Mark Paid on the second.
		await page.goto('/admin/invoices?status=unpaid&per_page=1000');
		await page.getByRole('checkbox', { name: `Select ${numbers.get(toPay)}` }).check();
		await page.getByTestId('invoice-bulk-mark-paid').click();
		await page.getByRole('dialog').locator('button').last().click();
		await expect.poll(() => statusOf(toPay), { timeout: 20_000, message: 'invoice paid' }).toBe('paid');
	});
});
