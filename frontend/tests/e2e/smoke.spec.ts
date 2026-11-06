import { expect, test } from './fixtures';

test('root renders the public portal for anonymous visitors, login still reachable', async ({
	page
}) => {
	// The root '/' now serves the Twenty-One public portal (no longer a redirect
	// to /login) for anonymous visitors.
	await page.goto('/');
	await expect(page.getByTestId('portal-home')).toBeVisible();

	// Login remains reachable and functional.
	await page.goto('/login');
	await expect(page.locator('input[name="email"]')).toBeVisible();
	await expect(page.locator('input[name="password"]')).toBeVisible();
	await expect(page.locator('button[type="submit"]')).toBeVisible();
});

test('page title and nav brand follow PUBLIC_APP_NAME, not a hardcoded product name', async ({
	page
}) => {
	// The home page's <title> is the bare app name - a stable value to compare
	// against the nav brand without hardcoding what PUBLIC_APP_NAME resolves to.
	await page.goto('/');
	const brand = await page.getByTestId('app-name').innerText();
	expect(brand.length).toBeGreaterThan(0);
	await expect(page).toHaveTitle(brand);

	await page.goto('/login');
	const title = await page.title();
	expect(title.endsWith(` — ${brand}`)).toBe(true);
});
