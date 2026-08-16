import { expect, test, type APIRequestContext, type Page } from './fixtures';
import {
	adminToken,
	API_BASE,
	authHeaders,
	createOrder,
	newApi,
	payInvoiceViaMock,
	pollListStatus,
	registerLogin,
	registerVerifyLogin,
	seededHostingProductId,
	setSessionCookies,
	unique,
	uniqueDomainLabel,
	type Client
} from './helpers';

/** Put the seeded hosting product in the cart via the real order UI. */
async function addHostingToCart(page: Page): Promise<void> {
	await page.goto('/order/product/e2e-shared-hosting');
	await page.waitForLoadState('networkidle');
	await page.getByTestId('product-cycle-monthly').check();
	await page.getByTestId('domain-mode-own').check();
	await page.getByTestId('domain-search-input').fill(`${uniqueDomainLabel()}.owned.e2e.test`);
	await page.getByTestId('add-to-cart').click();
	await expect(page).toHaveURL(/\/order\/cart/);
	await expect(page.getByTestId('cart-items')).toBeVisible();
}

/**
 * Cart coupon apply/remove and domain management (EPP reveal, auto-renew toggle,
 * contact + DNS tabs). A verified client with a registered domain and a fresh
 * percentage coupon are provisioned via the API.
 */

