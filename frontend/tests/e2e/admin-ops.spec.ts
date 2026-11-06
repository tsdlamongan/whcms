import { expect, test, waitForHydration, type APIRequestContext } from './fixtures';
import { adminToken, sleep } from './helpers';

/**
 * Flow 7 (admin-ops), PRD §13.2 / docs/E2E.md §5.7:
 *   1. Log in as the seeded admin via the UI.
 *   2. Create a NEW product from the admin UI.
 *   3. Perform a manual provisioning action on an existing service (suspend
 *      then unsuspend) from the admin services page.
 *   4. View the admin dashboard and confirm the KPIs render without error.
 *   5. View the revenue report page without error.
 *
 * The "existing service" fixture is built directly against the live backend
 * API (register -> verify -> order -> admin manual payment -> poll for
 * activation) rather than through the full client checkout UI, because that
 * checkout journey is Flow 2's job - this spec only needs an ACTIVE service
 * to exist so the admin suspend/unsuspend action has something to act on.
 * All test data (email, domain, product name) is timestamp+random suffixed
 * so this spec is safe to run concurrently with the other 7 flows against
 * the same shared backend/DB.
 */

const API_BASE = 'http://localhost:8080';
const MOCK_BASE = 'http://localhost:9090';

const ADMIN_EMAIL = 'admin@e2e.test';
const ADMIN_PASSWORD = 'AdminE2E!2026';

// The seed's hosting product (backend/cmd/seed): slug e2e-shared-hosting,
// module=cpanel, auto_setup=on_payment, monthly 50000 IDR.
const HOSTING_PRODUCT_SLUG = 'e2e-shared-hosting';

interface Envelope<T> {
	data: T | null;
	meta: unknown;
	error: { code: string; message: string } | null;
}

async function apiPost<T>(
	request: APIRequestContext,
	path: string,
	body: unknown,
	token?: string
): Promise<T> {
	// /auth/* endpoints (register/login/verify-email) share one IP-keyed
	// fixed-window rate-limit bucket (30 req/min,
	// backend/internal/modules/auth/handler.go publicLimit) with every other
	// concurrently-running E2E spec on this machine. Retry with backoff on a
	// transient RATE_LIMITED instead of failing outright for those paths only
	// - cross-test interference, not a bug in this flow.
	const isAuthPath = path.startsWith('/api/v1/auth/');
	const maxAttempts = isAuthPath ? 25 : 1;

	let env: Envelope<T> | undefined;
	let status = 0;
	for (let attempt = 1; attempt <= maxAttempts; attempt++) {
		const res = await request.post(`${API_BASE}${path}`, {
			data: body,
			headers: token ? { Authorization: `Bearer ${token}` } : undefined
		});
		status = res.status();
		env = (await res.json()) as Envelope<T>;
		if (env.error?.code !== 'RATE_LIMITED' || attempt === maxAttempts) break;
		await sleep(3000);
	}
	if (!env || status < 200 || status >= 300 || env.error || env.data === null) {
		throw new Error(`POST ${path} failed: ${status} ${JSON.stringify(env?.error ?? env)}`);
	}
	return env.data;
}

async function apiGet<T>(request: APIRequestContext, path: string, token?: string): Promise<T> {
	const res = await request.get(`${API_BASE}${path}`, {
		headers: token ? { Authorization: `Bearer ${token}` } : undefined
	});
	const env = (await res.json()) as Envelope<T>;
	if (!res.ok() || env.error) {
		throw new Error(`GET ${path} failed: ${res.status()} ${JSON.stringify(env.error ?? env)}`);
	}
	return env.data as T;
}

interface MailMessage {
	to: string;
	text: string;
}

interface ServiceRow {
	id: number;
	status: string;
	domain: string;
}

interface AdminProductRow {
	id: number;
	slug: string;
}

/**
 * Build the "existing active service" fixture via the real API:
 * register a fresh client, verify its email via the mock mail sink, order
 * the seeded hosting product (with a domain - cPanel's createacct requires
 * one), then have the admin record a manual payment so billing.ProcessPaid
 * enqueues provisioning; finally poll until the worker has activated it.
 */
