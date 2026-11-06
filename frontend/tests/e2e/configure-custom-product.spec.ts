import { expect, test } from './fixtures';
import { randomUUID } from 'node:crypto';
import { loginApi } from './helpers';

/**
 * Dynamic / custom-spec products: an admin creates a configurable cPanel
 * product with a disk spec + per-unit pricing; a fresh client configures the
 * disk slider, sees the live estimate, orders, pays via the Duitku mock, and
 * the worker provisions the service - which for a configurable product first
 * creates a control-panel package (named deterministically from the resolved
 * limits, shared by any other service that resolves to the same limits) on
 * the mock WHM, then the account under it.
 *
 * Runs against the live stack (mockserver:9090, api:8080, worker, frontend).
 */

const MOCK_BASE = 'http://localhost:9090';
const API_BASE = 'http://localhost:8080';

const ADMIN_EMAIL = 'admin@e2e.test';
const ADMIN_PASSWORD = 'AdminE2E!2026';

test.describe('custom-spec product → configure → pay → package provisioned', () => {
	test('admin defines a configurable product; client orders it and a package is created', async ({
		page,
		request
	}) => {
		test.setTimeout(240_000);

		const suffix = `${Date.now()}${Math.floor(Math.random() * 10_000)}`;
		const slug = `e2e-custom-${suffix}`;
		const domain = `${randomUUID().replace(/-/g, '').slice(0, 10)}.test`;
		const email = `e2e-custom-${suffix}@example.test`;
		const password = 'Sup3rSecret!23';

		// --- 1. Admin: create a configurable cPanel product ---------------------------
		const adminToken = (await loginApi(request, ADMIN_EMAIL, ADMIN_PASSWORD)).accessToken;
		const adminAuth = { Authorization: `Bearer ${adminToken}` };

		// A product group to file the product under (reuse the seeded hosting one).
		const seededRes = await request.get(`${API_BASE}/api/v1/admin/products`, {
			headers: adminAuth,
			params: { search: 'e2e-shared-hosting' }
		});
		expect(seededRes.ok()).toBeTruthy();
		const seeded = ((await seededRes.json()).data ?? []).find(
			(p: { slug: string }) => p.slug === 'e2e-shared-hosting'
		);
		expect(seeded, 'seeded hosting product must exist').toBeTruthy();

		// Provision against the seeded cPanel MOCK server (localhost:9090) - its
		// server group, so createacct/addpkg hit the deterministic mock rather than
		// any real WHM another product might point to.
		const serversRes = await request.get(`${API_BASE}/api/v1/admin/servers`, { headers: adminAuth });
		expect(serversRes.ok()).toBeTruthy();
		const mockServer = ((await serversRes.json()).data ?? []).find(
			(s: { module: string; hostname: string }) => s.module === 'cpanel' && s.hostname === 'localhost'
		);
		expect(mockServer, 'seeded cPanel mock server must exist').toBeTruthy();
		const serverGroupId = mockServer.group_id as number;

		const createRes = await request.post(`${API_BASE}/api/v1/admin/products`, {
			headers: adminAuth,
			data: {
				group_id: seeded.group_id,
				name: `Custom Hosting ${suffix}`,
				slug,
				type: 'shared_hosting',
				module: 'cpanel',
				server_group_id: serverGroupId,
				package_name: 'e2e-base',
				auto_setup: 'on_payment',
				configurable: true,
				shell_access: true,
				cgi_access: true,
				feature_list: 'e2e_features'
			}
		});
		expect(createRes.ok(), 'create configurable product').toBeTruthy();
		const productId = (await createRes.json()).data.id as number;

		// Monthly base price.
		expect(
			(
				await request.put(`${API_BASE}/api/v1/admin/products/${productId}/pricing`, {
					headers: adminAuth,
					data: { cycle: 'monthly', price: 50000, setup_fee: 0 }
				})
			).ok()
		).toBeTruthy();

		// Disk spec: GB, 5 included, 5..100 step 5, default 10.
		const specRes = await request.post(`${API_BASE}/api/v1/admin/products/${productId}/specs`, {
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
		expect(specRes.ok(), 'create disk spec').toBeTruthy();
		const specId = (await specRes.json()).data.id as number;

		// Per-GB price for monthly.
		expect(
			(
				await request.put(`${API_BASE}/api/v1/admin/product-specs/${specId}/pricing`, {
					headers: adminAuth,
					data: { cycle: 'monthly', unit_price: 1000, unlimited_price: 0 }
				})
			).ok()
		).toBeTruthy();

		// --- 2. Register + verify a fresh client --------------------------------------
		await page.goto('/register');
		await page.waitForLoadState('networkidle');
		await page.locator('#field-email').fill(email);
		await page.locator('#field-password').fill(password);
		await page.locator('#field-confirm_password').fill(password);
		await page.locator('#field-first_name').fill('E2E');
		await page.locator('#field-last_name').fill('Custom');
		await page.locator('#field-address1').fill('Jl. Testing No. 1');
		await page.locator('#field-city').fill('Jakarta');
		await page.locator('#field-state').fill('DKI Jakarta');
		await page.locator('#field-postcode').fill('12345');
		await page.locator('#field-country').selectOption('ID');
		await page.locator('#field-phone').fill('+6281234567890');
		await page.getByTestId('register-submit').click();
		await expect(page.getByTestId('register-success')).toBeVisible();

		let verifyLink = '';
		await expect
			.poll(
				async () => {
					const res = await request.get(`${MOCK_BASE}/mail/messages`, { params: { to: email } });
					if (!res.ok()) return '';
					const messages = (await res.json()) as Array<{ to: string; html: string; text: string }>;
					const msg = messages.find((m) => m.to.toLowerCase() === email.toLowerCase());
					if (!msg) return '';
					const src = msg.html || msg.text || '';
					const match =
						src.match(/href="([^"]*verify-email\?token=[^"]*)"/) ??
						src.match(/(https?:\/\/\S*verify-email\?token=\S+)/);
					verifyLink = match ? match[1].replace(/&amp;/g, '&') : '';
					return verifyLink;
				},
				{ timeout: 20_000, message: 'waiting for the verification email' }
			)
			.not.toBe('');
		await page.goto(verifyLink);
		await expect(page).toHaveURL(/\/verify-email\?verified=1/);

		// --- 3. Log in (UI) -----------------------------------------------------------
		let loggedIn = false;
		for (let attempt = 1; attempt <= 8 && !loggedIn; attempt++) {
			await page.goto('/login');
			await page.waitForLoadState('networkidle');
			await page.locator('input[name="email"]').fill(email);
			await page.locator('input[name="password"]').fill(password);
			await page.locator('button[type="submit"]').click();
			try {
				await expect(page).toHaveURL(/\/dashboard/, { timeout: 20_000 });
				loggedIn = true;
			} catch (err) {
				if (attempt === 8) throw err;
			}
		}

		// --- 4. Configure the product: set disk to 50 GB ------------------------------
		await page.goto(`/order/product/${slug}`);
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('product-title')).toBeVisible();
		await page.getByTestId('product-cycle-monthly').check();

		// Move the disk slider to 50 GB.
		const disk = page.getByTestId('spec-disk-input');
		await disk.fill('50');
		await expect(disk).toHaveValue('50');

		await page.getByTestId('domain-mode-own').check();
		await page.getByTestId('domain-search-input').fill(domain);

		// Re-assert the slider + estimate right before checkout - this is the state
		// that must reach the cart. Live estimate = 50000 + (50-5)*1000 = 95000.
		await expect(disk).toHaveValue('50');
		await expect(page.getByTestId('spec-estimate-total')).toContainText('95');
		await page.getByTestId('add-to-cart').click();

		// --- 5. Checkout + pay --------------------------------------------------------
		await expect(page).toHaveURL(/\/order\/cart/);
		await expect(page.getByTestId('checkout-submit')).toBeEnabled();
		await page.getByTestId('checkout-submit').click();

		await expect(page).toHaveURL(/\/billing\/invoices\/\d+/);
		const invoiceId = Number(page.url().match(/invoices\/(\d+)/)?.[1]);
		expect(invoiceId).toBeGreaterThan(0);

		// "VC" (credit card) has no VA/QRIS raw payload to render inline, so
		// it's the channel guaranteed to redirect to the mock's hosted payment
		// page - see payments-instructions.spec.ts for VA/QRIS inline-render
		// coverage this flow deliberately doesn't exercise.
		await expect(page.getByTestId('payment-methods')).toBeVisible();
		// Credit Card sits behind the "show more" toggle (only major VA
		// banks + QRIS are shown expanded by default).
		await page.getByTestId('payment-methods-toggle').click();
		await page.getByTestId('payment-method-VC').click();
		await page.getByTestId('pay-button').click();
		await page.waitForURL(/^http:\/\/localhost:9090\/payment\//, { timeout: 15_000 });
		await page.click('#pay-now');
		await page.waitForURL(new RegExp(`/billing/invoices/${invoiceId}`), { timeout: 20_000 });

		// --- 6. Confirm invoice paid + service active (includes disk price) -----------
		const token = (await loginApi(request, email, password)).accessToken;
		const authHeader = { Authorization: `Bearer ${token}` };

		await expect
			.poll(
				async () => {
					const res = await request.get(`${API_BASE}/api/v1/invoices/${invoiceId}`, {
						headers: authHeader
					});
					if (!res.ok()) return null;
					const body = await res.json();
					return (body.data?.invoice ?? body.data)?.status ?? null;
				},
				{ timeout: 20_000, message: 'waiting for the invoice to be paid' }
			)
			.toBe('paid');

		let serviceId: number | null = null;
		let packageName: string | null = null;
		await expect
			.poll(
				async () => {
					const res = await request.get(`${API_BASE}/api/v1/services`, {
						headers: authHeader,
						params: { search: domain }
					});
					if (!res.ok()) return null;
					const services = ((await res.json()).data ?? []) as Array<{
						id: number;
						domain: string;
						status: string;
						panel_meta?: { package_name?: string };
					}>;
					const svc = services.find((s) => s.domain === domain);
					serviceId = svc?.id ?? null;
					packageName = svc?.panel_meta?.package_name ?? null;
					return svc?.status ?? null;
				},
				{ timeout: 60_000, message: 'waiting for the worker to provision the service' }
			)
			.toBe('active');
		expect(serviceId).not.toBeNull();
		expect(packageName).not.toBeNull();

		// --- 7. Assert the dynamic package was created on the mock WHM -----------------
		// The package name is a deterministic hash of the resolved limits (not the
		// service ID), so read the actual name the service recorded rather than
		// assuming a naming scheme here.
		let dynamicPackage: { name: string; params: Record<string, string> } | undefined;
		await expect
			.poll(
				async () => {
					const res = await request.get(`${MOCK_BASE}/mock/whm/packages`);
					if (!res.ok()) return null;
					const pkgs = ((await res.json()).packages ?? []) as Array<{
						name: string;
						params: Record<string, string>;
					}>;
					dynamicPackage = pkgs.find((p) => p.name === packageName);
					return dynamicPackage?.params?.quota ?? null;
				},
				{ timeout: 20_000, message: 'waiting for the dynamic package to be created' }
			)
			.toBe(String(50 * 1024)); // 50 GB → 51200 MB

		// Shell access, CGI access, and the WHM Feature List set on the product
		// must reach the panel package unchanged.
		expect(dynamicPackage?.params?.hasshell).toBe('1');
		expect(dynamicPackage?.params?.cgi).toBe('1');
		expect(dynamicPackage?.params?.featurelist).toBe('e2e_features');
	});
});