test.describe('cart + domain management', () => {
	let api: APIRequestContext;
	let client: Client;
	let domainId: number;
	let domainName: string;
	let couponCode: string;

	test.beforeAll(async () => {
		test.setTimeout(180_000);
		api = await newApi();
		client = await registerVerifyLogin(api, 'cartdom');

		// A registered, active domain to manage.
		domainName = `${uniqueDomainLabel()}.e2e.test`;
		const inv = await createOrder(api, client.accessToken, [{ item_type: 'domain_register', domain: domainName, domain_years: 1 }]);
		await payInvoiceViaMock(api, client.accessToken, inv);
		const dom = await pollListStatus(api, client.accessToken, '/api/v1/domains', (r) => r.name === domainName, 'active');
		domainId = dom.id as number;

		// A percentage coupon (applies to everything) for the cart flow.
		couponCode = unique('SAVE').toUpperCase().replace(/[^A-Z0-9]/g, '');
		const token = await adminToken(api);
		const res = await api.post(`${API_BASE}/api/v1/admin/coupons`, {
			headers: authHeaders(token),
			data: { code: couponCode, type: 'percentage', value: 10, active: true }
		});
		expect(res.ok(), `create coupon: ${await res.text()}`).toBeTruthy();

		// DNS management is gated behind the DNS Management add-on for newly
		// registered domains - activate the catalog entry, then enable it on
		// this test domain so the existing "DNS tab loads" assertion holds.
		const addonsRes = await api.get(`${API_BASE}/api/v1/admin/domain-addons`, { headers: authHeaders(token) });
		const addons = (await addonsRes.json()).data as { id: number; key: string; active: boolean }[];
		const dnsAddon = addons.find((a) => a.key === 'dns_management');
		expect(dnsAddon, 'dns_management addon exists in the catalog').toBeTruthy();
		if (!dnsAddon!.active) {
			const putRes = await api.put(`${API_BASE}/api/v1/admin/domain-addons/${dnsAddon!.id}`, {
				headers: authHeaders(token),
				data: { price: 0, active: true }
			});
			expect(putRes.ok(), `activate dns_management addon: ${await putRes.text()}`).toBeTruthy();
		}
		const enableRes = await api.post(`${API_BASE}/api/v1/domains/${domainId}/addons`, {
			headers: authHeaders(client.accessToken),
			data: { addons: ['dns_management'] }
		});
		expect(enableRes.ok(), `enable dns_management on domain: ${await enableRes.text()}`).toBeTruthy();
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, client.accessToken, client.refreshToken);
	});

	test('apply and remove a coupon in the cart', async ({ page }) => {
		// Put the seeded hosting product in the cart via the real order UI.
		await page.goto('/order/product/e2e-shared-hosting');
		await page.waitForLoadState('networkidle');
		await page.getByTestId('product-cycle-monthly').check();
		await page.getByTestId('domain-mode-own').check();
		await page.getByTestId('domain-search-input').fill(`${uniqueDomainLabel()}.owned.e2e.test`);
		await page.getByTestId('add-to-cart').click();

		await expect(page).toHaveURL(/\/order\/cart/);
		await expect(page.getByTestId('cart-items')).toBeVisible();

		// Apply the coupon -> discount appears.
		await page.getByTestId('coupon-input').fill(couponCode);
		await page.getByTestId('coupon-apply').click();
		await expect(page.getByTestId('coupon-applied')).toBeVisible({ timeout: 10_000 });
		await expect(page.getByTestId('cart-discount')).toBeVisible();

		// Remove it -> discount goes away.
		await page.getByTestId('coupon-remove').click();
		await expect(page.getByTestId('coupon-applied')).toHaveCount(0, { timeout: 10_000 });
	});

	test('reveal the domain EPP code', async ({ page }) => {
		await page.goto(`/domains/${domainId}?tab=epp`);
		await page.waitForLoadState('networkidle');
		await page.getByTestId('epp-reveal-button').locator('button').click();
		// epp-code is an <input> populated with the revealed code from the registrar.
		await expect(page.getByTestId('epp-code')).toBeVisible({ timeout: 10_000 });
		await expect(page.getByTestId('epp-code')).toHaveValue(/EPP/, { timeout: 10_000 });
	});

	test('toggle domain auto-renew', async ({ page }) => {
		await page.goto(`/domains/${domainId}`);
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('domain-name')).toContainText(domainName);
		const toggle = page.getByTestId('domain-autorenew-toggle');
		await expect(toggle).toBeVisible();
		// Flip it and confirm the control reflects the new state via the API.
		const before = await api.get(`${API_BASE}/api/v1/domains/${domainId}`, { headers: authHeaders(client.accessToken) });
		const wasAuto = ((await before.json()).data as { auto_renew?: boolean }).auto_renew ?? false;
		await toggle.click();
		await expect
			.poll(async () => {
				const res = await api.get(`${API_BASE}/api/v1/domains/${domainId}`, { headers: authHeaders(client.accessToken) });
				return ((await res.json()).data as { auto_renew?: boolean }).auto_renew ?? false;
			}, { timeout: 10_000 })
			.toBe(!wasAuto);
	});

	test('domain contact and DNS tabs load', async ({ page }) => {
		await page.goto(`/domains/${domainId}?tab=contact`);
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('domain-tabs')).toBeVisible();
		await expect(page.getByTestId('contact-load-error')).toHaveCount(0);

		await page.goto(`/domains/${domainId}?tab=dns`);
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('domain-tabs')).toBeVisible();
		await expect(page.getByTestId('dns-load-error')).toHaveCount(0);
		await expect(page.getByTestId('dns-addon-required')).toHaveCount(0);
	});

	test('DNS tab is gated behind the DNS Management add-on until purchased', async ({ page }) => {
		test.setTimeout(60_000);
		const unmanagedDomain = `${uniqueDomainLabel()}.e2e.test`;
		const inv = await createOrder(api, client.accessToken, [
			{ item_type: 'domain_register', domain: unmanagedDomain, domain_years: 1 }
		]);
		await payInvoiceViaMock(api, client.accessToken, inv);
		const dom = await pollListStatus(
			api,
			client.accessToken,
			'/api/v1/domains',
			(r) => r.name === unmanagedDomain,
			'active'
		);
		expect(dom.dns_management_enabled).toBe(false);

		await page.goto(`/domains/${dom.id}?tab=dns`);
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('dns-addon-required')).toBeVisible();
		await expect(page.getByTestId('dns-load-error')).toHaveCount(0);

		// Buying the add-on from the client's own Add-ons tab lifts the gate.
		await page.getByTestId('dns-addon-required').getByRole('link').click();
		await expect(page).toHaveURL(/tab=addons/);
		await page.getByTestId('addon-checkbox-dns_management').check();
		await page.getByTestId('addons-save').locator('button').click();
		await expect(page.getByTestId('addon-checkbox-dns_management')).toBeChecked({ timeout: 10_000 });

		// Retried as a whole: under a busy full-suite run this fresh GET can
		// occasionally lag a beat behind the addon save that just completed, so
		// a single static read can catch it mid-flight - re-navigating rather
		// than just waiting longer picks up the real state on the next fetch
		// instead of masking a real bug.
		await expect(async () => {
			await page.goto(`/domains/${dom.id}?tab=dns`);
			await page.waitForLoadState('networkidle');
			await expect(page.getByTestId('dns-addon-required')).toHaveCount(0, { timeout: 2_000 });
			await expect(page.getByTestId('dns-load-error')).toHaveCount(0, { timeout: 2_000 });
		}).toPass({ timeout: 20_000 });
	});

	// Registrant contact updates now round-trip through the registrar's real
	// customer/contact resource graph (RDash: create a fresh contact, apply it
	// to all four contact roles) instead of a flat pass-through - state and
	// postal code are also newly mandatory (the registrar requires both).
	test('updating the registrant contact saves through the registrar', async ({ page }) => {
		await page.goto(`/domains/${domainId}?tab=contact`);
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('contact-form')).toBeVisible();

		const updatedEmail = `contact-${Date.now()}@e2e.test`;
		await page.locator('[name="first_name"]').fill('Updated');
		await page.locator('[name="last_name"]').fill('Registrant');
		await page.locator('[name="email"]').fill(updatedEmail);
		await page.locator('[name="phone"]').fill('081299999999');
		await page.locator('[name="address1"]').fill('Jl. Contact Update 1');
		await page.locator('[name="city"]').fill('Jakarta');
		await page.locator('[name="state"]').fill('DKI Jakarta');
		await page.locator('[name="postcode"]').fill('12190');
		await page.locator('[name="country"]').fill('ID');
		await page.getByTestId('contact-save').locator('button').click();

		await expect(page.getByRole('status')).toBeVisible({ timeout: 10_000 });
		await expect(page.getByTestId('contact-error')).toHaveCount(0);

		// Confirm it actually persisted at the registrar (not just local UI
		// state) by re-fetching the contact from the API.
		const res = await api.get(`${API_BASE}/api/v1/domains/${domainId}/contact`, { headers: authHeaders(client.accessToken) });
		expect(res.ok()).toBeTruthy();
		const contact = (await res.json()).data as { email?: string };
		expect(contact.email).toBe(updatedEmail);
	});
});

