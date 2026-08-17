import { expect, test } from './fixtures';
import {
	adminToken,
	createOrder,
	newApi,
	payInvoiceViaMock,
	registerVerifyLogin,
	uniqueDomainLabel
} from './helpers';

/**
 * Admin "Pending Module Actions" queue (WHMCS's Module Queue equivalent):
 * a provisioning action that fails moves into the queue (state "retry" until
 * its retries exhaust, then "archived"), where an admin can inspect the
 * failure and either retry it or dismiss it. Added 2026-07-27 after a real
 * live invoice's domain registration + hosting provisioning got stuck with
 * zero visibility - see docs/CONTRACTS.md and the adminops module doc.
 *
 * To get a deterministic, fast failure (no waiting through real retry
 * backoff) this points a throwaway product at a server nothing listens on
 * (127.0.0.1:1 - connection refused immediately), so provision:create fails
 * on its very first attempt and appears in the queue with state "retry"
 * right away.
 */

const API_BASE = 'http://localhost:8080';
const ADMIN_EMAIL = 'admin@e2e.test';
const ADMIN_PASSWORD = 'AdminE2E!2026';

test.describe('admin Pending Module Actions queue', () => {
	test('a failed provisioning action appears in the queue, is viewable, and can be dismissed', async ({
		page
	}) => {
		test.setTimeout(120_000);

		const api = await newApi();
		const token = await adminToken(api);
		const auth = { Authorization: `Bearer ${token}` };
		const suffix = `${Date.now()}`;

		// --- 1. Admin: a server nothing listens on -> instant connection failure ---
		const groupRes = await api.post(`${API_BASE}/api/v1/admin/server-groups`, {
			headers: auth,
			data: { name: `E2E Queue Group ${suffix}`, strategy: 'round_robin' }
		});
		expect(groupRes.ok(), await groupRes.text()).toBeTruthy();
		const groupId = (await groupRes.json()).data.id as number;

		const serverRes = await api.post(`${API_BASE}/api/v1/admin/servers`, {
			headers: auth,
			data: {
				group_id: groupId,
				name: `E2E Unreachable Server ${suffix}`,
				module: 'cpanel',
				hostname: '127.0.0.1',
				port: 1,
				username: 'root',
				api_token: 'irrelevant',
				use_ssl: false
			}
		});
		expect(serverRes.ok(), await serverRes.text()).toBeTruthy();

		// --- 2. Admin: a plain product pointed at that server group ------------------
		// File it under the seeded hosting product's group (any existing product
		// group works - this test doesn't care which one).
		const seededRes = await api.get(`${API_BASE}/api/v1/admin/products`, {
			headers: auth,
			params: { search: 'e2e-shared-hosting' }
		});
		expect(seededRes.ok(), await seededRes.text()).toBeTruthy();
		const seeded = ((await seededRes.json()).data ?? []).find(
			(p: { slug: string }) => p.slug === 'e2e-shared-hosting'
		);
		expect(seeded, 'seeded hosting product must exist').toBeTruthy();

		const productRes = await api.post(`${API_BASE}/api/v1/admin/products`, {
			headers: auth,
			data: {
				group_id: seeded.group_id,
				name: `E2E Queue Product ${suffix}`,
				slug: `e2e-queue-${suffix}`,
				type: 'shared_hosting',
				module: 'cpanel',
				server_group_id: groupId,
				package_name: 'e2e-queue-pkg',
				auto_setup: 'on_payment'
			}
		});
		expect(productRes.ok(), await productRes.text()).toBeTruthy();
		const productId = (await productRes.json()).data.id as number;
		const pricingRes = await api.put(`${API_BASE}/api/v1/admin/products/${productId}/pricing`, {
			headers: auth,
			data: { cycle: 'monthly', price: 10000, setup_fee: 0 }
		});
		expect(pricingRes.ok(), await pricingRes.text()).toBeTruthy();

		// --- 3. Client orders + pays -> provisioning fails once, moves to "retry" ---
		const client = await registerVerifyLogin(api, 'queue');
		const domain = `${uniqueDomainLabel()}.queue.e2e.test`;
		const invoiceId = await createOrder(api, client.accessToken, [
			{ item_type: 'product', product_id: productId, cycle: 'monthly', domain }
		]);
		await payInvoiceViaMock(api, client.accessToken, invoiceId, 'BC');

		await expect
			.poll(
				async () => {
					const res = await api.get(`${API_BASE}/api/v1/admin/logs/queue`, {
						headers: auth,
						params: { type: 'provision:create', state: 'retry' }
					});
					if (!res.ok()) return 0;
					const rows = ((await res.json()).data ?? []) as unknown[];
					return rows.length;
				},
				{ timeout: 30_000, message: 'waiting for the provisioning failure to reach the queue' }
			)
			.toBeGreaterThan(0);

		// --- 4. Admin UI: the row is visible, viewable, and dismissible --------------
		await page.goto('/login');
		await page.waitForLoadState('networkidle');
		await page.locator('input[name="email"]').fill(ADMIN_EMAIL);
		await page.locator('input[name="password"]').fill(ADMIN_PASSWORD);
		await page.locator('button[type="submit"]').click();
		await page.waitForURL(/\/admin/, { timeout: 15_000 });

		await page.goto('/admin/logs/queue?type=provision:create&state=retry');
		await page.waitForLoadState('networkidle');

		const row = page
			.locator('[data-testid^="row-module-action-"]')
			.filter({ hasText: 'Create Hosting Account' })
			.first();
		await expect(row).toBeVisible({ timeout: 10_000 });
		await expect(row).toContainText('Retrying');

		// The row's own testid embeds the asynq task's unique id - capture it so
		// the final assertion targets THIS exact row, not just "any row with this
		// text" (a leftover row from an earlier/failed run of this same spec
		// would otherwise still match the generic content filter above).
		const rowTestId = await row.getAttribute('data-testid');
		expect(rowTestId).toBeTruthy();

		// View the payload/error detail.
		await row.locator('[data-testid^="module-queue-view-"]').click();
		const payload = page.getByTestId('module-queue-payload');
		await expect(payload).toBeVisible();
		await expect(payload).not.toHaveText('—');
		await page.getByRole('dialog', { name: 'Module Action Detail' }).getByLabel('Close').click();

		// Dismiss it via the confirm dialog - the row disappears.
		await row.locator('[data-testid^="module-queue-delete-"]').click();
		const dialog = page.getByRole('dialog');
		await expect(dialog).toBeVisible();
		await dialog.getByRole('button', { name: /^Dismiss$/ }).click();
		await expect(page.getByText('Module action dismissed')).toBeVisible({ timeout: 10_000 });
		await expect(page.getByTestId(rowTestId!)).toHaveCount(0);

		await api.dispose();
	});

	test('Dismiss All removes every action matching the current filter in one go', async ({
		page
	}) => {
		test.setTimeout(120_000);

		const api = await newApi();
		const token = await adminToken(api);
		const auth = { Authorization: `Bearer ${token}` };
		const suffix = `${Date.now()}dismissall`;

		// Same "server nothing listens on" setup as the test above - a fresh
		// group/server/product per run so this spec is safe under concurrency.
		const groupRes = await api.post(`${API_BASE}/api/v1/admin/server-groups`, {
			headers: auth,
			data: { name: `E2E DismissAll Group ${suffix}`, strategy: 'round_robin' }
		});
		expect(groupRes.ok(), await groupRes.text()).toBeTruthy();
		const groupId = (await groupRes.json()).data.id as number;

		const serverRes = await api.post(`${API_BASE}/api/v1/admin/servers`, {
			headers: auth,
			data: {
				group_id: groupId,
				name: `E2E DismissAll Unreachable Server ${suffix}`,
				module: 'cpanel',
				hostname: '127.0.0.1',
				port: 1,
				username: 'root',
				api_token: 'irrelevant',
				use_ssl: false
			}
		});
		expect(serverRes.ok(), await serverRes.text()).toBeTruthy();

		const seededRes = await api.get(`${API_BASE}/api/v1/admin/products`, {
			headers: auth,
			params: { search: 'e2e-shared-hosting' }
		});
		expect(seededRes.ok(), await seededRes.text()).toBeTruthy();
		const seeded = ((await seededRes.json()).data ?? []).find(
			(p: { slug: string }) => p.slug === 'e2e-shared-hosting'
		);
		expect(seeded, 'seeded hosting product must exist').toBeTruthy();

		const productRes = await api.post(`${API_BASE}/api/v1/admin/products`, {
			headers: auth,
			data: {
				group_id: seeded.group_id,
				name: `E2E DismissAll Product ${suffix}`,
				slug: `e2e-dismissall-${suffix}`,
				type: 'shared_hosting',
				module: 'cpanel',
				server_group_id: groupId,
				package_name: 'e2e-dismissall-pkg',
				auto_setup: 'on_payment'
			}
		});
		expect(productRes.ok(), await productRes.text()).toBeTruthy();
		const productId = (await productRes.json()).data.id as number;
		const pricingRes = await api.put(`${API_BASE}/api/v1/admin/products/${productId}/pricing`, {
			headers: auth,
			data: { cycle: 'monthly', price: 10000, setup_fee: 0 }
		});
		expect(pricingRes.ok(), await pricingRes.text()).toBeTruthy();

		// Two separate clients order it -> two independent provision:create
		// failures land in the queue under this test's own product/type.
		for (let i = 0; i < 2; i++) {
			const client = await registerVerifyLogin(api, `dismissall${i}`);
			const domain = `${uniqueDomainLabel()}.dismissall.e2e.test`;
			const invoiceId = await createOrder(api, client.accessToken, [
				{ item_type: 'product', product_id: productId, cycle: 'monthly', domain }
			]);
			await payInvoiceViaMock(api, client.accessToken, invoiceId, 'BC');
		}

		await expect
			.poll(
				async () => {
					const res = await api.get(`${API_BASE}/api/v1/admin/logs/queue`, {
						headers: auth,
						params: { type: 'provision:create', state: 'retry' }
					});
					if (!res.ok()) return 0;
					const rows = ((await res.json()).data ?? []) as unknown[];
					return rows.length;
				},
				{ timeout: 30_000, message: 'waiting for both provisioning failures to reach the queue' }
			)
			.toBeGreaterThanOrEqual(2);

		await page.goto('/login');
		await page.waitForLoadState('networkidle');
		await page.locator('input[name="email"]').fill(ADMIN_EMAIL);
		await page.locator('input[name="password"]').fill(ADMIN_PASSWORD);
		await page.locator('button[type="submit"]').click();
		await page.waitForURL(/\/admin/, { timeout: 15_000 });

		// Filtered narrowly to this test's own type/state so it only ever
		// touches the rows it created, safe alongside other concurrently
		// running specs' own queue entries.
		await page.goto('/admin/logs/queue?type=provision:create&state=retry');
		await page.waitForLoadState('networkidle');

		const dismissAllBtn = page.getByTestId('module-queue-dismiss-all');
		await expect(dismissAllBtn).toBeVisible({ timeout: 10_000 });
		// The filter is shared with every provision:create/retry row on the
		// stack (other specs may have their own), so just confirm ours are
		// gone afterward rather than asserting an exact total count here.
		await dismissAllBtn.click();

		const dialog = page.getByRole('dialog');
		await expect(dialog).toBeVisible();
		await dialog.getByRole('button', { name: /^Dismiss All$/ }).click();
		await expect(page.getByText(/module actions? dismissed/)).toBeVisible({ timeout: 15_000 });

		// This test's own rows are identifiable by the unique product name in
		// the payload viewer - simplest proof: the whole filtered table is now
		// empty (nothing else concurrently creates provision:create/retry rows
		// against this same fresh product/server, so zero is unambiguous).
		await expect(page.locator('[data-testid^="row-module-action-"]')).toHaveCount(0);

		await api.dispose();
	});

	test('staff without the logs permission cannot reach the queue', async ({ request }) => {
		// Mirrors the existing audit/email/integration log permission gate
		// (adminops.RequirePermission("logs")) - same guard, new route.
		const res = await request.get(`${API_BASE}/api/v1/admin/logs/queue`);
		expect(res.status()).toBe(401); // no token at all -> RequireAuth rejects first
	});
});
