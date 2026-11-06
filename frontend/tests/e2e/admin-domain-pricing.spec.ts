import { expect, test, type APIRequestContext, type Locator, type Page } from './fixtures';
import { API_BASE, adminToken, authHeaders, clickBtn, newApi, setSessionCookies, uniqueDomainLabel } from './helpers';

/**
 * Click an addon row's Save and wait for the form-action response. Reloading
 * (or ending the test) right after the click cancels an in-flight save on a
 * loaded runner, so the change silently never lands.
 */
async function saveAddonRow(page: Page, row: Locator): Promise<void> {
	await Promise.all([
		page.waitForResponse((r) => r.request().method() === 'POST' && r.url().includes('/admin/domains/addons')),
		row.locator('[data-testid^="domain-addon-save-"]').click()
	]);
}

/**
 * Admin TLD Pricing, Premium Domain Pricing, and Domain Addons CRUD -
 * /admin/domains/pricing and /admin/domains/addons.
 */

test.describe('admin domain pricing and addons', () => {
	let api: APIRequestContext;
	let token: string;

	test.beforeAll(async () => {
		api = await newApi();
		token = await adminToken(api);
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, token);
	});

	test('create, edit and delete a TLD pricing row', async ({ page }) => {
		const tld = uniqueDomainLabel();

		await page.goto('/admin/domains/pricing');
		await page.waitForLoadState('networkidle');
		await clickBtn(page, 'tld-pricing-create-button');
		const form = page.getByTestId('tld-pricing-form');
		await expect(form).toBeVisible();

		await form.locator('[name="tld"]').fill(tld);
		await form.locator('[name="min_years"]').fill('1');
		await form.locator('[name="max_years"]').fill('3');
		await form.locator('[name="transfer_price"]').fill('90000');
		await form.locator('[name="restore_price"]').fill('500000');
		await page.getByTestId('register-price-1-input').fill('100000');
		await page.getByTestId('register-price-2-input').fill('180000');
		await page.getByTestId('renew-price-1-input').fill('110000');
		await clickBtn(page, 'tld-pricing-form-submit');

		await expect(page.getByText(`.${tld}`)).toBeVisible({ timeout: 10_000 });
		const row = page.locator('tr', { hasText: `.${tld}` });
		await expect(row.getByText('Active')).toBeVisible();
		await expect(row).toContainText('500.000');

		// Edit: confirm the restore price round-trips, then flip active off.
		await row.locator('[data-testid^="tld-pricing-edit-"]').click();
		await expect(form).toBeVisible();
		await expect(form.locator('[name="restore_price"]')).toHaveValue('500000');
		await form.locator('[name="active"]').uncheck();
		await clickBtn(page, 'tld-pricing-form-submit');
		await expect(row.getByText('Inactive')).toBeVisible({ timeout: 10_000 });

		// Delete.
		await row.locator('[data-testid^="tld-pricing-delete-"]').click();
		await page.getByRole('dialog').getByRole('button', { name: /delete/i }).click();
		await expect(page.getByText(`.${tld}`)).toHaveCount(0, { timeout: 10_000 });
	});

	test('import a TLD from the registrar catalog with markup applied', async ({ page }) => {
		// Fixed mock-catalog fixture (mockserver/rdash.go rdashCatalogTLDs):
		// .my.id costs Rp75.000/yr at the registrar.
		const tld = 'my.id';
		const markupPercent = 20;
		const expectedSellPrice = 90_000; // 75_000 * 1.20

		await page.goto('/admin/domains/pricing');
		await page.waitForLoadState('networkidle');

		// Self-heal: a previous failed run may have left this TLD imported.
		const existingRow = page.locator('tr', { hasText: `.${tld}` });
		if (await existingRow.count()) {
			await existingRow.locator('[data-testid^="tld-pricing-delete-"]').click();
			await page.getByRole('dialog').getByRole('button', { name: /delete/i }).click();
			await expect(page.getByText(`.${tld}`)).toHaveCount(0, { timeout: 10_000 });
		}

		await clickBtn(page, 'tld-pricing-import-button');
		const importForm = page.getByTestId('tld-pricing-import-form');
		await expect(importForm).toBeVisible();

		// Registrar source picker is present (modular - ready for a future
		// second registrar even though only "rdash" exists today).
		await expect(importForm.locator('[name="_registrar_select"]')).toBeVisible();
		await expect(importForm.locator('[name="_registrar_select"]')).toHaveValue(/.+/);

		const catalogTable = page.getByTestId('tld-pricing-import-catalog');
		await expect(catalogTable.getByText(`.${tld}`)).toBeVisible({ timeout: 10_000 });

		await importForm.locator('[name="search"]').fill(tld);
		await expect(catalogTable.getByText(`.${tld}`)).toBeVisible();
		await importForm.locator('[name="markup_percent"]').fill(String(markupPercent));

		// Use "Select All" (filtered to just this one TLD by the search box
		// above) instead of checking the row directly, exercising the bulk
		// select-all control end to end.
		await clickBtn(page, 'tld-pricing-import-select-all');
		await expect(page.getByTestId(`tld-pricing-import-check-${tld}`)).toBeChecked();

		await clickBtn(page, 'tld-pricing-import-submit');

		await expect(page.getByText(`.${tld}`)).toBeVisible({ timeout: 10_000 });
		const row = page.locator('tr', { hasText: `.${tld}` });
		await expect(row.getByText('Active')).toBeVisible();
		await expect(row).toContainText(expectedSellPrice.toLocaleString('id-ID'));

		// Clean up so this fixed-name row doesn't leak into other runs.
		await row.locator('[data-testid^="tld-pricing-delete-"]').click();
		await page.getByRole('dialog').getByRole('button', { name: /delete/i }).click();
		await expect(page.getByText(`.${tld}`)).toHaveCount(0, { timeout: 10_000 });
	});

	test('an explicit year-1 register price of 0 (free-domain promo) is saved and honored at checkout', async ({
		page
	}) => {
		const tld = uniqueDomainLabel();

		await page.goto('/admin/domains/pricing');
		await page.waitForLoadState('networkidle');
		await clickBtn(page, 'tld-pricing-create-button');
		const form = page.getByTestId('tld-pricing-form');
		await expect(form).toBeVisible();

		await form.locator('[name="tld"]').fill(tld);
		await form.locator('[name="min_years"]').fill('1');
		await form.locator('[name="max_years"]').fill('2');
		await form.locator('[name="transfer_price"]').fill('50000');
		await form.locator('[name="restore_price"]').fill('250000');
		// Year 1 register is free (the promo); year 2 is normal price, and
		// renewal is normal from year 1 onward - free is a registration-only
		// discount, not a renewal discount.
		await page.getByTestId('register-price-1-input').fill('0');
		await page.getByTestId('register-price-2-input').fill('100000');
		await page.getByTestId('renew-price-1-input').fill('60000');
		await clickBtn(page, 'tld-pricing-form-submit');

		await expect(page.getByText(`.${tld}`)).toBeVisible({ timeout: 10_000 });
		const row = page.locator('tr', { hasText: `.${tld}` });
		await expect(row).toContainText('Rp0,-');

		// Re-open edit: the year-1 field must round-trip as an explicit "0",
		// not come back blank (which would mean it was never actually saved -
		// the exact bug this test guards against).
		await row.locator('[data-testid^="tld-pricing-edit-"]').click();
		await expect(form).toBeVisible();
		await expect(page.getByTestId('register-price-1-input')).toHaveValue('0');
		await expect(page.getByTestId('register-price-3-input')).toHaveValue('');
		await clickBtn(page, 'tld-pricing-form-submit');
		await expect(row).toBeVisible({ timeout: 10_000 });

		// The public storefront price-check endpoint must reflect the free
		// price too - this is what a customer's cart preview reads.
		const api = await newApi();
		const admin = await adminToken(api);
		const checkRes = await api.post(`${API_BASE}/api/v1/domains/check`, {
			headers: authHeaders(admin),
			data: { names: [`${uniqueDomainLabel()}.${tld}`] }
		});
		const checkBody = await checkRes.json();
		expect(checkBody.data[0].price).toBe(0);
		await api.dispose();

		// Clean up.
		await row.locator('[data-testid^="tld-pricing-delete-"]').click();
		await page.getByRole('dialog').getByRole('button', { name: /delete/i }).click();
		await expect(page.getByText(`.${tld}`)).toHaveCount(0, { timeout: 10_000 });
	});

	test('create and delete a premium domain pricing row', async ({ page }) => {
		const domainName = `${uniqueDomainLabel()}.test`;

		await page.goto('/admin/domains/pricing');
		await page.waitForLoadState('networkidle');
		await page.getByRole('tab', { name: 'Premium Domain Pricing' }).click();
		await clickBtn(page, 'premium-pricing-create-button');
		const form = page.getByTestId('premium-pricing-form');
		await expect(form).toBeVisible();

		await form.locator('[name="domain_name"]').fill(domainName);
		await form.locator('[name="register_price"]').fill('5000000');
		await form.locator('[name="renew_price"]').fill('4500000');
		await form.locator('[name="transfer_price"]').fill('3000000');
		await clickBtn(page, 'premium-pricing-form-submit');

		await expect(page.getByText(domainName)).toBeVisible({ timeout: 10_000 });
		const row = page.locator('tr', { hasText: domainName });
		await row.locator('[data-testid^="premium-pricing-delete-"]').click();
		await page.getByRole('dialog').getByRole('button', { name: /delete/i }).click();
		await expect(page.getByText(domainName)).toHaveCount(0, { timeout: 10_000 });
	});

	test('create, edit and delete a premium length-tier pricing row', async ({ page }) => {
		const tld = uniqueDomainLabel();

		await page.goto('/admin/domains/pricing');
		await page.waitForLoadState('networkidle');
		await page.getByRole('tab', { name: 'Premium Length Pricing' }).click();
		await clickBtn(page, 'length-pricing-create-button');
		const form = page.getByTestId('length-pricing-form');
		await expect(form).toBeVisible();

		await form.locator('[name="tld"]').fill(tld);
		await form.locator('[name="char_length"]').fill('2');
		await form.locator('[name="price"]').fill('485000000');
		await clickBtn(page, 'length-pricing-form-submit');

		await expect(page.getByText(`.${tld}`)).toBeVisible({ timeout: 10_000 });
		const row = page.locator('tr', { hasText: `.${tld}` });
		await expect(row).toContainText('485.000.000');

		// Edit: change the price.
		await row.locator('[data-testid^="length-pricing-edit-"]').click();
		await expect(form).toBeVisible();
		await form.locator('[name="price"]').fill('500000000');
		await clickBtn(page, 'length-pricing-form-submit');
		await expect(row).toContainText('500.000.000', { timeout: 10_000 });

		// Delete.
		await row.locator('[data-testid^="length-pricing-delete-"]').click();
		await page.getByRole('dialog').getByRole('button', { name: /delete/i }).click();
		await expect(page.getByText(`.${tld}`)).toHaveCount(0, { timeout: 10_000 });
	});

	test('update a domain addon price and active flag', async ({ page }) => {
		await page.goto('/admin/domains/addons');
		await page.waitForLoadState('networkidle');
		const row = page.locator('[data-testid^="row-domain-addon-"]', { hasText: 'DNS Management' });
		await expect(row).toBeVisible();

		const priceInput = row.locator('[data-testid^="domain-addon-price-"]');
		const activeCheckbox = row.locator('[data-testid^="domain-addon-active-"]');
		const originalPrice = await priceInput.inputValue();
		const wasActive = await activeCheckbox.isChecked();

		await priceInput.fill('25000');
		if (!wasActive) await activeCheckbox.check();
		await saveAddonRow(page, row);

		await page.reload();
		const rowAfter = page.locator('[data-testid^="row-domain-addon-"]', { hasText: 'DNS Management' });
		await expect(rowAfter.locator('[data-testid^="domain-addon-price-"]')).toHaveValue('25000');
		await expect(rowAfter.locator('[data-testid^="domain-addon-active-"]')).toBeChecked();

		// Restore original state so other specs relying on this shared,
		// global 3-row catalog aren't affected.
		await rowAfter.locator('[data-testid^="domain-addon-price-"]').fill(originalPrice || '0');
		if (!wasActive) await rowAfter.locator('[data-testid^="domain-addon-active-"]').uncheck();
		await saveAddonRow(page, rowAfter);
	});

	test('settings overview lists Domain Pricing (not the Domain Registrations operational page)', async ({ page }) => {
		await page.goto('/admin/settings/overview');
		await page.waitForLoadState('networkidle');
		await page.getByPlaceholder(/search/i).fill('domain');

		await expect(page.getByRole('link', { name: 'Domain Pricing' })).toBeVisible();
		await expect(page.getByRole('link', { name: 'Domain Addons' })).toBeVisible();
		await expect(page.getByRole('link', { name: 'Domain Registrations' })).toHaveCount(0);
	});
});
