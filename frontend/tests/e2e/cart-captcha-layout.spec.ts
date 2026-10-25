import { expect, test, type APIRequestContext, type Page } from './fixtures';
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
 * Cart checkout card: on desktop the Turnstile widget spans the same width as
 * the "Checkout & Create Invoice" button (and sits flush with it), instead of
 * the 150px `compact` widget Cloudflare falls back to when its container is
 * narrower than 300px.
 *
 * The widget is exercised against a fake `window.turnstile` that sizes itself
 * the way Cloudflare does (`flexible` = 100% of the container, `compact` =
 * 150px), with a NON-test sitekey so the component's automation fast-path is
 * bypassed. Captcha is global state; the suite runs on a single worker and
 * afterAll restores it.
 */

const CF_API = 'https://challenges.cloudflare.com/turnstile/v0/api.js*';
const FAKE_SITEKEY = '0x4AAAAAAAe2eFakeSiteKeyNotATestKey';
const CART_KEY = 'whcms.cart.v1';

/** Stand-in for api.js that lays the widget out like Cloudflare's own sizes. */
const FAKE_TURNSTILE_JS = `
(function () {
	var n = 0;
	window.turnstile = {
		render: function (el, opts) {
			var compact = opts.size === 'compact';
			el.innerHTML = '<div data-fake-turnstile style="box-sizing:border-box;height:' +
				(compact ? 140 : 65) + 'px;width:' + (compact ? '150px' : '100%') + ';background:#ddd">fake</div>';
			window.__turnstileOpts = opts;
			return 'fake-' + (++n);
		},
		reset: function () {},
		remove: function () {}
	};
})();
`;

type TurnstileWindow = Window & { __turnstileOpts: { size: string } };

async function box(page: Page, testid: string) {
	const b = await page.getByTestId(testid).boundingBox();
	if (!b) throw new Error(`no bounding box for ${testid}`);
	return b;
}

test.describe('cart: captcha widget width', () => {
	let api: APIRequestContext;
	let admin: string;
	let client: Client;
	let original: { enabled: boolean; siteKey: string };

	async function putSettings(values: Record<string, unknown>): Promise<void> {
		const res = await api.put(`${API_BASE}/api/v1/admin/settings`, {
			headers: authHeaders(admin),
			data: values
		});
		expect(res.ok(), await res.text()).toBeTruthy();
	}

	test.beforeAll(async () => {
		api = await newApi();
		admin = await adminToken(api);
		// register BEFORE captcha is switched on
		client = await registerVerifyLogin(api, 'captchalayout');
		const res = await api.get(`${API_BASE}/api/v1/public/config`);
		const cfg = (await res.json()).data.captcha as { enabled: boolean; site_key: string };
		original = { enabled: cfg.enabled, siteKey: cfg.site_key };
		await putSettings({
			'security.captcha_enabled': true,
			'security.captcha_site_key': FAKE_SITEKEY
		});
	});

	test.afterAll(async () => {
		await putSettings({
			'security.captcha_enabled': original.enabled,
			'security.captcha_site_key': original.siteKey
		});
		await api.dispose();
	});

	test.beforeEach(async ({ page, context }) => {
		await page.route(CF_API, (route) =>
			route.fulfill({ status: 200, contentType: 'application/javascript', body: FAKE_TURNSTILE_JS })
		);
		await setSessionCookies(context, client.accessToken, client.refreshToken);
		// one sellable domain line in the cart (.com is priced in the e2e seed)
		await page.addInitScript(
			([key, domain]) => {
				localStorage.setItem(
					key,
					JSON.stringify({
						items: [
							{
								uid: 'e2e-line',
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
			[CART_KEY, `${uniqueDomainLabel()}.com`]
		);
	});

	async function openCart(page: Page) {
		await page.goto('/order/cart');
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('checkout-submit')).toBeVisible();
		await expect(page.locator('[data-fake-turnstile]')).toBeVisible();
	}

	for (const width of [1440, 1280]) {
		test(`desktop ${width}px: widget is flexible and as wide as the checkout button`, async ({
			page
		}) => {
			await page.setViewportSize({ width, height: 900 });
			await openCart(page);

			const size = await page.evaluate(
				() => (window as unknown as TurnstileWindow).__turnstileOpts.size
			);
			expect(size, 'a 300px+ container must get the full-width widget').toBe('flexible');

			const widget = (await page.locator('[data-fake-turnstile]').boundingBox())!;
			const button = await box(page, 'checkout-submit');
			expect(
				Math.abs(widget.width - button.width),
				`widget ${widget.width}px vs button ${button.width}px`
			).toBeLessThanOrEqual(1);
			expect(Math.abs(widget.x - button.x), 'left edges line up').toBeLessThanOrEqual(1);
			expect(widget.width).toBeGreaterThanOrEqual(300);
		});
	}

	test('tablet 1100px: the stacked card still has widget and button flush', async ({ page }) => {
		await page.setViewportSize({ width: 1100, height: 900 });
		await openCart(page);
		const size = await page.evaluate(
			() => (window as unknown as TurnstileWindow).__turnstileOpts.size
		);
		expect(size).toBe('flexible');
		const widget = (await page.locator('[data-fake-turnstile]').boundingBox())!;
		const button = await box(page, 'checkout-submit');
		expect(Math.abs(widget.width - button.width)).toBeLessThanOrEqual(1);
		expect(Math.abs(widget.x - button.x)).toBeLessThanOrEqual(1);
	});

	test('phone 320px: compact fallback, nothing overflows', async ({ page }) => {
		await page.setViewportSize({ width: 320, height: 700 });
		await openCart(page);
		const size = await page.evaluate(
			() => (window as unknown as TurnstileWindow).__turnstileOpts.size
		);
		expect(['flexible', 'compact']).toContain(size);
		const overflow = await page.evaluate(
			() => document.documentElement.scrollWidth - document.documentElement.clientWidth
		);
		expect(overflow, 'page scrolls sideways').toBeLessThanOrEqual(0);
		const card = (await page
			.getByTestId('checkout-submit')
			.locator('xpath=ancestor::section')
			.boundingBox())!;
		const widget = (await page.locator('[data-fake-turnstile]').boundingBox())!;
		expect(widget.x + widget.width).toBeLessThanOrEqual(card.x + card.width + 1);
	});
});
