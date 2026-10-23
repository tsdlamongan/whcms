import { expect, request, test } from './fixtures';
import { API_BASE, MOCK_BASE } from './helpers';

/**
 * CAPTCHA (Cloudflare Turnstile) is optional and toggled by the
 * `security.captcha_enabled` setting. These checks are toggle-agnostic - they
 * read the current config and assert the widget's presence matches it - so the
 * suite stays green whether an operator has captcha on or off. Flipping the
 * global setting here would race the other specs on the shared stack, so we
 * never do that; enforcement of the enabled path is additionally covered by the
 * Go unit tests (internal/service/captcha, internal/integration/turnstile).
 */

async function captchaConfig() {
	const api = await request.newContext();
	const res = await api.get(`${API_BASE}/api/v1/public/config`);
	expect(res.ok(), await res.text()).toBeTruthy();
	const cfg = (await res.json()).data.captcha as {
		enabled: boolean;
		provider: string;
		site_key: string;
	};
	await api.dispose();
	return cfg;
}

test('public config endpoint exposes the captcha config shape', async () => {
	const cfg = await captchaConfig();
	expect(cfg).toBeTruthy();
	expect(typeof cfg.enabled).toBe('boolean');
	expect(cfg.provider).toBe('turnstile');
});

test('captcha widget presence on login/register/contact matches the enabled flag', async ({ page }) => {
	const cfg = await captchaConfig();
	const expected = cfg.enabled && !!cfg.site_key ? 1 : 0;

	await page.goto('/login');
	await expect(page.locator('input[name="email"]')).toBeVisible();
	await expect(page.getByTestId('captcha')).toHaveCount(expected);

	await page.goto('/register');
	await expect(page.getByTestId('register-form')).toBeVisible();
	await expect(page.getByTestId('captcha')).toHaveCount(expected);

	await page.goto('/contact');
	await expect(page.getByTestId('contact-form')).toBeVisible();
	await expect(page.getByTestId('captcha')).toHaveCount(expected);
});

test('mockserver siteverify accepts the always-pass test secret', async () => {
	// Sanity-check the dev/E2E hermetic verify path the backend uses when
	// captcha is enabled in development.
	const api = await request.newContext();
	const res = await api.post(`${MOCK_BASE}/turnstile/v0/siteverify`, {
		form: { secret: '1x0000000000000000000000000000000AA', response: 'any-token' }
	});
	expect(res.ok()).toBeTruthy();
	const body = (await res.json()) as { success: boolean };
	expect(body.success).toBe(true);
	await api.dispose();
});
