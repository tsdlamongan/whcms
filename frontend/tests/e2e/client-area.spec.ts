import { expect, test, type APIRequestContext } from './fixtures';
import {
	createOrder,
	newApi,
	payInvoiceViaMock,
	pollListStatus,
	seededHostingProductId,
	setSessionCookies,
	registerVerifyLogin,
	uniqueDomainLabel,
	type Client
} from './helpers';

/**
 * Client area read surfaces (dashboard, services/domains/invoices lists) plus
 * the add-funds deposit flow. One verified client with an active service +
 * registered domain + paid invoices is set up once via the API.
 */

test.describe('client area', () => {
	let api: APIRequestContext;
	let client: Client;
	let serviceDomain: string;
	let registeredDomain: string;

	test.beforeAll(async () => {
		test.setTimeout(180_000);
		api = await newApi();
		client = await registerVerifyLogin(api, 'clientarea');
		const productId = await seededHostingProductId(api);

		serviceDomain = `${uniqueDomainLabel()}.hosting.e2e.test`;
		const svcInvoice = await createOrder(api, client.accessToken, [
			{ item_type: 'product', product_id: productId, cycle: 'monthly', domain: serviceDomain }
		]);
		await payInvoiceViaMock(api, client.accessToken, svcInvoice);
		await pollListStatus(api, client.accessToken, '/api/v1/services', (r) => r.domain === serviceDomain, 'active');

		registeredDomain = `${uniqueDomainLabel()}.e2e.test`;
		const domInvoice = await createOrder(api, client.accessToken, [
			{ item_type: 'domain_register', domain: registeredDomain, domain_years: 1 }
		]);
		await payInvoiceViaMock(api, client.accessToken, domInvoice);
		await pollListStatus(api, client.accessToken, '/api/v1/domains', (r) => r.name === registeredDomain, 'active');
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, client.accessToken, client.refreshToken);
	});

	test('dashboard shows the client stat cards and recent invoices', async ({ page }) => {
		await page.goto('/dashboard');
		await expect(page.getByTestId('dashboard-welcome')).toBeVisible();
		await expect(page.getByTestId('stat-active-services')).toBeVisible();
		await expect(page.getByTestId('stat-domains')).toBeVisible();
		await expect(page.getByTestId('stat-unpaid-invoices')).toBeVisible();
		await expect(page.getByTestId('stat-open-tickets')).toBeVisible();
		await expect(page.getByTestId('recent-invoices')).toBeVisible();
		await expect(page.getByTestId('quick-links')).toBeVisible();
		// The client has exactly one active service.
		await expect(page.getByTestId('stat-active-services')).toContainText('1');
	});

	test('services list shows the active service and filters by status', async ({ page }) => {
		await page.goto('/services');
		await expect(page.getByText(serviceDomain)).toBeVisible();
		// The row must show the real product name (ServiceView enrichment),
		// not the "Produk #<id>" fallback.
		await expect(page.getByText('E2E Shared Hosting').first()).toBeVisible();

		await page.getByTestId('services-status-filter').selectOption('active');
		await page.getByTestId('services-filter-submit').click();
		await expect(page.getByText(serviceDomain)).toBeVisible();
	});

	test('domains list shows the registered domain', async ({ page }) => {
		await page.goto('/domains');
		await expect(page.getByTestId('domains-table')).toBeVisible();
		await expect(page.getByText(registeredDomain)).toBeVisible();
	});

	test('billing invoices list shows the paid invoices', async ({ page }) => {
		await page.goto('/billing');
		// At least one invoice row from the hosting + domain orders.
		await expect(page.locator('[data-testid^="row-invoice-"]').first()).toBeVisible();
		await expect(page.getByTestId('invoice-filter-submit')).toBeVisible();
	});

	test('deposit (add funds) creates an invoice', async ({ page }) => {
		await page.goto('/billing/deposit');
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('deposit-form')).toBeVisible();
		await page.getByTestId('deposit-amount-input').locator('input').fill('100000');
		await page.getByTestId('deposit-submit').locator('button').click();
		// Success redirects to the generated deposit invoice.
		await page.waitForURL(/\/billing\/invoices\/\d+/, { timeout: 15_000 });
		await expect(page).toHaveURL(/\/billing\/invoices\/\d+/);
	});
});