/**
 * Cart tax preview reflects the live billing.tax_enabled/tax_rate settings
 * (GET /public/config) instead of a hardcoded estimate - regression coverage
 * for a bug where the cart always showed an 11% PPN line even with tax
 * disabled in General Settings. Isolated in its own describe block since it
 * mutates the GLOBAL billing settings; `afterEach` restores the original
 * values unconditionally (even on assertion failure) so this never leaks
 * into other specs sharing the same live backend/DB.
 */
test.describe('cart tax preview', () => {
	let api: APIRequestContext;
	let client: Client;
	let adminTok: string;
	let originalBilling: Record<string, unknown>;

	test.beforeAll(async () => {
		api = await newApi();
		client = await registerVerifyLogin(api, 'carttax');
		adminTok = await adminToken(api);
		const res = await api.get(`${API_BASE}/api/v1/admin/settings`, { headers: authHeaders(adminTok) });
		const billing = (await res.json()).data.billing as Record<string, unknown>;
		originalBilling = {
			'billing.tax_enabled': billing.tax_enabled,
			'billing.tax_rate': billing.tax_rate,
			'billing.tax_inclusive': billing.tax_inclusive
		};
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, client.accessToken, client.refreshToken);
	});

	test.afterEach(async () => {
		const res = await api.put(`${API_BASE}/api/v1/admin/settings`, {
			headers: authHeaders(adminTok),
			data: originalBilling
		});
		expect(res.ok(), 'restore original billing tax settings').toBeTruthy();
	});

	test('cart summary omits the tax line when billing.tax_enabled is off', async ({ page }) => {
		const put = await api.put(`${API_BASE}/api/v1/admin/settings`, {
			headers: authHeaders(adminTok),
			data: { 'billing.tax_enabled': false }
		});
		expect(put.ok()).toBeTruthy();

		await addHostingToCart(page);
		await expect(page.getByTestId('cart-tax')).toHaveCount(0);
	});

	test('cart summary shows a computed tax line once billing.tax_enabled is on', async ({ page }) => {
		const put = await api.put(`${API_BASE}/api/v1/admin/settings`, {
			headers: authHeaders(adminTok),
			data: { 'billing.tax_enabled': true, 'billing.tax_rate': 11, 'billing.tax_inclusive': false }
		});
		expect(put.ok()).toBeTruthy();

		await addHostingToCart(page);
		await expect(page.getByTestId('cart-tax')).toBeVisible({ timeout: 10_000 });

		const subtotal = Number((await page.getByTestId('cart-subtotal').innerText()).replace(/[^\d]/g, ''));
		const tax = Number((await page.getByTestId('cart-tax').innerText()).replace(/[^\d]/g, ''));
		expect(tax).toBe(Math.round((subtotal * 11) / 100));
	});
});

