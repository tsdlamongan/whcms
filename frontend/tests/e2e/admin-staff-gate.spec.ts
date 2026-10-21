import { expect, test, type APIRequestContext } from './fixtures';
import {
	ADMIN_PASSWORD,
	API_BASE,
	adminToken,
	authHeaders,
	loginApi,
	ADMIN_EMAIL,
	newApi,
	setSessionCookies,
	unique
} from './helpers';

/**
 * Administrators page: the password gate is a real server-held confirmation
 * (POST/GET /auth/confirm-password), not a client-side decoration, and the
 * account edits behind it must not lose data.
 */

interface StaffUser {
	id: number;
	email: string;
	status: string;
	permissions: unknown;
}

function permsOf(u: StaffUser): Record<string, boolean> {
	const raw = u.permissions;
	return (typeof raw === 'string' ? JSON.parse(raw) : raw) as Record<string, boolean>;
}

test.describe('administrators page', () => {
	let api: APIRequestContext;
	let token: string;
	let staffId: number;
	let staffEmail: string;

	async function getStaff(): Promise<StaffUser> {
		const res = await api.get(`${API_BASE}/api/v1/admin/staff/${staffId}`, { headers: authHeaders(token) });
		expect(res.ok(), await res.text()).toBeTruthy();
		return (await res.json()).data as StaffUser;
	}

	test.beforeAll(async () => {
		test.setTimeout(60_000);
		api = await newApi();
		token = await adminToken(api); // a fresh login also closes any earlier confirmation window

		staffEmail = `${unique('gate-staff')}@e2e.test`;
		const res = await api.post(`${API_BASE}/api/v1/admin/staff`, {
			headers: authHeaders(token),
			// `registrars` is an admin-only key the staff form does not show: it
			// must survive an edit made through the UI.
			data: { email: staffEmail, password: 'StaffGate!2026', status: 'active', permissions: { clients: true, registrars: true } }
		});
		expect(res.ok(), `create staff: ${await res.text()}`).toBeTruthy();
		staffId = ((await res.json()).data as StaffUser).id;
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, token);
	});

	test('a wrong password keeps the list locked, the right one unlocks it, a new login re-locks it', async ({ page }) => {
		await page.goto('/admin/staff');
		await expect(page.getByTestId('staff-gate')).toBeVisible();
		await expect(page.getByTestId('staff-table')).toHaveCount(0);

		// Any non-empty text used to unlock the page; now the API must accept it.
		await page.getByTestId('staff-gate-password').fill('definitely-not-my-password');
		await page.getByTestId('staff-gate-confirm').click();
		await expect(page.getByTestId('staff-gate-error')).toContainText(/incorrect/i);
		await expect(page.getByTestId('staff-table')).toHaveCount(0);

		await page.getByTestId('staff-gate-password').fill(ADMIN_PASSWORD);
		await page.getByTestId('staff-gate-confirm').click();
		await expect(page.getByTestId('staff-table')).toBeVisible();
		await expect(page.getByTestId(`staff-row-${staffId}`)).toBeVisible();

		// The window is held server-side, so a reload stays unlocked...
		await page.reload();
		await expect(page.getByTestId('staff-table')).toBeVisible();

		// ...until the user signs in again (a new session must re-confirm).
		await loginApi(api, ADMIN_EMAIL, ADMIN_PASSWORD);
		await page.reload();
		await expect(page.getByTestId('staff-gate')).toBeVisible();
		await expect(page.getByTestId('staff-table')).toHaveCount(0);
	});

	test('editing keeps unseen permission keys, toggles Transactions & Payments, then deactivates', async ({ page }) => {
		await page.goto('/admin/staff');
		await page.getByTestId('staff-gate-password').fill(ADMIN_PASSWORD);
		await page.getByTestId('staff-gate-confirm').click();
		await expect(page.getByTestId(`staff-edit-${staffId}`)).toBeVisible();

		await page.getByTestId(`staff-edit-${staffId}`).click();
		const payments = page.getByTestId('staff-permission-payments');
		await expect(payments).toBeVisible();
		await expect(payments).not.toBeChecked();
		await expect(page.getByTestId('staff-permission-clients')).toBeChecked();
		await payments.check();
		await page.getByTestId('staff-save').click();

		await expect
			.poll(async () => permsOf(await getStaff()).payments, { message: 'payments permission saved', timeout: 15_000 })
			.toBe(true);
		const saved = permsOf(await getStaff());
		expect(saved.clients).toBe(true);
		expect(saved.registrars, 'admin-only keys the form does not show must be preserved').toBe(true);

		// Accounts are never hard-deleted: "Deactivate" flips status to inactive.
		await page.getByTestId(`staff-deactivate-${staffId}`).click();
		// ConfirmDialog's confirm button is the last button in its footer.
		await page.getByRole('dialog').locator('button').last().click();
		await expect
			.poll(async () => (await getStaff()).status, { message: 'staff deactivated', timeout: 15_000 })
			.toBe('inactive');
		expect(permsOf(await getStaff()).payments, 'deactivating must not touch permissions').toBe(true);
	});
});
