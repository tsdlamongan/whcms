import { expect, test, type APIRequestContext, type Page } from './fixtures';
import { adminToken, API_BASE, authHeaders, newApi, uniqueDomainLabel } from './helpers';

/**
 * Feedback while something is in flight, so users neither submit before they
 * can nor click the same button again and again:
 *
 *  - Domain search (a GET form whose load() asks the registrar) shows a spinner,
 *    locks its button and ignores repeat submits until results arrive.
 *  - The Turnstile widget is always visible (it used to hide itself while
 *    Cloudflare verified silently, so a quick submit posted an empty token),
 *    sizes itself to its container, keeps submit disabled until it holds a
 *    token, re-arms after a failed submit, and recovers from a failed api.js
 *    load without a page refresh.
 *
 * The widget is exercised against a fake `window.turnstile` served in place of
 * Cloudflare's api.js (page.route), with a NON-test sitekey so the component's
 * automation fast-path (test sitekeys only) is bypassed and the real widget
 * code path runs. That needs captcha switched on, which is global state - safe
 * here because the suite runs on a single worker; afterAll restores it.
 */

const CF_API = 'https://challenges.cloudflare.com/turnstile/v0/api.js*';
// The login form is the page's only POST form (the header KB search is a GET form).
const LOGIN_SUBMIT = 'form[method="POST"] button[type="submit"]';
// Not one of Cloudflare's documented test sitekeys.
const FAKE_SITEKEY = '0x4AAAAAAAe2eFakeSiteKeyNotATestKey';

/** Stand-in for Cloudflare's api.js; the tests drive the widget through window.__solveTurnstile. */
const FAKE_TURNSTILE_JS = `
(function () {
	var n = 0;
	window.__turnstileResets = 0;
	window.turnstile = {
		ready: function (cb) { cb(); },
		render: function (el, opts) {
			el.innerHTML = '<div data-fake-turnstile style="height:65px">fake widget</div>';
			window.__turnstileOpts = opts;
			return 'fake-' + (++n);
		},
		reset: function () { window.__turnstileResets++; },
		remove: function () {}
	};
	window.__solveTurnstile = function (tok) { window.__turnstileOpts.callback(tok); };
})();
`;

type TurnstileWindow = Window & {
	__solveTurnstile: (tok: string) => void;
	__turnstileResets: number;
	__turnstileOpts: { appearance: string; size: string };
};

async function fakeTurnstile(page: Page): Promise<void> {
	await page.route(CF_API, (route) =>
		route.fulfill({ status: 200, contentType: 'application/javascript', body: FAKE_TURNSTILE_JS })
	);
}

async function solve(page: Page, token: string): Promise<void> {
	await page.evaluate((tok) => (window as unknown as TurnstileWindow).__solveTurnstile(tok), token);
}

