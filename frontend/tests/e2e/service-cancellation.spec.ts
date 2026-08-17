import { expect, test, type APIRequestContext } from './fixtures';
import {
	ADMIN_EMAIL,
	ADMIN_PASSWORD,
	authHeaders,
	clientWithActiveService,
	loginApi,
	newApi,
	setSessionCookies,
	type Client
} from './helpers';

/**
 * Service cancellation-request lifecycle (client submit -> admin review),
 * added 2026-08-17 after the "Ajukan Pembatalan" button was found to have no
 * admin-side surface at all: an end_of_term request used to just silently
 * flag panel_meta.cancel_at_period_end with zero visibility. Now:
 *   - immediate requests still process automatically (no approval gate) and
 *     start pending, resolved to "auto_processed" only once the worker
 *     actually completes the termination job (NOT synchronously at
 *     submission - see the 2026-08-18 fix below) - recorded for history;
 *   - end_of_term requests start "pending" and only take effect once an
 *     admin accepts them via the new /admin/services/cancellation-requests
 *     list (or are rejected, leaving the service untouched).
 *
 * 2026-08-18 fix: immediate mode used to mark its request "auto_processed"
 * at submission time, before the async termination job actually ran - so a
 * client could submit "immediate" repeatedly while the job sat in the queue
 * (the service stays active until the worker gets to it), each click
 * enqueuing a duplicate termination job. Immediate now starts "pending" too,
 * so the existing "one pending request per service" guard blocks a second
 * submission until the worker resolves the first one - the "admin sees the
 * immediate request auto-processed" test below polls for that resolution
 * instead of asserting it synchronously.
 *
 * Each of the three service fixtures (accept/reject/immediate) is built
 * directly against the live backend API (register -> verify -> order -> pay
 * -> poll active) via the shared clientWithActiveService helper, since
 * ordering itself is covered by other specs - this spec only needs an ACTIVE
 * service per flow. All test data is timestamp+random suffixed so this spec
 * is safe under concurrency against the shared backend + DB.
 */

const API_BASE = 'http://localhost:8080';

test.describe.configure({ mode: 'serial', timeout: 90_000 });

