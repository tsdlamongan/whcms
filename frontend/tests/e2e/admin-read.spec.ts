import { expect, test, type APIRequestContext } from './fixtures';
import { adminToken, newApi, setSessionCookies } from './helpers';

/**
 * Breadth coverage: every admin list/read page loads for an admin and renders
 * its primary control (no fatal load error). CRUD writes live in
 * admin-write.spec.ts.
 */

// testid: a stable, always-visible element (empty -> assert the page's h1 heading,
// used for list pages whose filter panel is collapsed by default).
const PAGES: Array<{ path: string; testid: string; name: string }> = [
	{ name: 'dashboard', path: '/admin', testid: 'stat-income-today' },
	{ name: 'clients', path: '/admin/clients', testid: 'client-create-link' },
	{ name: 'orders', path: '/admin/orders', testid: '' },
	{ name: 'invoices', path: '/admin/invoices', testid: 'admin-invoice-create-link' },
	{ name: 'transactions', path: '/admin/transactions', testid: 'stat-total-income' },
	{ name: 'domains', path: '/admin/domains', testid: '' },
	{ name: 'services', path: '/admin/services', testid: '' },
	{ name: 'products', path: '/admin/products', testid: 'product-create-link' },
	{ name: 'product-groups', path: '/admin/product-groups', testid: 'group-create-button' },
	{ name: 'coupons', path: '/admin/coupons', testid: 'coupon-create-button' },
	{ name: 'servers', path: '/admin/servers', testid: 'servers-pager' },
	{ name: 'email-templates', path: '/admin/email-templates', testid: 'template-filter-form' },
	{ name: 'announcements', path: '/admin/announcements', testid: 'announcement-filter-form' },
	{ name: 'knowledgebase', path: '/admin/knowledgebase', testid: 'kb-category-create-button' },
	{ name: 'network-status', path: '/admin/network-status', testid: 'network-filter-form' },
	{ name: 'departments', path: '/admin/departments', testid: 'department-create-button' },
	{ name: 'staff', path: '/admin/staff', testid: 'staff-gate-password' },
	{ name: 'gateways', path: '/admin/gateways', testid: 'gateway-save' },
	{ name: 'registrars', path: '/admin/registrars', testid: '' },
	{ name: 'settings', path: '/admin/settings', testid: 'settings-form-general' },
	{ name: 'settings-overview', path: '/admin/settings/overview', testid: 'setup-progress' },
	{ name: 'audit-log', path: '/admin/logs/audit', testid: 'audit-filter-form' },
	{ name: 'email-log', path: '/admin/logs/email', testid: 'email-log-filter-form' },
	{ name: 'integration-log', path: '/admin/logs/integration', testid: 'integration-filter-form' },
	{ name: 'reports-revenue', path: '/admin/reports/revenue', testid: 'revenue-total' },
	{ name: 'reports-orders', path: '/admin/reports/orders', testid: 'report-run' },
	{ name: 'reports-services', path: '/admin/reports/services', testid: '' }
];

test.describe('admin read pages', () => {
	let api: APIRequestContext;
	let token: string;

	test.beforeAll(async () => {
		test.setTimeout(60_000);
		api = await newApi();
		token = await adminToken(api);
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, token);
	});

	for (const p of PAGES) {
		test(`${p.name} page renders`, async ({ page }) => {
			await page.goto(p.path);
			if (p.testid) {
				await expect(page.getByTestId(p.testid)).toBeVisible({ timeout: 15_000 });
			} else {
				// Collapsed-filter list pages: assert the page rendered for admin.
				await expect(page.getByRole('heading', { level: 1 }).first()).toBeVisible({
					timeout: 15_000
				});
			}
		});
	}

	test('admin page title follows the HostPanel nav brand (PUBLIC_APP_NAME), not a hardcoded name', async ({
		page
	}) => {
		await page.goto('/admin');
		const brand = await page.getByTestId('app-name').innerText();
		expect(brand.length).toBeGreaterThan(0);
		const title = await page.title();
		expect(title.endsWith(` — ${brand} Admin`)).toBe(true);
	});
});
