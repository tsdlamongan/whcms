import { expect, test, type APIRequestContext } from './fixtures';
import {
	API_BASE,
	adminToken,
	authHeaders,
	newApi,
	registerVerifyLogin,
	setSessionCookies,
	uniqueDomainLabel,
	type Client
} from './helpers';

/**
 * A domain can only be bought when its extension has an ACTIVE row in Domain
 * Pricing. An extension the operator never priced (or switched off) is shown
 * as "not offered" - no price, no add-to-cart - and is refused at checkout,
 * so the registrar's own (cost) quote can never become the selling price.
 */

const CART_KEY = 'whcms.cart.v1';

/** A TLD label no other spec or seed uses: letters only (the last label must be). */
function randomTld(): string {
	return `q${Array.from({ length: 8 }, () => 'abcdefghijklmnopqrstuvwxyz'[Math.floor(Math.random() * 26)]).join('')}`;
}

test.describe('only extensions priced in Domain Pricing are sold', () => {
	let api: APIRequestContext;
	let admin: string;
	let client: Client;
	let registrarId: number;
	const createdTlds: number[] = [];

	function tldBody(tld: string, active: boolean) {
		return {
			tld,
			registrar_id: registrarId,
			active,
			min_years: 1,
			max_years: 3,
			register_prices: { '1': 90000, '2': 170000, '3': 250000 },
			renew_prices: { '1': 95000 },
			transfer_price: 80000
		};
	}

	async function createTld(tld: string, active: boolean): Promise<number> {
		const res = await api.post(`${API_BASE}/api/v1/admin/tld-pricing`, {
			headers: authHeaders(admin),
			data: tldBody(tld, active)
		});
		expect(res.ok(), `create tld pricing: ${await res.text()}`).toBeTruthy();
		const id = ((await res.json()).data as { id: number }).id;
		createdTlds.push(id);
		return id;
	}

	async function check(name: string) {
		const res = await api.post(`${API_BASE}/api/v1/domains/check`, { data: { names: [name] } });
		expect(res.ok(), await res.text()).toBeTruthy();
		return (
			(await res.json()).data as Array<{
				name: string;
				available: boolean;
				sellable: boolean;
				price: number;
				premium: boolean;
			}>
		)[0];
	}

	async function order(name: string, itemType = 'domain_register') {
		return api.post(`${API_BASE}/api/v1/orders`, {
			headers: authHeaders(client.accessToken),
			data: {
				captcha_token: 'e2e-dummy-captcha-token',
				items: [{ item_type: itemType, domain: name, domain_years: 1, epp_code: 'epp' }]
			}
		});
	}

	test.beforeAll(async () => {
		api = await newApi();
		admin = await adminToken(api);
		client = await registerVerifyLogin(api, 'sellable');
		const regs = await api.get(`${API_BASE}/api/v1/admin/registrars`, {
			headers: authHeaders(admin)
		});
		registrarId = ((await regs.json()).data as { id: number }[])[0].id;
	});

	test.afterAll(async () => {
		for (const id of createdTlds) {
			await api.delete(`${API_BASE}/api/v1/admin/tld-pricing/${id}`, {
				headers: authHeaders(admin)
			});
		}
		await api.dispose();
	});

	test('an extension with no pricing row is not sellable: no price, refused at checkout', async () => {
		const name = `${uniqueDomainLabel()}.${randomTld()}`;

		const result = await check(name);
		expect(result.sellable).toBe(false);
		expect(result.price, 'the registrar cost quote must not be exposed').toBe(0);
		expect(result.premium).toBe(false);

		for (const itemType of ['domain_register', 'domain_transfer']) {
			const res = await order(name, itemType);
			expect(res.status(), `${itemType} must be refused`).toBe(422);
			const body = await res.json();
			expect(JSON.stringify(body.error)).toContain('not offered for sale');
		}
	});

	test('an INACTIVE pricing row is not sellable either', async () => {
		const tld = randomTld();
		await createTld(tld, false);
		const name = `${uniqueDomainLabel()}.${tld}`;
		expect((await check(name)).sellable).toBe(false);
		expect((await order(name)).status()).toBe(422);
	});

	test('an active row sells at its own price; switching it off stops sales again', async () => {
		const tld = randomTld();
		const id = await createTld(tld, true);
		const name = `${uniqueDomainLabel()}.${tld}`;

		const live = await check(name);
		expect(live.sellable).toBe(true);
		expect(live.price, 'priced from Domain Pricing, not the registrar quote').toBe(90000);

		const ok = await order(name);
		expect(ok.ok(), `order: ${await ok.text()}`).toBeTruthy();
		const created = (await ok.json()).data as { items: Array<{ unit_price: number }> };
		expect(created.items[0].unit_price).toBe(90000);

		// the operator switches the extension off
		const off = await api.put(`${API_BASE}/api/v1/admin/tld-pricing/${id}`, {
			headers: authHeaders(admin),
			data: tldBody(tld, false)
		});
		expect(off.ok(), await off.text()).toBeTruthy();

		const second = `${uniqueDomainLabel()}.${tld}`;
		expect((await check(second)).sellable).toBe(false);
		expect((await order(second)).status()).toBe(422);
	});

	test('search results flag an unsold extension and offer no add-to-cart', async ({ page }) => {
		const label = uniqueDomainLabel();
		const unsold = `${label}.${randomTld()}`;

		await page.goto(`/order/domain?q=${encodeURIComponent(unsold)}`);
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId(`domain-result-${unsold}`)).toBeVisible({ timeout: 20_000 });
		await expect(page.getByTestId(`domain-not-offered-${unsold}`)).toBeVisible();
		await expect(page.getByTestId(`domain-register-${unsold}`)).toHaveCount(0);
		await expect(page.getByTestId(`domain-transfer-${unsold}`)).toHaveCount(0);

		// a seeded, priced extension is sold as before
		const sold = `${label}.com`;
		await page.goto(`/order/domain?q=${encodeURIComponent(sold)}`);
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId(`domain-register-${sold}`)).toBeVisible({ timeout: 20_000 });
		await expect(page.getByTestId(`domain-not-offered-${sold}`)).toHaveCount(0);
	});

	test('the "transfer my own domain" form refuses an unsold extension', async ({ page }) => {
		await page.goto('/order/domain');
		await page.waitForLoadState('networkidle');
		await page
			.getByTestId('transfer-own-domain-input')
			.fill(`${uniqueDomainLabel()}.${randomTld()}`);
		await page.getByTestId('transfer-own-add').click();
		await expect(
			page.getByRole('alert').filter({ hasText: /tidak kami jual|do not sell/i })
		).toBeVisible();
	});

	test('a stale cart line for an unsold extension is flagged and checkout is blocked', async ({
		page,
		context
	}) => {
		await setSessionCookies(context, client.accessToken, client.refreshToken);
		const unsold = `${uniqueDomainLabel()}.${randomTld()}`;
		await page.addInitScript(
			([key, domain]) => {
				localStorage.setItem(
					key,
					JSON.stringify({
						items: [
							{
								uid: 'stale',
								item_type: 'domain_register',
								domain,
								cycle: 'annually',
								unit_price: 150000,
								setup_fee: 0,
								domain_years: 1
							}
						],
						coupon: null
					})
				);
			},
			[CART_KEY, unsold]
		);

		await page.goto('/order/cart');
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('cart-tld-not-offered-0')).toBeVisible();
		await expect(page.getByTestId('checkout-submit')).toBeDisabled();
	});
});
