import { expect, test, type APIRequestContext } from './fixtures';
import {
	API_BASE,
	adminFindClientId,
	adminToken,
	authHeaders,
	newApi,
	registerVerifyLogin,
	setSessionCookies,
	unique,
	type Client
} from './helpers';

/**
 * Admin ticket controls that used to call API routes that do not exist
 * (PATCH /admin/tickets/:id, POST .../close, a priority endpoint): status,
 * priority and close on the detail page, the priority/client filters and the
 * bulk Close + Auto Refresh on the list.
 */

interface TicketRow {
	id: number;
	status: string;
	priority: string;
}

test.describe('admin ticket controls', () => {
	let api: APIRequestContext;
	let token: string;
	let client: Client;
	let clientId: number;
	let departmentId: number;

	async function openTicket(priority: 'low' | 'medium' | 'high'): Promise<number> {
		const res = await api.post(`${API_BASE}/api/v1/tickets`, {
			headers: authHeaders(client.accessToken),
			data: { department_id: departmentId, subject: `${unique('admin-ctl')} ${priority}`, priority, message: 'E2E admin ticket controls' }
		});
		expect(res.ok(), `open ticket: ${await res.text()}`).toBeTruthy();
		return ((await res.json()).data as { ticket: { id: number } }).ticket.id;
	}

	async function adminTicket(id: number): Promise<TicketRow> {
		const res = await api.get(`${API_BASE}/api/v1/admin/tickets`, { headers: authHeaders(token), params: { client_id: clientId, per_page: 1000 } });
		expect(res.ok(), await res.text()).toBeTruthy();
		const row = (((await res.json()).data ?? []) as TicketRow[]).find((t) => t.id === id);
		expect(row, `ticket ${id} should be listed for client ${clientId}`).toBeTruthy();
		return row!;
	}

	test.beforeAll(async () => {
		test.setTimeout(90_000);
		api = await newApi();
		client = await registerVerifyLogin(api, 'tkctl');
		token = await adminToken(api);
		clientId = await adminFindClientId(api, token, client.email);
		const depts = (await (await api.get(`${API_BASE}/api/v1/ticket-departments`)).json()).data as Array<{ id: number }>;
		expect(depts.length, 'a seeded ticket department is required').toBeGreaterThan(0);
		departmentId = depts[0].id;
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, token);
	});

	test('detail page: change status and priority, then close the ticket', async ({ page }) => {
		const id = await openTicket('low');
		await page.goto(`/admin/tickets/${id}`);

		await page.getByTestId('ticket-priority-select').selectOption('high');
		await expect.poll(async () => (await adminTicket(id)).priority, { timeout: 15_000, message: 'priority saved' }).toBe('high');

		await page.getByTestId('ticket-status-select').selectOption('on_hold');
		await expect.poll(async () => (await adminTicket(id)).status, { timeout: 15_000, message: 'status saved' }).toBe('on_hold');

		await page.getByTestId('ticket-close-button').click();
		await page.getByRole('dialog').locator('button').last().click();
		await expect.poll(async () => (await adminTicket(id)).status, { timeout: 15_000, message: 'ticket closed' }).toBe('closed');
		await expect(page.getByTestId('ticket-close-button')).toHaveCount(0);
		await expect(page.getByTestId('ticket-priority-select')).toBeDisabled();
		await expect(page.getByTestId('ticket-client-link')).toBeVisible();
	});

	test('list: the priority filter narrows the table', async ({ page }) => {
		const low = await openTicket('low');
		const high = await openTicket('high');

		await page.goto(`/admin/tickets?priority=high&per_page=1000`);
		await expect(page.getByTestId(`row-ticket-${high}`)).toBeVisible();
		await expect(page.getByTestId(`row-ticket-${low}`)).toHaveCount(0);

		await page.goto(`/admin/tickets?priority=low&per_page=1000`);
		await expect(page.getByTestId(`row-ticket-${low}`)).toBeVisible();
		await expect(page.getByTestId(`row-ticket-${high}`)).toHaveCount(0);
	});

	test('list: bulk Close closes only the selected tickets', async ({ page }) => {
		const first = await openTicket('medium');
		const second = await openTicket('medium');
		const untouched = await openTicket('medium');

		await page.goto('/admin/tickets?per_page=1000');
		await page.getByTestId(`ticket-select-${first}`).check();
		await page.getByTestId(`ticket-select-${second}`).check();
		await page.getByTestId('ticket-bulk-close').click();
		await page.getByRole('dialog').locator('button').last().click();

		await expect.poll(async () => (await adminTicket(first)).status, { timeout: 20_000, message: 'first closed' }).toBe('closed');
		await expect.poll(async () => (await adminTicket(second)).status, { timeout: 20_000, message: 'second closed' }).toBe('closed');
		expect((await adminTicket(untouched)).status, 'unselected ticket stays open').toBe('open');
	});

	test('list: Auto Refresh picks up a ticket opened while the page is on screen', async ({ page }) => {
		await page.clock.install();
		await page.goto('/admin/tickets?per_page=1000');

		const button = page.getByTestId('ticket-auto-refresh');
		await expect(button).toContainText('Off');
		await button.click();
		await expect(button).toContainText('On');

		const fresh = await openTicket('medium');
		await expect(page.getByTestId(`row-ticket-${fresh}`)).toHaveCount(0);

		await page.clock.fastForward(31_000);
		await expect(page.getByTestId(`row-ticket-${fresh}`)).toBeVisible({ timeout: 15_000 });

		// Switching it off stops the polling: a later ticket does not appear by itself.
		await button.click();
		await expect(button).toContainText('Off');
		const later = await openTicket('medium');
		await page.clock.fastForward(61_000);
		await expect(page.getByTestId(`row-ticket-${later}`)).toHaveCount(0);
	});
});