/**
 * Checkout's email-verification gate (security.require_email_verification,
 * default true) - regression coverage for the existing hard block, plus the
 * new admin-configurable override. Isolated in its own describe block since
 * it mutates the GLOBAL security settings; `afterEach` restores the original
 * value unconditionally, matching the "cart tax preview" block above.
 */
test.describe('checkout email verification gate', () => {
	let api: APIRequestContext;
	let adminTok: string;
	let originalRequireVerify: boolean;

	test.beforeAll(async () => {
		api = await newApi();
		adminTok = await adminToken(api);
		const res = await api.get(`${API_BASE}/api/v1/admin/settings`, { headers: authHeaders(adminTok) });
		const security = (await res.json()).data.security as Record<string, unknown>;
		// The key may never have been explicitly saved yet (absent from the
		// grouped response) - the server's own default is `true`, so fall back
		// to that rather than restoring an `undefined` value (which would drop
		// the key from the PUT body entirely and fail settings validation).
		originalRequireVerify = security.require_email_verification !== false;
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.afterEach(async () => {
		const res = await api.put(`${API_BASE}/api/v1/admin/settings`, {
			headers: authHeaders(adminTok),
			data: { 'security.require_email_verification': originalRequireVerify }
		});
		expect(res.ok(), 'restore original require_email_verification setting').toBeTruthy();
	});

	test('an unverified client is blocked at checkout by default', async ({ page, context }) => {
		const put = await api.put(`${API_BASE}/api/v1/admin/settings`, {
			headers: authHeaders(adminTok),
			data: { 'security.require_email_verification': true }
		});
		expect(put.ok()).toBeTruthy();

		const client = await registerLogin(api, 'unverified');
		await setSessionCookies(context, client.accessToken, client.refreshToken);

		// The cart now surfaces the gate up-front: the verify-required panel
		// replaces the checkout form entirely (no submit button to click).
		await addHostingToCart(page);
		await expect(page.getByTestId('verify-required-panel')).toBeVisible();
		await expect(page.getByTestId('checkout-submit')).toHaveCount(0);

		// The backend gate stays the enforcer regardless of the UI: a direct
		// API checkout with the unverified token must still be rejected.
		const res = await api.post(`${API_BASE}/api/v1/orders`, {
			headers: authHeaders(client.accessToken),
			data: {
				captcha_token: 'e2e-dummy-captcha-token',
				items: [{ item_type: 'product', product_id: await seededHostingProductId(api), cycle: 'monthly', domain: 'unverified-gate.e2e.test' }]
			}
		});
		expect(res.status()).toBe(403);
		expect(await res.text()).toContain('verification');
	});

	test('disabling the setting lets an unverified client complete checkout', async ({ page, context }) => {
		const put = await api.put(`${API_BASE}/api/v1/admin/settings`, {
			headers: authHeaders(adminTok),
			data: { 'security.require_email_verification': false }
		});
		expect(put.ok()).toBeTruthy();

		const client = await registerLogin(api, 'unverified');
		await setSessionCookies(context, client.accessToken, client.refreshToken);

		await addHostingToCart(page);
		await page.getByTestId('checkout-submit').click();
		await expect(page).toHaveURL(/\/billing\/invoices\/\d+/, { timeout: 15_000 });
	});
});
