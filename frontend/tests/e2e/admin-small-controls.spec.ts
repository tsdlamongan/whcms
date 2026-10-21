import { expect, test, type APIRequestContext } from './fixtures';
import {
	API_BASE,
	adminFindClientId,
	adminToken,
	authHeaders,
	newApi,
	registerVerifyLogin,
	seededHostingProductId,
	setSessionCookies,
	unique
} from './helpers';

/**
 * Smaller admin fixes: controls that did nothing (department edit used the
 * wrong HTTP verb, Duplicate Product only toasted, dummy email-template
 * buttons), setup progress that always read "complete", a static System
 * Health panel, and settings tabs that could not be deep-linked.
 */

test.describe('admin small controls', () => {
	let api: APIRequestContext;
	let token: string;

	const get = async (path: string, params: Record<string, string | number> = {}) => {
		const res = await api.get(`${API_BASE}/api/v1${path}`, { headers: authHeaders(token), params });
		expect(res.ok(), `${path}: ${await res.text()}`).toBeTruthy();
		return (await res.json()) as { data: any; meta: any };
	};

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

	test('departments: editing a department persists (PATCH)', async ({ page }) => {
		const name = unique('dept');
		const created = await api.post(`${API_BASE}/api/v1/admin/ticket-departments`, {
			headers: authHeaders(token),
			data: { name, email: 'dept-e2e@example.test', sort: 5, active: true }
		});
		expect(created.ok(), await created.text()).toBeTruthy();
		const id = ((await created.json()).data as { id: number }).id;

		await page.goto('/admin/departments');
		await page.getByTestId(`department-edit-${id}`).click();
		const form = page.getByTestId('department-form');
		await expect(form).toBeVisible();
		const renamed = `${name}-renamed`;
		await form.locator('input[name="name"]').fill(renamed);
		await page.getByTestId('department-save-button').click();

		await expect
			.poll(async () => ((await get('/admin/ticket-departments', { per_page: 1000 })).data as Array<{ id: number; name: string }>).find((d) => d.id === id)?.name, {
				timeout: 15_000,
				message: 'department renamed'
			})
			.toBe(renamed);
	});

	test('products: Duplicate creates a hidden copy and opens it', async ({ page }) => {
		const productId = await seededHostingProductId(api);
		const original = (await get(`/admin/products/${productId}`)).data as { product?: { name: string }; name?: string };
		const originalName = original.product?.name ?? original.name!;

		await page.goto('/admin/products?per_page=1000');
		await page.getByTestId(`product-duplicate-${productId}`).click();
		await page.waitForURL(/\/admin\/products\/(\d+)\?duplicated=1/, { timeout: 20_000 });
		const copyId = Number(/\/admin\/products\/(\d+)/.exec(page.url())![1]);
		expect(copyId).not.toBe(productId);

		const copy = (await get(`/admin/products/${copyId}`)).data as { product?: { name: string; hidden?: boolean }; name?: string; hidden?: boolean };
		const copyProduct = copy.product ?? (copy as { name: string; hidden?: boolean });
		expect(copyProduct.name).toContain(originalName);
		expect(copyProduct.hidden, 'a duplicate must not be orderable until reviewed').toBe(true);
	});

	test('email templates: the no-op buttons are gone and the search filter works', async ({ page }) => {
		await page.goto('/admin/email-templates');
		await expect(page.getByRole('button', { name: /Create New Email Template/ })).toHaveCount(0);
		await expect(page.getByRole('button', { name: /Manage Languages/ })).toHaveCount(0);

		const all = await page.locator('[data-testid^="row-template-"]').count();
		expect(all).toBeGreaterThan(1);
		await page.getByTestId('template-search-input').fill('verify_email');
		await page.getByTestId('template-filter-submit').click();
		await expect(page).toHaveURL(/search=verify_email/);
		const filtered = await page.locator('[data-testid^="row-template-"]').count();
		expect(filtered).toBeGreaterThan(0);
		expect(filtered).toBeLessThan(all);
		await expect(page.locator('[data-testid^="row-template-verify_email-"]').first()).toBeVisible();
	});

	test('settings: tabs deep-link through ?tab= and keep it in the URL', async ({ page }) => {
		await page.goto('/admin/settings?tab=mail');
		await expect(page.getByTestId('settings-form-mail')).toBeVisible();
		// Inactive tab panels stay in the DOM but are hidden.
		await expect(page.getByTestId('settings-form-general')).toBeHidden();

		await page.getByTestId('settings-tab-billing').click();
		await expect(page.getByTestId('settings-form-billing')).toBeVisible();
		await expect(page).toHaveURL(/tab=billing/);

		await page.reload();
		await expect(page.getByTestId('settings-form-billing')).toBeVisible();
	});

	test('settings overview: setup tasks reflect real state and link to the right tab', async ({ page }) => {
		await page.goto('/admin/settings/overview');
		await expect(page.getByTestId('setup-progress')).toBeVisible();
		for (const key of ['company', 'mail', 'gateway', 'products', 'servers']) {
			const task = page.getByTestId(`setup-task-${key}`);
			await expect(task, key).toBeVisible();
			expect(['true', 'false', 'null'], key).toContain(await task.getAttribute('data-done'));
		}
		// The e2e seed ships products and servers, so those two are genuinely done.
		await expect(page.getByTestId('setup-task-products')).toHaveAttribute('data-done', 'true');
		await expect(page.getByTestId('setup-task-servers')).toHaveAttribute('data-done', 'true');

		await page.getByTestId('setup-task-mail').getByRole('link').click();
		await expect(page).toHaveURL(/\/admin\/settings\?tab=mail/);
		await expect(page.getByTestId('settings-form-mail')).toBeVisible();
	});

	test('dashboard: System Health rating follows the real signals', async ({ page }) => {
		const dash = (await get('/admin/dashboard')).data as { stats?: { invoices_overdue?: number }; module_actions_pending?: number };
		const pending = dash.module_actions_pending ?? 0;
		const overdue = dash.stats?.invoices_overdue ?? 0;
		const expected = pending > 0 ? 'Needs Attention' : overdue > 0 ? 'Fair' : 'Good';

		await page.goto('/admin');
		await expect(page.getByTestId('system-health-rating')).toHaveText(expected);
		const issues = page.getByTestId('system-health-issue');
		if (expected === 'Good') {
			await expect(issues).toHaveCount(0);
		} else {
			expect(await issues.count()).toBe((pending > 0 ? 1 : 0) + (overdue > 0 ? 1 : 0));
		}
	});

	test('client detail: last login is shown for a client who has signed in', async ({ page }) => {
		const client = await registerVerifyLogin(api, 'lastlogin');
		const clientId = await adminFindClientId(api, token, client.email);

		await page.goto(`/admin/clients/${clientId}`);
		const lastLogin = page.getByTestId('client-last-login');
		await expect(lastLogin).toBeVisible();
		await expect(lastLogin).not.toContainText('Never');

		// The list no longer carries a "Send Message" control that did nothing.
		await page.goto('/admin/clients');
		await expect(page.getByRole('button', { name: /send message/i })).toHaveCount(0);
	});
});