test.describe('domain search loading feedback', () => {
	/** Hold every SvelteKit data request for /order/domain until `release()` runs. */
	async function holdDomainSearch(page: Page) {
		let release!: () => void;
		const gate = new Promise<void>((resolve) => (release = resolve));
		const seen = { requests: 0 };
		await page.route('**/order/domain/__data.json*', async (route) => {
			seen.requests++;
			await gate;
			await route.continue();
		});
		return { release, seen };
	}

	test('order/domain: spinner + locked button while searching, repeat submits ignored', async ({ page }) => {
		await page.goto('/order/domain');
		await page.waitForLoadState('networkidle');

		const { release, seen } = await holdDomainSearch(page);
		const domain = `${uniqueDomainLabel()}.e2e.test`;
		const input = page.getByTestId('domain-search-input');
		const submit = page.getByTestId('domain-search-submit');
		await input.fill(domain);
		await submit.click();

		await expect(submit).toBeDisabled();
		await expect(submit).toHaveAttribute('aria-busy', 'true');
		await expect(submit.locator('.ca-spinner')).toBeVisible();
		// The router-level bar is held back ~150ms, so it only shows for slow loads.
		await expect(page.getByTestId('nav-progress')).toBeVisible();

		// Enter in the field must not start a second lookup either.
		await input.press('Enter');
		await submit.click({ force: true });
		// A negative check needs a beat: give a stray second request time to show up.
		await page.waitForTimeout(300);
		expect(seen.requests).toBe(1);

		release();
		await expect(page.getByTestId(`domain-result-${domain}`)).toBeVisible({ timeout: 15_000 });
		await expect(submit).toBeEnabled();
		await expect(submit.locator('.ca-spinner')).toHaveCount(0);
		await expect(page.getByTestId('nav-progress')).toHaveCount(0);
		expect(seen.requests).toBe(1);
	});

	test('order/domain: previous results are inert while a new search runs', async ({ page }) => {
		const first = `${uniqueDomainLabel()}.e2e.test`;
		await page.goto(`/order/domain?q=${encodeURIComponent(first)}`);
		await page.waitForLoadState('networkidle');
		const results = page.getByTestId('domain-results');
		await expect(results).toBeVisible({ timeout: 15_000 });
		await expect(results).not.toHaveClass(/ca-loading/);

		const { release } = await holdDomainSearch(page);
		const second = `${uniqueDomainLabel()}.e2e.test`;
		await page.getByTestId('domain-search-input').fill(second);
		await page.getByTestId('domain-search-submit').click();

		await expect(results).toHaveClass(/ca-loading/);
		await expect(results).toHaveAttribute('aria-busy', 'true');

		release();
		await expect(page.getByTestId(`domain-result-${second}`)).toBeVisible({ timeout: 15_000 });
		await expect(page.getByTestId('domain-results')).not.toHaveClass(/ca-loading/);
	});

	test('home hero search shows a spinner while the search page loads', async ({ page }) => {
		await page.goto('/');
		await page.waitForLoadState('networkidle');

		const { release, seen } = await holdDomainSearch(page);
		const domain = `${uniqueDomainLabel()}.e2e.test`;
		const form = page.getByTestId('home-domain-search');
		const submit = form.locator('button[type="submit"]');
		await form.locator('textarea[name="q"]').fill(domain);
		await submit.click();

		await expect(submit).toBeDisabled();
		await expect(submit).toHaveAttribute('aria-busy', 'true');
		await expect(submit.locator('.ca-spinner')).toBeVisible();
		expect(seen.requests).toBe(1);

		release();
		await expect(page).toHaveURL(/\/order\/domain\?q=/);
		await expect(page.getByTestId(`domain-result-${domain}`)).toBeVisible({ timeout: 15_000 });
	});
});

