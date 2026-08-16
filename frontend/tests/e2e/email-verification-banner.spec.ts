import { expect, test, type APIRequestContext } from './fixtures';
import {
	API_BASE,
	newApi,
	registerLogin,
	setSessionCookies,
	uniqueDomainLabel,
	VERIFY_LINK,
	waitForMailMatch,
	type Client
} from './helpers';

/**
 * Email-verification visibility for a logged-in but unverified client.
 *
 * Checkout is gated on email verification (orders.Checkout rejects with
 * "email verification required before checkout"), but the client area used to
 * give no hint: the dashboard showed nothing and the cart's verify-required
 * panel never rendered because its load parsed a flat `email_verified_at`
 * field that /auth/me (nested MeResponse) never had - so unverified users ran
 * straight into the raw backend error. This spec covers the fix end-to-end:
 *
 * 1. an unverified client sees a dashboard banner with a resend button, and
 *    resending actually lands a verification email in the mock mail sink;
 * 2. the cart shows the verify-required panel instead of the checkout form
 *    (regression for the broken parse);
 * 3. after verifying via the emailed link, both the banner and the panel are
 *    gone and the checkout form is reachable.
 */

test.describe.configure({ mode: 'serial', timeout: 90_000 });

test.describe('unverified client: dashboard banner + cart panel + resend', () => {
	let api: APIRequestContext;
	let client: Client;

	test.beforeAll(async () => {
		api = await newApi();
		// Registered + logged in, deliberately NOT verified.
		client = await registerLogin(api, 'verifybanner');
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, client.accessToken, client.refreshToken);
	});

	test('dashboard shows the unverified banner and resend sends a fresh email', async ({
		page
	}) => {
		await page.goto('/dashboard');
		await expect(page.getByTestId('dashboard-verify-banner')).toBeVisible();
		await expect(page.getByTestId('dashboard-verify-banner')).toContainText(client.email);

		await page.getByTestId('dashboard-resend-submit').click();
		await expect(page.getByTestId('dashboard-resend-success')).toBeVisible();

		// The resend must actually deliver a verification link to the inbox.
		const token = await waitForMailMatch(api, client.email, VERIFY_LINK);
		expect(token).toBeTruthy();
	});

	test('cart shows the verify-required panel instead of the checkout form', async ({ page }) => {
		// Put a real product in the cart first - the checkout sidebar (and the
		// verify panel that replaces it) only renders with items present.
		await page.goto('/order/product/e2e-shared-hosting');
		await page.getByTestId('product-cycle-monthly').check();
		await page.getByTestId('domain-mode-own').check();
		await page.getByTestId('domain-search-input').fill(`${uniqueDomainLabel()}.verify.e2e.test`);
		await page.getByTestId('add-to-cart').click();
		await expect(page).toHaveURL(/\/order\/cart/);

		await expect(page.getByTestId('verify-required-panel')).toBeVisible();
		await expect(page.getByTestId('resend-verification')).toBeVisible();
		await expect(page.getByTestId('checkout-submit')).toHaveCount(0);
	});

	test('after verifying, the banner and panel are gone and checkout is reachable', async ({
		page
	}) => {
		const token = await waitForMailMatch(api, client.email, VERIFY_LINK);
		const verify = await api.post(`${API_BASE}/api/v1/auth/verify-email`, { data: { token } });
		expect(verify.ok(), `verify-email: ${await verify.text()}`).toBeTruthy();

		await page.goto('/dashboard');
		await expect(page.getByTestId('dashboard-welcome')).toBeVisible();
		await expect(page.getByTestId('dashboard-verify-banner')).toHaveCount(0);

		// The cart item from the previous test lives in this fresh context's
		// empty localStorage, so re-add one to reach the checkout sidebar.
		await page.goto('/order/product/e2e-shared-hosting');
		await page.getByTestId('product-cycle-monthly').check();
		await page.getByTestId('domain-mode-own').check();
		await page.getByTestId('domain-search-input').fill(`${uniqueDomainLabel()}.verify.e2e.test`);
		await page.getByTestId('add-to-cart').click();
		await expect(page).toHaveURL(/\/order\/cart/);

		await expect(page.getByTestId('verify-required-panel')).toHaveCount(0);
		await expect(page.getByTestId('checkout-submit')).toBeVisible();
	});
});
