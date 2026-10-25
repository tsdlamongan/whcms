import { expect, test, type APIRequestContext, type Page } from './fixtures';
import {
	API_BASE,
	MOCK_BASE,
	adminToken,
	authHeaders,
	createOrder,
	newApi,
	payInvoiceViaMock,
	pollListStatus,
	registerVerifyLogin,
	setSessionCookies,
	uniqueDomainLabel
} from './helpers';

/**
 * Admin side of the domain document / verification / refund lifecycle
 * (docs.dewabiz.co.id "Auto Activation"; PRD FR-DOM-009).
 *
 * A client orders + pays a `.co.id` domain (a document-requiring TLD). The
 * RDash mock registers it Active with `is_required_document=1` and
 * verification "Waiting", exactly like Auto Activation does, and the spec then
 * plays the registrar's reviewer through the mock's control endpoint while the
 * admin drives the real HostPanel domain page:
 *
 *   waiting -> validating -> review bounced (reason shown) -> suspended ->
 *   rejected (document failure: documented NO automatic refund) -> admin
 *   refunds part of the price to the client's credit (once only).
 */

const REFUND_AMOUNT = 50_000;

/** Play the registrar: change a mock domain's status/verification. */
async function mockDomainState(
	api: APIRequestContext,
	domain: string,
	state: {
		status?: number;
		verification_status?: number;
		status_reason?: string;
		is_refundable?: boolean;
	}
): Promise<void> {
	const res = await api.post(`${MOCK_BASE}/mock/rdash/domain-state`, {
		data: { domain, ...state }
	});
	expect(res.ok(), `mock domain-state: ${await res.text()}`).toBeTruthy();
}

/** Click Sync Status on the admin domain page and confirm the modal. */
async function syncViaUi(page: Page): Promise<void> {
	await page.getByTestId('domain-sync-button').getByRole('button').click();
	await page.getByTestId('domain-sync-confirm').click();
	// the modal closes once the sync action succeeded
	await expect(page.getByTestId('domain-sync-confirm')).toHaveCount(0, { timeout: 20_000 });
}

async function creditBalance(api: APIRequestContext, token: string): Promise<number> {
	const res = await api.get(`${API_BASE}/api/v1/account/credit`, { headers: authHeaders(token) });
	expect(res.ok(), `account credit: ${await res.text()}`).toBeTruthy();
	return ((await res.json()).data as { balance: number }).balance;
}