async function createActiveService(
	request: APIRequestContext,
	suffix: string,
	adminAccessToken: string
): Promise<number> {
	const email = `e2e-adminops-${suffix}@example.test`;
	const password = 'AdminOpsE2E!123';
	// domain.UsernameFromDomain() (backend/internal/domain/generate.go) derives
	// the cPanel account username from everything before the domain's first
	// dot, alnum-stripped and truncated to 8 chars - so the first label must
	// itself be <=8 chars and unique, or repeat runs collide on the mock WHM
	// server's in-memory account list ("createacct: account exists"). A
	// letter + the last 7 digits of Date.now() fits exactly in 8 chars.
	const shortId = `t${String(Date.now()).slice(-7)}`;
	const domainName = `${shortId}.e2e-adminops.test`;

	await apiPost(request, '/api/v1/auth/register', {
		email,
		password,
		captcha_token: 'e2e-dummy-captcha-token',
		first_name: 'AdminOps',
		last_name: 'Fixture',
		address1: 'Jl. Test No. 1',
		city: 'Jakarta',
		state: 'DKI Jakarta',
		postcode: '12345',
		country: 'ID',
		phone: '081200000000'
	});

	// The verify-email notification is fire-and-forget on the backend side -
	// poll the mock mail sink until it shows up, then pull the token out of
	// the emailed link (?token=...).
	let verifyToken = '';
	await expect
		.poll(
			async () => {
				const res = await request.get(`${MOCK_BASE}/mail/messages?to=${encodeURIComponent(email)}`);
				const mails = (await res.json()) as MailMessage[];
				const match = mails[0]?.text.match(/token=([^\s&"]+)/);
				verifyToken = match?.[1] ?? '';
				return verifyToken !== '';
			},
			{ timeout: 15_000, message: 'waiting for verify-email mail' }
		)
		.toBe(true);

	await apiPost(request, '/api/v1/auth/verify-email', { token: verifyToken });

	const clientLogin = await apiPost<{ access_token: string }>(request, '/api/v1/auth/login', {
		email,
		password,
		captcha_token: 'e2e-dummy-captcha-token'
	});

	const products = await apiGet<AdminProductRow[]>(
		request,
		'/api/v1/admin/products?per_page=100',
		adminAccessToken
	);
	const hostingProduct = products.find((p) => p.slug === HOSTING_PRODUCT_SLUG);
	if (!hostingProduct) throw new Error(`seeded product ${HOSTING_PRODUCT_SLUG} not found`);

	const order = await apiPost<{
		order: { id: number };
		invoice: { id: number; total: number };
	}>(
		request,
		'/api/v1/orders',
		{
			captcha_token: 'e2e-dummy-captcha-token',
			items: [
				{
					item_type: 'product',
					product_id: hostingProduct.id,
					cycle: 'monthly',
					domain: domainName
				}
			]
		},
		clientLogin.access_token
	);

	await apiPost(
		request,
		`/api/v1/admin/invoices/${order.invoice.id}/payment`,
		{ amount: order.invoice.total, method: 'manual' },
		adminAccessToken
	);

	// Poll (the worker processes provision:create asynchronously) until active.
	let serviceId = 0;
	await expect
		.poll(
			async () => {
				const services = await apiGet<ServiceRow[]>(
					request,
					'/api/v1/services?per_page=100',
					clientLogin.access_token
				);
				const row = services.find((s) => s.domain === domainName);
				if (row?.status === 'active') serviceId = row.id;
				return row?.status ?? 'missing';
			},
			{ timeout: 45_000, message: 'waiting for service to become active' }
		)
		.toBe('active');

	return serviceId;
}

test.describe('admin-ops (Flow 7)', () => {
	test.setTimeout(220_000);

	test('admin creates a product, suspends/unsuspends a service, and views dashboard + revenue report', async ({
		page,
		request
	}) => {
		const suffix = `${Date.now()}${Math.floor(Math.random() * 1000)}`;

		// Single admin login reused for every API setup call below - the
		// backend's /auth/* rate limiter (30 req/min) is shared across the
		// whole test, so avoid logging in as admin more than once here.
		const adminAccessToken = await adminToken(request);

		// --- Fixture: an existing ACTIVE service to act on later --------------
		const serviceId = await createActiveService(request, suffix, adminAccessToken);

		// --- 1. Log in as the seeded admin via the UI -------------------------
		// The UI login hits /auth/login (the same shared 30/min IP-keyed
		// bucket every other concurrently-running spec draws from), so retry
		// a few times across more than one fixed window before failing.
		let adminLoggedIn = false;
		for (let attempt = 1; attempt <= 8 && !adminLoggedIn; attempt++) {
			await page.goto('/login');
			// SvelteKit hydration race: filling before client JS hydration wires
			// up bind:value can get silently wiped by the hydration effect
			// snapping the field back to its initial empty state - widens under
			// concurrent-suite load, so always wait for network-idle first.
			await page.waitForLoadState('networkidle');
			await page.locator('input[name="email"]').fill(ADMIN_EMAIL);
			await page.locator('input[name="password"]').fill(ADMIN_PASSWORD);
			await page.locator('button[type="submit"]').click();
			try {
				await expect(page).toHaveURL(/\/admin$/, { timeout: 20_000 });
				adminLoggedIn = true;
			} catch (err) {
				if (attempt === 8) throw err;
			}
		}

		// --- 2. Create a NEW product from the admin UI ------------------------
		await page.goto('/admin/products/new');
		// SvelteKit SSR-renders the form immediately, but client hydration
		// (which wires up the submit button's handler and each field's
		// bind:value) finishes slightly later - under concurrent-suite load
		// this can widen enough that a fast fill+click sequence lands before
		// hydration attaches the submit handler, so the click silently no-ops
		// and the page never navigates. Wait for network-idle first.
		await page.waitForLoadState('networkidle');
		await expect(page.locator('[data-testid="product-form"]')).toBeVisible();

		const productName = `E2E Admin Product ${suffix}`;
		await page.locator('#field-name').fill(productName);
		// Slug auto-derives from name via the form's own $effect; leave it.
		await page.locator('#field-group_id').selectOption({ index: 1 });

		// --- 2b. Module tab: package picker loaded live from the mock WHM -----
		// (2nd tab: details, module, pricing) - covers the "Load from Server"
		// dropdown and the Configurable checkbox's auto-generated-package note.
		await page.locator('[data-testid="product-form-tabs"] [role="tab"]').nth(1).click();
		await page.locator('#field-module').selectOption('cpanel');
		await page.locator('#field-server_group_id').selectOption({ label: 'E2E cPanel Group' });

		await page.locator('[data-testid="product-package-load-button"]').click();
		const packageSelect = page.locator('[data-testid="product-package-select"]');
		await expect(packageSelect).toBeVisible();
		await expect(packageSelect.locator('option[value="default"]')).toHaveCount(1);
		await packageSelect.selectOption('default');
		await expect(page.locator('#field-package_name')).toHaveValue('default');

		// Configurable ignores package_name entirely - the field becomes a
		// disabled "auto-generated" note instead of a free-text input.
		await page.locator('#field-configurable').check();
		const packageField = page.locator('#field-package_name');
		await expect(packageField).toBeDisabled();
		await expect(packageField).toHaveValue('Auto-generated per service');
		await expect(packageSelect).toBeHidden();
		await page.locator('#field-configurable').uncheck();
		await expect(page.locator('#field-package_name')).toHaveValue('default');

		// Leave Configurable on for this product so the Dynamic Specs section
		// (§2c below) is reachable - package_name stays set but is ignored.
		await page.locator('#field-configurable').check();

		// --- 2b-i. Package options: shell/CGI toggles + panel-specific field --
		// Shell/CGI Access are universal, shown for any configurable product;
		// WHM Feature List is cpanel-only and Template Package directadmin-only
		// - switching the module swaps one for the other.
		await page.locator('#field-shell_access').check();
		await page.locator('#field-cgi_access').check();
		await expect(page.locator('[data-testid="product-feature-list-field"]')).toBeVisible();
		await expect(page.locator('[data-testid="product-template-package-field"]')).toBeHidden();
		await page.locator('#field-feature_list').fill('e2e_features');

		await page.locator('#field-module').selectOption('directadmin');
		await expect(page.locator('[data-testid="product-template-package-field"]')).toBeVisible();
		await expect(page.locator('[data-testid="product-feature-list-field"]')).toBeHidden();
		// Shell/CGI stay checked across the module switch - only the
		// panel-specific field swaps.
		await expect(page.locator('#field-shell_access')).toBeChecked();
		await expect(page.locator('#field-cgi_access')).toBeChecked();

		// Switch back to cpanel before submitting - this product provisions
		// against the seeded cPanel mock group selected above.
		await page.locator('#field-module').selectOption('cpanel');
		await expect(page.locator('[data-testid="product-feature-list-field"]')).toBeVisible();
		await expect(page.locator('#field-feature_list')).toHaveValue('e2e_features');

		// Switch to the "Pricing" tab (3rd tab: details, module, pricing).
		await page.locator('[data-testid="product-form-tabs"] [role="tab"]').nth(2).click();
		const monthlyEnabled = page.locator('[data-testid="cycle-monthly-enabled"]');
		await monthlyEnabled.waitFor({ state: 'visible' });
		if (!(await monthlyEnabled.isChecked())) await monthlyEnabled.check();
		await page.locator('[data-testid="price-monthly-input"]').fill('75000');
		await page.locator('[data-testid="setup-monthly-input"]').fill('0');

		// Retry the click itself: under concurrent-suite load a click that lands
		// just before hydration attaches the submit handler silently no-ops
		// (passes Playwright's actionability checks but nothing happens), so
		// re-click if the page hasn't navigated away after a short wait.
		const submitBtn = page.locator('[data-testid="product-form-submit"] button');
		for (let attempt = 1; attempt <= 3; attempt++) {
			await submitBtn.click();
			try {
				await expect(page).toHaveURL(/\/admin\/products\/\d+\?created=1/, { timeout: 5_000 });
				break;
			} catch (err) {
				if (attempt === 3) throw err;
			}
		}
		await expect(page.locator('[data-testid="product-created-alert"]')).toBeVisible();
		await expect(page.locator('[data-testid="product-title"]')).toHaveText(productName);

		// --- 2c. Dynamic Specs: add a spec knob via the real admin form -------
		// Regression coverage: a freshly-created spec has no pricing rows yet,
		// and the API returns pricing: null for that case - the product page's
		// own reload after creating it must not 500 (see SpecsEditor's
		// `row.pricing.find(...)` / the load's `pricing: row.pricing ?? []`).
		await page.locator('#field-spec-key').fill('disk');
		await page.locator('#field-spec-label').fill('Disk Space');
		await page.locator('#field-spec-min_qty').fill('10');
		await page.locator('#field-spec-max_qty').fill('100');
		await page.locator('#field-spec-default_qty').fill('10');
		await page.locator('[data-testid="admin-spec-submit"]').click();
		await expect(page.getByText('Disk Space')).toBeVisible();
		await expect(page.getByText('min 10 · max 100 · step 1 · included 0 · default 10')).toBeVisible();

		// Editing an existing spec (e.g. its "Allow unlimited" flag) had no UI at
		// all before - only create (above) and delete were wired up, even though
		// the backend's updateSpec action already worked. The edit form is the
		// exact same table as "Add a spec knob", swapped in inline in place of
		// the read-only summary. Drive it end to end and confirm the change
		// persists after a reload.
		// If either submit above fell back to a native post (its click raced
		// hydration), the browser loaded a FRESH document: wait for that
		// document's own hydration before driving the pure-JS edit toggle.
		await waitForHydration(page);
		await page.locator('[data-testid^="spec-edit-"]').click();
		const specEditForm = page.locator('[data-testid^="admin-spec-edit-form-"]');
		await expect(specEditForm).toBeVisible();
		await specEditForm.locator('input[name="allow_unlimited"]').check();
		await specEditForm.locator('[data-testid^="admin-spec-edit-submit-"]').click();
		await expect(specEditForm).toBeHidden();
		await expect(page.getByText('min 10 · max 100 · step 1 · included 0 · default 10 · unlimited allowed')).toBeVisible();
		await page.reload();
		await page.waitForLoadState('networkidle');
		await expect(page.getByText('min 10 · max 100 · step 1 · included 0 · default 10 · unlimited allowed')).toBeVisible();

		// --- 2d. Duplicate Product: clone (pricing + specs) into a new hidden
		// product from the edit page's "Duplicate Product" button -------------
		// Retry the click itself: same hydration race as the other buttons in
		// this spec (a click landing just before hydration attaches the form's
		// handler silently no-ops).
		const duplicateButton = page.locator('[data-testid="product-duplicate-button"]');
		for (let attempt = 1; attempt <= 3; attempt++) {
			await duplicateButton.click();
			try {
				await expect(page).toHaveURL(/\/admin\/products\/\d+\?duplicated=1/, { timeout: 5_000 });
				break;
			} catch (err) {
				if (attempt === 3) throw err;
			}
		}
		await expect(page.locator('[data-testid="product-duplicated-alert"]')).toBeVisible();
		await expect(page.locator('[data-testid="product-title"]')).toHaveText(`${productName} (Copy)`);
		await expect(page.locator('#field-hidden')).toBeChecked();

		await page.locator('[data-testid="product-form-tabs"] [role="tab"]').nth(2).click();
		await expect(page.locator('[data-testid="price-monthly-input"]')).toHaveValue('75000');
		await expect(
			page.getByText('min 10 · max 100 · step 1 · included 0 · default 10 · unlimited allowed')
		).toBeVisible();

		// --- 3. Manual provisioning action: suspend then unsuspend -----------
		await page.goto(`/admin/services/${serviceId}`);
		// SvelteKit hydration race: a click can silently no-op if it lands
		// before client JS finishes hydrating and wiring up the button's
		// handler - widens under concurrent-suite load, so wait first.
		await page.waitForLoadState('networkidle');
		const suspendAction = page.locator('[data-testid="service-action-suspend"]');
		await expect(suspendAction).toBeVisible();

		// Retry the click that opens the suspend modal in case it lands just
		// before hydration attaches the handler (see comment above).
		const reasonField = page.locator('#field-reason');
		for (let attempt = 1; attempt <= 3; attempt++) {
			await suspendAction.locator('button').click();
			try {
				await reasonField.waitFor({ state: 'visible', timeout: 5_000 });
				break;
			} catch (err) {
				if (attempt === 3) throw err;
			}
		}
		await reasonField.fill('E2E admin-ops flow: manual suspend test');
		await page.locator('[data-testid="service-suspend-submit"] button').click();

		await expect(page.locator('[data-testid="service-action-unsuspend"]')).toBeVisible({
			timeout: 20_000
		});
		await expect(page.locator('[data-testid="service-suspend-reason-text"]')).toContainText(
			'E2E admin-ops flow'
		);

		await page.locator('[data-testid="service-action-unsuspend"] button').click();
		// ConfirmDialog's confirm button is the last button in its footer
		// (cancel, then confirm) - locale-agnostic instead of matching text.
		await page.getByRole('dialog').locator('button').last().click();

		await expect(page.locator('[data-testid="service-action-suspend"]')).toBeVisible({
			timeout: 20_000
		});

		// --- 4. Admin dashboard: KPIs render without error --------------------
		await page.goto('/admin');
		await expect(page.locator('[data-testid="dashboard-error"]')).toHaveCount(0);
		await expect(page.locator('[data-testid="stat-income-today"]')).toBeVisible();
		await expect(page.locator('[data-testid="stat-active-services"]')).toBeVisible();
		await expect(page.locator('[data-testid="stat-overdue-invoices"]')).toBeVisible();
		await expect(page.locator('[data-testid="stat-module-actions-pending"]')).toBeVisible();
		await expect(page.locator('[data-testid="recent-orders"]')).toBeVisible();

		// --- 5. Revenue report page renders without error ---------------------
		await page.goto('/admin/reports/revenue');
		await expect(page.locator('[data-testid="revenue-total"]')).toBeVisible();
		await expect(page.locator('[data-testid="revenue-chart"]')).toBeVisible();
		await expect(page.locator('[data-testid="revenue-totals-table"]')).toBeVisible();
	});
});
