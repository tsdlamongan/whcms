import { readFile } from 'node:fs/promises';
import { expect, test, type APIRequestContext } from './fixtures';
import { APP_BASE, adminToken, clientWithActiveService, newApi, setSessionCookies } from './helpers';

/**
 * Admin reports against real data: a client with a paid order + active
 * service guarantees at least one transaction, one order and one service in
 * the default (month-to-date) window. The pages must show those figures and
 * the CSV exports must carry the same numbers.
 */

const digits = (text: string | null): number => Number((text ?? '').replace(/\D/g, ''));

test.describe('admin reports', () => {
	let api: APIRequestContext;
	let token: string;

	test.beforeAll(async () => {
		test.setTimeout(180_000);
		api = await newApi();
		await clientWithActiveService(api, 'rpt');
		token = await adminToken(api);
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, token);
	});

	test('revenue report shows income and its CSV carries the same total', async ({ page }) => {
		await page.goto('/admin/reports/revenue');
		const total = digits(await page.getByTestId('revenue-total').textContent());
		expect(total, 'paid order should produce income this month').toBeGreaterThan(0);
		expect(digits(await page.getByTestId('revenue-total-count').textContent())).toBeGreaterThan(0);

		const [download] = await Promise.all([page.waitForEvent('download'), page.getByTestId('revenue-csv-link').click()]);
		expect(download.suggestedFilename()).toMatch(/revenue.*\.csv$/);
		const lines = (await readFile((await download.path())!, 'utf8')).trim().split(/\r?\n/);
		expect(lines[0]).toBe('period,amount,count');
		const totalRow = lines.find((l) => l.startsWith('TOTAL,'));
		expect(totalRow, 'CSV must end with a TOTAL row').toBeTruthy();
		expect(Number(totalRow!.split(',')[1])).toBe(total);
	});

	test('orders report shows the by-status breakdown and exports it', async ({ page }) => {
		await page.goto('/admin/reports/orders');
		await expect(page.getByTestId('orders-status-table')).toBeVisible();
		expect(digits(await page.getByTestId('orders-total-count').textContent())).toBeGreaterThan(0);
		await expect(page.locator('[data-testid^="orders-status-row-"]').first()).toBeVisible();

		const res = await page.request.get(`${APP_BASE}/admin/reports/orders/csv`);
		expect(res.status()).toBe(200);
		expect(res.headers()['content-type']).toContain('text/csv');
		const body = await res.text();
		expect(body.startsWith('period,amount,count')).toBeTruthy();
		expect(body).toMatch(/\nTOTAL,\d+,\d+/);
		expect(body, 'by-status block follows the per-day rows').toMatch(/\r?\n\r?\nstatus,amount,count/);
	});

	test('services report shows by-status and by-product and exports them', async ({ page }) => {
		await page.goto('/admin/reports/services');
		expect(digits(await page.getByTestId('services-total').textContent())).toBeGreaterThan(0);
		await expect(page.getByTestId('services-status-row-active')).toBeVisible();
		await expect(page.locator('[data-testid^="services-product-row-"]').first()).toBeVisible();

		const res = await page.request.get(`${APP_BASE}/admin/reports/services/csv`);
		expect(res.status()).toBe(200);
		const body = await res.text();
		expect(body.startsWith('product,status,count')).toBeTruthy();
		expect(body).toContain(',active,');
	});

	test('CSV exports refuse anonymous callers', async () => {
		const anon = await newApi();
		try {
			for (const path of ['/admin/reports/revenue/csv', '/admin/reports/orders/csv', '/admin/reports/services/csv', '/admin/clients/export.csv']) {
				const res = await anon.get(`${APP_BASE}${path}`);
				expect(res.status(), path).toBe(403);
			}
		} finally {
			await anon.dispose();
		}
	});
});