test.describe('admin: domain documents, verification and refund', () => {
	test('document domain: review bounce, suspension, rejection without auto-refund, manual refund', async ({
		page,
		context
	}) => {
		test.setTimeout(300_000);

		const api = await newApi();
		try {
			const admin = await adminToken(api);
			const client = await registerVerifyLogin(api, 'docadmin');
			const domain = `${uniqueDomainLabel()}.co.id`;

			// --- a paid, registered .co.id domain ----------------------------
			let domainId = 0;
			await test.step('client orders and pays a document-requiring domain', async () => {
				const invoiceId = await createOrder(api, client.accessToken, [
					{ item_type: 'domain_register', domain, domain_years: 1 }
				]);
				await payInvoiceViaMock(api, client.accessToken, invoiceId);
				const row = await pollListStatus(
					api,
					client.accessToken,
					'/api/v1/domains',
					(r) => r.name === domain,
					'active',
					90_000
				);
				domainId = row.id as number;
				expect(domainId).toBeGreaterThan(0);
			});

			await setSessionCookies(context, admin);
			const detail = `/admin/domains/${domainId}`;
			await page.goto(detail);
			await page.waitForLoadState('networkidle');

			await test.step('panel shows documents required, waiting, and the documented timeline', async () => {
				await expect(page.getByTestId('admin-domain-verification')).toBeVisible({
					timeout: 15_000
				});
				await expect(page.getByTestId('admin-domain-documents-required')).toHaveText('Yes');
				await expect(page.getByTestId('admin-domain-verification-status')).toHaveText('Waiting');
				await expect(page.getByTestId('domain-status')).toHaveText('active');
				// upload by day 3, suspended on day 5, deleted on day 30
				await expect(page.getByTestId('admin-domain-deadlines')).toBeVisible();
				await expect(page.getByTestId('admin-domain-deadline-upload')).not.toHaveText('—');
				await expect(page.getByTestId('admin-domain-deadline-suspend')).not.toHaveText('—');
				await expect(page.getByTestId('admin-domain-deadline-delete')).not.toHaveText('—');
				// nothing to refund on a live domain
				await expect(page.getByTestId('admin-domain-refund-form')).toHaveCount(0);
				await expect(page.getByTestId('admin-domain-refunded')).toHaveCount(0);
			});

			await test.step('documents uploaded -> document validating', async () => {
				await mockDomainState(api, domain, { verification_status: 2 });
				await syncViaUi(page);
				await expect(page.getByTestId('admin-domain-verification-status')).toHaveText(
					'Document validating',
					{ timeout: 15_000 }
				);
				await expect(page.getByTestId('admin-domain-documents-rejected')).toHaveCount(0);
			});

			await test.step('review bounced -> reason and rejection time shown, domain stays active', async () => {
				await mockDomainState(api, domain, {
					verification_status: 0,
					status_reason: 'KTP tidak terbaca'
				});
				await syncViaUi(page);
				await expect(page.getByTestId('admin-domain-status-reason')).toContainText(
					'KTP tidak terbaca',
					{ timeout: 15_000 }
				);
				await expect(page.getByTestId('admin-domain-verification-status')).toHaveText('Waiting');
				await expect(page.getByTestId('admin-domain-documents-rejected')).toBeVisible();
				await expect(page.getByTestId('domain-status')).toHaveText('active');
			});

			await test.step('registrar suspends the domain', async () => {
				await mockDomainState(api, domain, { status: 7 });
				await syncViaUi(page);
				await expect(page.getByTestId('domain-status')).toHaveText('suspended', {
					timeout: 15_000
				});
				// still counting down to deletion
				await expect(page.getByTestId('admin-domain-deadlines')).toBeVisible();
				await expect(page.getByTestId('admin-domain-refund-form')).toHaveCount(0);
			});

			const balanceBefore = await creditBalance(api, client.accessToken);

			await test.step('registrar rejects it: documented policy = no automatic refund', async () => {
				// the registrar even flags it refundable - missing documents win
				await mockDomainState(api, domain, { status: 8, is_refundable: true });
				await syncViaUi(page);
				await expect(page.getByTestId('domain-status')).toHaveText('rejected', { timeout: 15_000 });
				await expect(page.getByTestId('admin-domain-refundable')).toHaveText('Yes');
				await expect(page.getByTestId('admin-domain-refunded')).toHaveCount(0);
				await expect(page.getByTestId('admin-domain-refund-form')).toBeVisible();
				expect(await creditBalance(api, client.accessToken)).toBe(balanceBefore);
			});

			await test.step('admin refunds part of the price to the client credit', async () => {
				await page.getByTestId('admin-domain-refund-amount').fill(String(REFUND_AMOUNT));
				await page.getByTestId('admin-domain-refund-reason').fill('goodwill after document review');
				await page.getByTestId('admin-domain-refund-submit').click();
				await expect(page.getByTestId('admin-domain-refund-confirm-text')).toBeVisible();
				await page.getByTestId('admin-domain-refund-confirm').click();

				await expect(page.getByTestId('admin-domain-refunded')).toBeVisible({ timeout: 20_000 });
				await expect(page.getByTestId('admin-domain-refunded')).toContainText('50.000');
				await expect(page.getByTestId('admin-domain-refund-form')).toHaveCount(0);

				expect(await creditBalance(api, client.accessToken)).toBe(balanceBefore + REFUND_AMOUNT);
			});

			await test.step('a domain is refunded at most once', async () => {
				const again = await api.post(`${API_BASE}/api/v1/admin/domains/${domainId}/refund`, {
					headers: authHeaders(admin),
					data: { amount: 1000 }
				});
				expect(again.status()).toBe(409);
				expect(await creditBalance(api, client.accessToken)).toBe(balanceBefore + REFUND_AMOUNT);
			});
		} finally {
			await api.dispose();
		}
	});

	test('refund amount above what the client paid is refused', async ({ page, context }) => {
		test.setTimeout(300_000);

		const api = await newApi();
		try {
			const admin = await adminToken(api);
			const client = await registerVerifyLogin(api, 'docadmin-cap');
			const domain = `${uniqueDomainLabel()}.co.id`;

			const invoiceId = await createOrder(api, client.accessToken, [
				{ item_type: 'domain_register', domain, domain_years: 1 }
			]);
			await payInvoiceViaMock(api, client.accessToken, invoiceId);
			const row = await pollListStatus(
				api,
				client.accessToken,
				'/api/v1/domains',
				(r) => r.name === domain,
				'active',
				90_000
			);
			const domainId = row.id as number;

			// the registrar rejects the domain outright
			await mockDomainState(api, domain, { status: 8, is_refundable: false });

			await setSessionCookies(context, admin);
			await page.goto(`/admin/domains/${domainId}`);
			await page.waitForLoadState('networkidle');
			await syncViaUi(page);
			await expect(page.getByTestId('domain-status')).toHaveText('rejected', { timeout: 15_000 });

			// an amount above what the client paid is refused; the page surfaces the API error
			await page.getByTestId('admin-domain-refund-amount').fill('999999999');
			await page.getByTestId('admin-domain-refund-submit').click();
			await page.getByTestId('admin-domain-refund-confirm').click();
			await expect(page.getByTestId('domain-action-error')).toContainText(
				'exceeds what the client paid',
				{ timeout: 20_000 }
			);
			await expect(page.getByTestId('admin-domain-refunded')).toHaveCount(0);
			await expect(page.getByTestId('admin-domain-refund-form')).toBeVisible();

			// nothing was credited
			expect(await creditBalance(api, client.accessToken)).toBe(0);
		} finally {
			await api.dispose();
		}
	});
});
