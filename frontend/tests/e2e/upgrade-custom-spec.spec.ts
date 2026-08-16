import { expect, test, type APIRequestContext, type Page } from './fixtures';
import {
	API_BASE,
	MOCK_BASE,
	adminToken,
	authHeaders,
	createOrder,
	newApi,
	payInvoiceViaMock,
	pollListStatus,
	registerVerifyLogin,
	setSessionCookies,
	unique,
	uniqueDomainLabel,
	type Client
} from './helpers';

/**
 * Custom-spec (configurable) service upgrade through the client area:
 *
 * an admin defines a configurable cPanel product (disk knob, per-GB pricing);
 * a client orders it at 20 GB and pays (API-driven setup - the order/configure
 * UI itself is covered by configure-custom-product.spec.ts); then - the flow
 * this spec owns - the client opens the service page, resizes the disk to
 * 50 GB in the upgrade modal, sees the live estimate, submits, lands on the
 * prorated diff invoice, pays it via the mock Duitku gateway, and the worker
 * rebuilds the panel package: the service's recurring amount and chosen specs
 * update, a new dynamic package (quota 51200 MB) exists on the mock WHM, and
 * the old dynamic package (no longer referenced) is deleted.
 *
 * Runs against the live stack (mockserver:9090, api:8080, worker, frontend).
 */

test.describe.configure({ mode: 'serial', timeout: 120_000 });

