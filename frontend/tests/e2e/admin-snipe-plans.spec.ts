import { expect, test, type APIRequestContext } from './fixtures';
import {
	API_BASE,
	STAFF_EMAIL,
	STAFF_PASSWORD,
	adminToken,
	authHeaders,
	loginApi,
	newApi,
	registerVerifyLogin,
	setSessionCookies,
	unique
} from './helpers';

/**
 * Admin domain-snipe plans: the packages clients buy a hunt with. Admin-only
 * (staff are refused even with the domains permission) and repricing never
 * touches hunts that were already sold.
 */
test.describe('admin domain snipe plans', () => {
	let api: APIRequestContext;
	let token: string;
	const created: number[] = [];

	const adminApi = (path: string) => `${API_BASE}/api/v1/admin${path}`;

	/** Plans a client can currently buy, by name. */
	async function offeredNames(): Promise<string[]> {
		const client = await registerVerifyLogin(api, 'planviewer');
		const res = await api.get(`${API_BASE}/api/v1/snipe/plans`, {
			headers: authHeaders(client.accessToken)
		});
		expect(res.ok()).toBeTruthy();
		return ((await res.json()).data as Array<{ name: string }>).map((p) => p.name);
	}

	test.beforeAll(async () => {
		test.setTimeout(90_000);
		api = await newApi();
		token = await adminToken(api);
	});

	test.afterAll(async () => {
		for (const id of created) {
			await api
				.put(adminApi(`/snipe-plans/${id}`), {
					headers: authHeaders(token),
					data: {
						name: `retired-${id}`,
						window_days: 1,
						fee: 0,
						success_fee: 0,
						sort: 999,
						active: false
					}
				})
				.catch(() => {});
		}
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, token);
	});

	test('create, edit and deactivate a plan from the UI; clients only see active ones', async ({
		page
	}) => {
		test.setTimeout(120_000);
		const name = unique('plan');

		await page.goto('/admin/snipes/plans');
		await page.waitForLoadState('networkidle');
		await page.getByTestId('snipe-plan-add').click();
		const form = page.getByTestId('snipe-plan-form');
		await expect(form).toBeVisible();

		await page.getByTestId('snipe-plan-name').fill(name);
		await page.getByTestId('snipe-plan-days').fill('45');
		await page.getByTestId('snipe-plan-fee').fill('50000');
		await page.getByTestId('snipe-plan-success-fee').fill('25000');
		await page.getByTestId('snipe-plan-sort').fill('777');
		await page.getByTestId('snipe-plan-submit').click();

		const row = page.locator('tr', { hasText: name });
		await expect(row).toBeVisible({ timeout: 10_000 });
		await expect(row).toContainText('45 days');
		await expect(row).toContainText('Rp50.000,-');
		await expect(row).toContainText('Rp25.000,-');
		await expect(row.getByText('Active')).toBeVisible();
		const id = Number(
			((await row.getAttribute('data-testid')) ?? '').replace('row-snipe-plan-', '')
		);
		expect(id).toBeGreaterThan(0);
		created.push(id);
		expect(await offeredNames()).toContain(name);

		// Edit: the price changes, the sold hunts would not.
		await page.getByTestId(`snipe-plan-edit-${id}`).click();
		await expect(page.getByTestId('snipe-plan-name')).toHaveValue(name);
		await page.getByTestId('snipe-plan-fee').fill('75000');
		await page.getByTestId('snipe-plan-success-fee').fill('0');
		await page.getByTestId('snipe-plan-submit').click();
		await expect(row).toContainText('Rp75.000,-', { timeout: 10_000 });
		await expect(row).not.toContainText('Rp25.000,-');

		// Deactivate: gone from the client's choices, still listed for the admin.
		await page.getByTestId(`snipe-plan-toggle-${id}`).click();
		await expect(row.getByText('Inactive')).toBeVisible({ timeout: 10_000 });
		expect(await offeredNames()).not.toContain(name);
		await page.getByTestId(`snipe-plan-toggle-${id}`).click();
		await expect(row.getByText('Active')).toBeVisible({ timeout: 10_000 });
		expect(await offeredNames()).toContain(name);
	});

	test('the server rejects an invalid plan with a readable error', async ({ page }) => {
		test.setTimeout(90_000);
		await page.goto('/admin/snipes/plans');
		await page.waitForLoadState('networkidle');
		await page.getByTestId('snipe-plan-add').click();
		// Bypass the browser's own min/required checks so the SERVER's rules answer.
		await page
			.getByTestId('snipe-plan-form')
			.evaluate((f) => ((f as HTMLFormElement).noValidate = true));
		await page.getByTestId('snipe-plan-name').fill(unique('bad'));
		await page.getByTestId('snipe-plan-days').fill('0');
		await page.getByTestId('snipe-plan-submit').click();
		await expect(page.getByTestId('snipe-plan-error')).toContainText(/between 1 and 365/i);

		await page.getByTestId('snipe-plan-days').fill('30');
		await page.getByTestId('snipe-plan-name').fill('');
		await page.getByTestId('snipe-plan-submit').click();
		await expect(page.getByTestId('snipe-plan-error')).toContainText(/name is required/i);
	});

	test('the API validates plans and only admins may manage them', async () => {
		test.setTimeout(90_000);
		const post = (data: Record<string, unknown>) =>
			api.post(adminApi('/snipe-plans'), { headers: authHeaders(token), data });
		expect((await post({ name: '', window_days: 30 })).status()).toBe(422);
		expect((await post({ name: unique('x'), window_days: 0 })).status()).toBe(422);
		expect((await post({ name: unique('x'), window_days: 366 })).status()).toBe(422);
		expect((await post({ name: unique('x'), window_days: 30, fee: -1 })).status()).toBe(422);
		const missing = await api.put(adminApi('/snipe-plans/999999999'), {
			headers: authHeaders(token),
			data: { name: 'x', window_days: 30 }
		});
		expect(missing.status()).toBe(404);

		// Staff, even with the domains permission, cannot reprice the service.
		const staff = await loginApi(api, STAFF_EMAIL, STAFF_PASSWORD);
		expect(
			(
				await api.get(adminApi('/snipe-plans'), { headers: authHeaders(staff.accessToken) })
			).status()
		).toBe(403);
		const staffPost = await api.post(adminApi('/snipe-plans'), {
			headers: authHeaders(staff.accessToken),
			data: { name: unique('x'), window_days: 30 }
		});
		expect(staffPost.status()).toBe(403);
		// A client is refused too.
		const client = await registerVerifyLogin(api, 'plannosy');
		expect(
			(
				await api.get(adminApi('/snipe-plans'), { headers: authHeaders(client.accessToken) })
			).status()
		).toBe(403);
	});
});