test.describe('turnstile widget (captcha enabled)', () => {
	let api: APIRequestContext;
	let token: string;
	let original: { enabled: boolean; siteKey: string };

	async function putSettings(values: Record<string, unknown>): Promise<void> {
		const res = await api.put(`${API_BASE}/api/v1/admin/settings`, { headers: authHeaders(token), data: values });
		expect(res.ok(), await res.text()).toBeTruthy();
	}

	test.beforeAll(async () => {
		api = await newApi();
		token = await adminToken(api);
		const res = await api.get(`${API_BASE}/api/v1/public/config`);
		const cfg = (await res.json()).data.captcha as { enabled: boolean; site_key: string };
		original = { enabled: cfg.enabled, siteKey: cfg.site_key };
		await putSettings({ 'security.captcha_enabled': true, 'security.captcha_site_key': FAKE_SITEKEY });
	});

	test.afterAll(async () => {
		await putSettings({
			'security.captcha_enabled': original.enabled,
			'security.captcha_site_key': original.siteKey
		});
		await api.dispose();
	});

	test('widget is visible from first paint, gates submit until it has a token, and re-arms after a failed login', async ({
		page
	}) => {
		// Hold api.js so the "still loading" placeholder is observable.
		let release!: () => void;
		const gate = new Promise<void>((resolve) => (release = resolve));
		await page.route(CF_API, async (route) => {
			await gate;
			await route.fulfill({ status: 200, contentType: 'application/javascript', body: FAKE_TURNSTILE_JS });
		});

		// A script still in flight holds up the window `load` event, so don't wait for it.
		await page.goto('/login', { waitUntil: 'commit' });
		const captcha = page.getByTestId('captcha');
		const submit = page.locator(LOGIN_SUBMIT);

		// Never blank: a placeholder with a spinner stands in until Cloudflare's script arrives.
		await expect(captcha).toHaveAttribute('data-status', 'loading');
		await expect(page.getByTestId('captcha-hint').locator('.ca-spinner')).toBeVisible();
		await expect(submit).toBeDisabled();

		release();
		await expect(page.locator('[data-fake-turnstile]')).toBeVisible();
		await expect(captcha).toHaveAttribute('data-status', 'verifying');
		// Always shown (not 'interaction-only') and fluid width on a normal viewport.
		const opts = await page.evaluate(() => (window as unknown as TurnstileWindow).__turnstileOpts);
		expect(opts.appearance).toBe('always');
		expect(opts.size).toBe('flexible');
		// Posting now would only earn "captcha verification required".
		await expect(submit).toBeDisabled();
		await expect(page.getByTestId('captcha-token')).toHaveValue('');

		await solve(page, 'tok-first');
		await expect(captcha).toHaveAttribute('data-status', 'ready');
		await expect(page.getByTestId('captcha-token')).toHaveValue('tok-first');
		await expect(submit).toBeEnabled();

		// A failed login consumes the single-use token: the widget re-arms and submit waits for a fresh one.
		await page.locator('input[name="email"]').fill('nobody-here@e2e.test');
		await page.locator('input[name="password"]').fill('definitely-wrong-password');
		// The widget's token really is what gets posted with the form.
		const posted = page.waitForRequest((r) => r.method() === 'POST' && (r.postData() ?? '').includes('tok-first'));
		await submit.click();
		await posted;
		await expect(page.locator('.ca-alert-danger')).toBeVisible({ timeout: 15_000 });
		await expect(captcha).toHaveAttribute('data-status', 'verifying');
		await expect(page.getByTestId('captcha-token')).toHaveValue('');
		await expect(submit).toBeDisabled();
		expect(await page.evaluate(() => (window as unknown as TurnstileWindow).__turnstileResets)).toBe(1);

		await solve(page, 'tok-second');
		await expect(page.getByTestId('captcha-token')).toHaveValue('tok-second');
		await expect(submit).toBeEnabled();
	});

	test('a failed api.js load shows a retry that recovers without a page refresh', async ({ page }) => {
		let loads = 0;
		await page.route(CF_API, (route) => {
			loads++;
			return loads === 1
				? route.abort('failed')
				: route.fulfill({ status: 200, contentType: 'application/javascript', body: FAKE_TURNSTILE_JS });
		});

		await page.goto('/login');
		const captcha = page.getByTestId('captcha');
		await expect(captcha).toHaveAttribute('data-status', 'error');
		await expect(page.getByTestId('captcha-hint')).toBeVisible();
		await expect(page.locator(LOGIN_SUBMIT)).toBeDisabled();

		await page.getByTestId('captcha-retry').click();
		await expect(page.locator('[data-fake-turnstile]')).toBeVisible();
		await expect(captcha).toHaveAttribute('data-status', 'verifying');
		expect(loads).toBe(2);

		await solve(page, 'tok-after-retry');
		await expect(page.locator(LOGIN_SUBMIT)).toBeEnabled();
	});

	test('a client-side navigation after a failed load still gets a working widget', async ({ page }) => {
		// Regression: the stale <script> from the failed attempt used to make every
		// later mount wait forever on events that never fire again.
		let loads = 0;
		await page.route(CF_API, (route) => {
			loads++;
			return loads === 1
				? route.abort('failed')
				: route.fulfill({ status: 200, contentType: 'application/javascript', body: FAKE_TURNSTILE_JS });
		});

		await page.goto('/login');
		await expect(page.getByTestId('captcha')).toHaveAttribute('data-status', 'error');

		await page.locator('a.login-create').click();
		await expect(page.getByTestId('register-form')).toBeVisible();
		await expect(page.locator('[data-fake-turnstile]')).toBeVisible();
		await expect(page.getByTestId('captcha')).toHaveAttribute('data-status', 'verifying');
	});

	test('widget falls back to the compact size when its container is narrower than 300px', async ({ page }) => {
		await fakeTurnstile(page);
		await page.setViewportSize({ width: 280, height: 800 });
		await page.goto('/login');
		await expect(page.locator('[data-fake-turnstile]')).toBeVisible();
		const opts = await page.evaluate(() => (window as unknown as TurnstileWindow).__turnstileOpts);
		expect(opts.size).toBe('compact');
		expect(opts.appearance).toBe('always');
	});
});
