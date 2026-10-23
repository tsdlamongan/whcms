import { expect, test } from './fixtures';
import { APP_BASE } from './helpers';

/**
 * Paths that start with '//' still match a route (//login renders /login), and
 * on such a page SvelteKit < 3 can turn an enhanced form's result into a
 * protocol-relative navigation (GHSA-pfhg-q3qv-9q6r). The handle hook folds
 * the leading slashes into one with a 308, always onto this origin.
 *
 * URLs are absolute on purpose: a bare '//login' would resolve against the
 * baseURL as protocol-relative, i.e. to the host "login".
 */

test.describe('double-slash paths', () => {
	const cases: { path: string; location: string }[] = [
		{ path: '//login', location: '/login' },
		{ path: '///register', location: '/register' },
		// The query string survives, so emailed links with a stray '//' keep working.
		{ path: '//verify-email?token=abc', location: '/verify-email?token=abc' },
		// Never an off-site Location: the would-be host becomes a path segment.
		{ path: '//evil.example/login', location: '/evil.example/login' },
		{ path: '//%5Cevil.example/login', location: '/%5Cevil.example/login' }
	];

	for (const { path, location } of cases) {
		test(`${path} redirects to ${location} on the same origin`, async ({ request }) => {
			const res = await request.get(`${APP_BASE}${path}`, { maxRedirects: 0 });
			expect(res.status()).toBe(308);
			expect(res.headers()['location']).toBe(location);
		});
	}

	test('the browser lands on the single-slash page and its form still works', async ({ page }) => {
		await page.goto(`${APP_BASE}//login`);
		await expect(page).toHaveURL(`${APP_BASE}/login`);
		await expect(page.locator('input[name="email"]')).toBeVisible();
	});

	test('ordinary paths are untouched', async ({ request }) => {
		const res = await request.get(`${APP_BASE}/login`, { maxRedirects: 0 });
		expect(res.status()).toBe(200);
	});
});