test.describe('service cancellation: client request + admin review', () => {
	let api: APIRequestContext;
	let adminAccessToken = '';
	let adminRefreshToken = '';

	let acceptClient: Client;
	let acceptServiceId = 0;
	let acceptDomain = '';

	let rejectClient: Client;
	let rejectServiceId = 0;
	let rejectDomain = '';

	let immediateClient: Client;
	let immediateServiceId = 0;
	let immediateDomain = '';

	test.beforeAll(async () => {
		// Three full register->order->pay->poll-active round trips against a
		// live, possibly-shared/busy backend + async worker - give it room.
		test.setTimeout(240_000);
		api = await newApi();

		const admin = await loginApi(api, ADMIN_EMAIL, ADMIN_PASSWORD);
		adminAccessToken = admin.accessToken;
		adminRefreshToken = admin.refreshToken;

		const a = await clientWithActiveService(api, 'cancel-accept');
		acceptClient = a.client;
		acceptServiceId = a.serviceId;
		acceptDomain = a.domain;

		const r = await clientWithActiveService(api, 'cancel-reject');
		rejectClient = r.client;
		rejectServiceId = r.serviceId;
		rejectDomain = r.domain;

		const m = await clientWithActiveService(api, 'cancel-immediate');
		immediateClient = m.client;
		immediateServiceId = m.serviceId;
		immediateDomain = m.domain;
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test('client submits an end-of-term cancellation request (accept flow)', async ({
		page,
		context
	}) => {
		await setSessionCookies(context, acceptClient.accessToken, acceptClient.refreshToken);
		await page.goto(`/services/${acceptServiceId}`);
		await expect(page.getByTestId('service-status')).toContainText(/active/i);

		await page.getByTestId('service-cancel-button').click();
		await expect(page.getByTestId('service-cancel-form')).toBeVisible();
		await page.getByTestId('service-cancel-mode-end-of-term').check();
		await page.getByTestId('service-cancel-submit').click();
		await expect(page.getByText(/cancellation/i).last()).toBeVisible({ timeout: 15_000 });

		// end_of_term has no visible effect on the service itself until an
		// admin accepts it - still shows Active, not some intermediate status.
		await page.reload();
		await expect(page.getByTestId('service-status')).toContainText(/active/i);
	});

	test('admin sees the pending request + banner and accepts it', async ({ page, context }) => {
		await setSessionCookies(context, adminAccessToken, adminRefreshToken);

		await page.goto(`/admin/services/${acceptServiceId}`);
		await expect(page.getByTestId('service-pending-cancellation-banner')).toBeVisible();

		await page.goto('/admin/services/cancellation-requests?status=pending');
		const row = page
			.locator('[data-testid^="row-cancellation-request-"]')
			.filter({ hasText: acceptDomain })
			.first();
		await expect(row).toBeVisible({ timeout: 10_000 });
		await expect(row).toContainText('Pending');

		await row.locator('[data-testid^="cancellation-request-accept-"]').click();
		const dialog = page.getByRole('dialog');
		await expect(dialog).toBeVisible();
		await dialog.getByRole('button', { name: /^Accept$/ }).click();
		await expect(page.getByText('Cancellation request accepted')).toBeVisible({ timeout: 10_000 });

		// Accepted requests drop out of the ?status=pending filter (correctly -
		// they're resolved), so re-check under ?status=accepted instead of
		// expecting the same filtered view to keep showing this row in place.
		await page.goto('/admin/services/cancellation-requests?status=accepted');
		const acceptedRow = page
			.locator('[data-testid^="row-cancellation-request-"]')
			.filter({ hasText: acceptDomain })
			.first();
		await expect(acceptedRow).toBeVisible({ timeout: 10_000 });
		await expect(acceptedRow).toContainText('Accepted');

		// Accepting resolves the request - the detail-page banner goes away.
		await page.goto(`/admin/services/${acceptServiceId}`);
		await expect(page.getByTestId('service-pending-cancellation-banner')).toHaveCount(0);
	});

	test('client submits an end-of-term cancellation request (reject flow)', async ({
		page,
		context
	}) => {
		await setSessionCookies(context, rejectClient.accessToken, rejectClient.refreshToken);
		await page.goto(`/services/${rejectServiceId}`);

		await page.getByTestId('service-cancel-button').click();
		await expect(page.getByTestId('service-cancel-form')).toBeVisible();
		await page.getByTestId('service-cancel-mode-end-of-term').check();
		await page.getByTestId('service-cancel-submit').click();
		await expect(page.getByText(/cancellation/i).last()).toBeVisible({ timeout: 15_000 });
	});

	test('admin rejects a pending request, leaving the service untouched', async ({
		page,
		context
	}) => {
		await setSessionCookies(context, adminAccessToken, adminRefreshToken);

		await page.goto('/admin/services/cancellation-requests?status=pending');
		const row = page
			.locator('[data-testid^="row-cancellation-request-"]')
			.filter({ hasText: rejectDomain })
			.first();
		await expect(row).toBeVisible({ timeout: 10_000 });

		await row.locator('[data-testid^="cancellation-request-reject-"]').click();
		const dialog = page.getByRole('dialog');
		await expect(dialog).toBeVisible();
		await dialog.getByRole('button', { name: /^Reject$/ }).click();
		await expect(page.getByText('Cancellation request rejected')).toBeVisible({ timeout: 10_000 });

		// Rejected requests drop out of the ?status=pending filter too - check
		// under ?status=rejected instead of the same filtered view.
		await page.goto('/admin/services/cancellation-requests?status=rejected');
		const rejectedRow = page
			.locator('[data-testid^="row-cancellation-request-"]')
			.filter({ hasText: rejectDomain })
			.first();
		await expect(rejectedRow).toBeVisible({ timeout: 10_000 });
		await expect(rejectedRow).toContainText('Rejected');

		await page.goto(`/admin/services/${rejectServiceId}`);
		await expect(page.getByTestId('service-status')).toContainText(/active/i);
		await expect(page.getByTestId('service-pending-cancellation-banner')).toHaveCount(0);
	});

	test('client submits an immediate cancellation request', async ({ page, context }) => {
		await setSessionCookies(context, immediateClient.accessToken, immediateClient.refreshToken);
		await page.goto(`/services/${immediateServiceId}`);

		await page.getByTestId('service-cancel-button').click();
		await expect(page.getByTestId('service-cancel-form')).toBeVisible();
		await page.getByTestId('service-cancel-mode-immediate').check();
		await page.getByTestId('service-cancel-submit').click();
		await expect(page.getByText(/cancellation/i).last()).toBeVisible({ timeout: 15_000 });
	});

	test('admin sees the immediate request auto-processed once the worker resolves it, with no pending actions', async ({
		page,
		context
	}) => {
		await setSessionCookies(context, adminAccessToken, adminRefreshToken);

		// The request starts pending and is only resolved by the worker once
		// ProvisionTerminate actually completes (resolvePendingCancellation) -
		// not synchronously at submission - so poll the admin API (fast, no UI
		// overhead) instead of asserting the end state immediately.
		await expect
			.poll(
				async () => {
					const res = await api.get(`${API_BASE}/api/v1/admin/services/cancellation-requests`, {
						headers: authHeaders(adminAccessToken),
						params: { status: 'auto_processed' }
					});
					if (!res.ok()) return false;
					const rows = ((await res.json()).data ?? []) as Array<{ service_domain?: string }>;
					return rows.some((r) => r.service_domain === immediateDomain);
				},
				{ timeout: 30_000, message: 'waiting for the immediate cancellation request to auto-resolve' }
			)
			.toBe(true);

		await page.goto('/admin/services/cancellation-requests?status=auto_processed');
		const row = page
			.locator('[data-testid^="row-cancellation-request-"]')
			.filter({ hasText: immediateDomain })
			.first();
		await expect(row).toBeVisible({ timeout: 10_000 });
		await expect(row).toContainText('Auto Processed');
		await expect(row).toContainText('Immediate');
		await expect(row.locator('[data-testid^="cancellation-request-accept-"]')).toHaveCount(0);
		await expect(row.locator('[data-testid^="cancellation-request-reject-"]')).toHaveCount(0);
	});

	test('unauthenticated request cannot reach the cancellation-requests endpoint', async ({
		request
	}) => {
		const res = await request.get(`${API_BASE}/api/v1/admin/services/cancellation-requests`);
		expect(res.status()).toBe(401); // no token at all -> RequireAuth rejects first
	});
});
