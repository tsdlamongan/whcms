import { expect, test, type APIRequestContext } from './fixtures';
import {
	API_BASE,
	adminFindClientId,
	adminToken,
	authHeaders,
	newApi,
	registerVerifyLogin,
	setSessionCookies,
	unique,
	uniqueDomainLabel
} from './helpers';

/**
 * Admin "add existing" flows - /admin/services/new and /admin/domains/new.
 *
 * An admin records hosting/domains that were provisioned or registered
 * BEFORE this app existed (e.g. accounts already living on a cPanel box, a
 * domain already registered at the registrar), so they can be billed and
 * managed here - with NO order, NO payment, and NO panel/registrar call.
 * The created rows must land directly in the Active status.
 *
 * All test data is unique-suffixed so this spec is safe to run concurrently
 * with the other flows against the same shared backend/DB.
 */

test.describe('admin adds existing service and domain', () => {
	let api: APIRequestContext;
	let token: string;
	let clientId: number;

	test.beforeAll(async () => {
		// beforeAll has its own 30s default timeout and registerVerifyLogin/
		// adminToken share the /auth/* rate-limit bucket with concurrent specs.
		test.setTimeout(120_000);
		api = await newApi();
		token = await adminToken(api);
		const client = await registerVerifyLogin(api, 'import');
		clientId = await adminFindClientId(api, token, client.email);
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, token);
	});

	test('admin records a pre-existing hosting service (no order, no provisioning)', async ({
		page
	}) => {
		test.setTimeout(60_000);

		// The seeded mock cPanel server, so the service gets a real panel binding.
		const serversRes = await api.get(`${API_BASE}/api/v1/admin/servers`, {
			headers: authHeaders(token),
			params: { per_page: 100 }
		});
		expect(serversRes.ok()).toBeTruthy();
		const servers = ((await serversRes.json()).data ?? []) as Array<{
			id: number;
			module: string;
			hostname: string;
		}>;
		const mockServer = servers.find((s) => s.module === 'cpanel' && s.hostname === 'localhost');
		expect(mockServer, 'seeded cPanel mock server must exist').toBeTruthy();

		const domain = `${uniqueDomainLabel()}-import.example`;

		// Entry point: the services list "Add Existing Service" button.
		await page.goto('/admin/services');
		await page.waitForLoadState('networkidle');
		await page.getByTestId('service-add-new').click();
		await expect(page).toHaveURL(/\/admin\/services\/new$/);
		await expect(page.getByTestId('service-create-form')).toBeVisible();

		// Error path first: a recurring cycle without a next due date must be
		// rejected with a visible error (renewal invoicing depends on it).
		await page.locator('#field-client_id').selectOption(String(clientId));
		// <option> elements never count as "visible" to Playwright - wait attached.
		await page
			.getByTestId('service-create-product')
			.locator('option')
			.nth(1)
			.waitFor({ state: 'attached' });
		const productValue = await page
			.getByTestId('service-create-product')
			.locator('option')
			.nth(1)
			.getAttribute('value');
		await page.locator('#field-product_id').selectOption(productValue!);
		await page.getByTestId('service-create-submit').click();
		await expect(page.getByTestId('service-create-error')).toBeVisible();

		// Happy path: full form.
		await page.locator('#field-client_id').selectOption(String(clientId));
		await page.locator('#field-product_id').selectOption(productValue!);
		await page.locator('#field-server_id').selectOption(String(mockServer!.id));
		await page.locator('#field-domain').fill(domain);
		await page.locator('#field-username').fill('importeduser');
		await page.locator('#field-billing_cycle').selectOption('monthly');
		await page.locator('#field-recurring_amount').fill('150000');
		await page.locator('#field-next_due_date').fill('2027-01-01');
		await page.getByTestId('service-create-submit').click();

		// Redirects to the new service's detail page, already ACTIVE - no
		// pending/provisioning step ever happened.
		await expect(page).toHaveURL(/\/admin\/services\/\d+$/, { timeout: 15_000 });
		await expect(page.getByTestId('service-status')).toHaveText(/active/i);
		await expect(page.getByText(domain).first()).toBeVisible();
	});

	test('admin records a pre-existing domain and edits its billing fields', async ({ page }) => {
		test.setTimeout(60_000);

		const name = `${uniqueDomainLabel()}-${unique('dom')}.com`.toLowerCase();

		// Entry point: the domains list "Add Existing Domain" button.
		await page.goto('/admin/domains');
		await page.waitForLoadState('networkidle');
		await page.getByTestId('domain-add-new').click();
		await expect(page).toHaveURL(/\/admin\/domains\/new$/);
		await expect(page.getByTestId('domain-create-form')).toBeVisible();

		// Error path: a single nameserver is rejected (2-4 required when set).
		await page.locator('#field-client_id').selectOption(String(clientId));
		await page.locator('#field-name').fill(name);
		await page.locator('#field-next_due_date').fill('2027-03-01');
		await page.locator('#field-ns1').fill('ns1.example.net');
		await page.getByTestId('domain-create-submit').click();
		await expect(page.getByTestId('domain-create-error')).toBeVisible();

		// Happy path: complete the form.
		await page.locator('#field-client_id').selectOption(String(clientId));
		await page.locator('#field-name').fill(name);
		await page.locator('#field-registration_date').fill('2024-03-01');
		await page.locator('#field-expiry_date').fill('2027-03-01');
		await page.locator('#field-next_due_date').fill('2027-03-01');
		await page.locator('#field-recurring_amount').fill('180000');
		await page.locator('#field-ns2').fill('ns2.example.net');
		await page.getByTestId('domain-create-submit').click();

		// Redirects to the new domain's detail page, already ACTIVE.
		await expect(page).toHaveURL(/\/admin\/domains\/\d+$/, { timeout: 15_000 });
		await expect(page.getByTestId('domain-status')).toHaveText(/active/i);
		await expect(page.getByText(name).first()).toBeVisible();

		// Billing fields are admin-editable on the settings form (needed to
		// keep a manually-recorded domain billable/correct over time).
		await page.getByTestId('domain-settings-amount').fill('275000');
		await page.getByTestId('domain-settings-next-due').fill('2027-04-01');
		await page.getByTestId('domain-settings-submit').locator('button').click();
		await expect(page.getByText('Action completed successfully').first()).toBeVisible({
			timeout: 10_000
		});

		await page.reload();
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('domain-settings-amount')).toHaveValue('275000');
		await expect(page.getByTestId('domain-settings-next-due')).toHaveValue('2027-04-01');
	});
});