test.describe('custom-spec upgrade: resize via client area → pay → package rebuilt', () => {
	const suffix = unique('upspec');
	const slug = `e2e-${suffix}`;
	const hostingDomain = `${uniqueDomainLabel()}.upspec.e2e.test`;

	let api: APIRequestContext;
	let client: Client;
	let productId = 0;
	let serviceId = 0;
	let originalPackage = '';

	/** Open a modal via a hydration-sensitive plain button (see client-manage). */
	async function clickUntilVisible(page: Page, buttonTestId: string, resultTestId: string) {
		await expect(async () => {
			await page.getByTestId(buttonTestId).click();
			await expect(page.getByTestId(resultTestId)).toBeVisible({ timeout: 2_000 });
		}).toPass({ timeout: 30_000 });
	}

	test.beforeAll(async () => {
		test.setTimeout(180_000);
		api = await newApi();

		// --- Admin: configurable cPanel product (base 50k/mo + disk knob) -----
		const admin = await adminToken(api);
		const adminAuth = authHeaders(admin);

		const seededRes = await api.get(`${API_BASE}/api/v1/admin/products`, {
			headers: adminAuth,
			params: { search: 'e2e-shared-hosting' }
		});
		expect(seededRes.ok()).toBeTruthy();
		const seeded = (((await seededRes.json()).data ?? []) as Array<{ slug: string; group_id: number }>).find(
			(p) => p.slug === 'e2e-shared-hosting'
		);
		expect(seeded, 'seeded hosting product must exist').toBeTruthy();

		// Provision against the seeded cPanel MOCK server so addpkg/changepackage
		// hit the deterministic mock.
		const serversRes = await api.get(`${API_BASE}/api/v1/admin/servers`, { headers: adminAuth });
		expect(serversRes.ok()).toBeTruthy();
		const mockServer = (((await serversRes.json()).data ?? []) as Array<{
			module: string;
			hostname: string;
			group_id: number;
		}>).find((s) => s.module === 'cpanel' && s.hostname === 'localhost');
		expect(mockServer, 'seeded cPanel mock server must exist').toBeTruthy();

		const createRes = await api.post(`${API_BASE}/api/v1/admin/products`, {
			headers: adminAuth,
			data: {
				group_id: seeded!.group_id,
				name: `Custom Upgrade ${suffix}`,
				slug,
				type: 'shared_hosting',
				module: 'cpanel',
				server_group_id: mockServer!.group_id,
				package_name: 'e2e-base',
				auto_setup: 'on_payment',
				configurable: true
			}
		});
		expect(createRes.ok(), `create configurable product: ${await createRes.text()}`).toBeTruthy();
		productId = (await createRes.json()).data.id as number;

		expect(
			(
				await api.put(`${API_BASE}/api/v1/admin/products/${productId}/pricing`, {
					headers: adminAuth,
					data: { cycle: 'monthly', price: 50_000, setup_fee: 0 }
				})
			).ok()
		).toBeTruthy();

		const specRes = await api.post(`${API_BASE}/api/v1/admin/products/${productId}/specs`, {
			headers: adminAuth,
			data: {
				key: 'disk',
				label: 'Disk space',
				provision_key: 'disk',
				unit: 'gb',
				included_qty: 5,
				min_qty: 5,
				max_qty: 100,
				step_qty: 5,
				default_qty: 10
			}
		});
		expect(specRes.ok(), `create disk spec: ${await specRes.text()}`).toBeTruthy();
		const specId = (await specRes.json()).data.id as number;
		expect(
			(
				await api.put(`${API_BASE}/api/v1/admin/product-specs/${specId}/pricing`, {
					headers: adminAuth,
					data: { cycle: 'monthly', unit_price: 1_000, unlimited_price: 0 }
				})
			).ok()
		).toBeTruthy();

		// --- Client: order at 20 GB (50k + 15×1k = 65k/mo) and pay ------------
		client = await registerVerifyLogin(api, 'upspec');
		const invoiceId = await createOrder(api, client.accessToken, [
			{
				item_type: 'product',
				product_id: productId,
				cycle: 'monthly',
				domain: hostingDomain,
				specs: [{ key: 'disk', qty: 20 }]
			}
		]);
		await payInvoiceViaMock(api, client.accessToken, invoiceId);

		const svc = await pollListStatus(
			api,
			client.accessToken,
			'/api/v1/services',
			(r) => r.domain === hostingDomain,
			'active'
		);
		serviceId = svc.id as number;
		expect(svc.recurring_amount, 'order must price base + chargeable disk').toBe(65_000);
		originalPackage = ((svc.panel_meta as { package_name?: string }) ?? {}).package_name ?? '';
		expect(originalPackage, 'configurable service must record its dynamic package').not.toBe('');
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, client.accessToken, client.refreshToken);
	});

	test('resize disk 20→50 GB, pay the prorated invoice, and the panel package is rebuilt', async ({
		page
	}) => {
		// --- Upgrade modal: pick the same product, resize the disk knob -------
		await page.goto(`/services/${serviceId}`);
		await clickUntilVisible(page, 'service-upgrade-button', 'service-upgrade-form');

		await page.locator('select[name="product_id"]').selectOption(String(productId));
		await page.locator('select[name="cycle"]').selectOption('monthly');

		// Resizing the service's own product prefills the knob from its current
		// chosen specs (20 GB), not the product default (10 GB).
		const disk = page.getByTestId('spec-disk-input');
		await expect(disk).toHaveValue('20');
		await disk.fill('50');
		await expect(disk).toHaveValue('50');

		// Live estimate: 50k base + (50-5)×1k = 95k/mo.
		await expect(page.getByTestId('service-upgrade-price')).toContainText('95.000');

		await page.getByTestId('service-upgrade-submit').click();
		await page.waitForURL(/\/billing\/invoices\/\d+/, { timeout: 20_000 });
		const upgradeInvoiceId = Number(page.url().match(/invoices\/(\d+)/)?.[1]);
		expect(upgradeInvoiceId).toBeGreaterThan(0);

		// The prorated diff invoice line itemizes the new spec configuration.
		const invRes = await api.get(`${API_BASE}/api/v1/invoices/${upgradeInvoiceId}`, {
			headers: authHeaders(client.accessToken)
		});
		expect(invRes.ok()).toBeTruthy();
		expect(JSON.stringify(await invRes.json())).toContain('50GB disk');

		// Until it is paid, the service shows the pending upgrade + invoice link.
		await page.goto(`/services/${serviceId}`);
		await expect(page.getByTestId('service-upgrade-invoice-link')).toBeVisible();

		// --- Pay via the mock gateway; ApplyUpgrade rebinds the service -------
		await payInvoiceViaMock(api, client.accessToken, upgradeInvoiceId);

		let panelMeta: { package_name?: string } = {};
		let pendingUpgrade: unknown = null;
		await expect
			.poll(
				async () => {
					const res = await api.get(`${API_BASE}/api/v1/services/${serviceId}`, {
						headers: authHeaders(client.accessToken)
					});
					if (!res.ok()) return null;
					const svc = (await res.json()).data as {
						recurring_amount: number;
						pending_upgrade: unknown;
						panel_meta?: { package_name?: string };
					};
					panelMeta = svc.panel_meta ?? {};
					pendingUpgrade = svc.pending_upgrade;
					return svc.recurring_amount;
				},
				{ timeout: 30_000, message: 'waiting for the paid upgrade to be applied' }
			)
			.toBe(95_000);
		expect(pendingUpgrade, 'pending_upgrade must clear once applied').toBeFalsy();

		// --- Worker rebuilds the panel package from the new chosen specs ------
		await expect
			.poll(
				async () => {
					const res = await api.get(`${API_BASE}/api/v1/services/${serviceId}`, {
						headers: authHeaders(client.accessToken)
					});
					if (!res.ok()) return null;
					panelMeta =
						((await res.json()).data as { panel_meta?: { package_name?: string } }).panel_meta ?? {};
					return panelMeta.package_name ?? '';
				},
				{ timeout: 60_000, message: 'waiting for the change-package job to run' }
			)
			.not.toBe(originalPackage);
		const newPackage = panelMeta.package_name ?? '';
		expect(newPackage).not.toBe('');

		// New dynamic package exists on the mock WHM with the resized quota;
		// the old one (no sibling references it) was deleted.
		await expect
			.poll(
				async () => {
					const res = await api.get(`${MOCK_BASE}/mock/whm/packages`);
					if (!res.ok()) return null;
					const pkgs = ((await res.json()).packages ?? []) as Array<{
						name: string;
						params: Record<string, string>;
					}>;
					return pkgs.find((p) => p.name === newPackage)?.params?.quota ?? null;
				},
				{ timeout: 20_000, message: 'waiting for the resized dynamic package' }
			)
			.toBe(String(50 * 1024)); // 50 GB → 51200 MB

		const pkgsRes = await api.get(`${MOCK_BASE}/mock/whm/packages`);
		const names = (((await pkgsRes.json()).packages ?? []) as Array<{ name: string }>).map(
			(p) => p.name
		);
		expect(names, 'orphaned old dynamic package must be deleted').not.toContain(originalPackage);

		// The client sees the new configuration reflected on the service page.
		await page.goto(`/services/${serviceId}`);
		await expect(page.getByTestId('service-status')).toContainText(/active/i);
	});
});
