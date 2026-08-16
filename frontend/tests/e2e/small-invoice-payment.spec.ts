import { expect, test, type APIRequestContext } from './fixtures';
import {
	API_BASE,
	adminFindClientId,
	adminToken,
	authHeaders,
	newApi,
	payInvoiceViaMock,
	registerVerifyLogin,
	setSessionCookies,
	type Client
} from './helpers';

/**
 * Sub-minimum (< Rp10.000) invoice payments - e.g. a small prorated upgrade
 * diff. Duitku rejects every non-QRIS channel below Rp10.000 ("Minimum
 * Payment 10000 IDR", observed against the real sandbox and mirrored by the
 * mockserver), so:
 *
 * 1. the invoice payment page must only offer channels that can actually
 *    complete (QRIS stays, VA/card/retail/e-wallet are filtered out);
 * 2. if a sub-minimum non-QRIS payment is still attempted (e.g. straight
 *    against the API), the surfaced error must carry Duitku's own reason
 *    instead of a bare "duitku error";
 * 3. paying the same invoice via QRIS works end-to-end.
 */

test.describe.configure({ mode: 'serial', timeout: 90_000 });

test.describe('small invoice: per-channel minimum handling', () => {
	let api: APIRequestContext;
	let client: Client;
	let invoiceId = 0;

	test.beforeAll(async () => {
		api = await newApi();
		client = await registerVerifyLogin(api, 'smallinv');

		// Admin issues a Rp5.000 manual invoice (stands in for any small
		// amount, like a prorated upgrade diff near the end of a cycle).
		const adminTok = await adminToken(api);
		const clientId = await adminFindClientId(api, adminTok, client.email);
		const res = await api.post(`${API_BASE}/api/v1/admin/invoices`, {
			headers: authHeaders(adminTok),
			data: {
				client_id: clientId,
				items: [{ description: 'Prorated upgrade diff (e2e small amount)', amount: 5000 }]
			}
		});
		expect(res.ok(), `admin create invoice: ${await res.text()}`).toBeTruthy();
		invoiceId = (await res.json()).data.id as number;
		expect(invoiceId).toBeGreaterThan(0);
	});

	test.afterAll(async () => {
		await api?.dispose();
	});

	test.beforeEach(async ({ context }) => {
		await setSessionCookies(context, client.accessToken, client.refreshToken);
	});

	test('payment page offers QRIS but hides sub-minimum channels', async ({ page }) => {
		await page.goto(`/billing/invoices/${invoiceId}`);
		await expect(page.getByTestId('payment-methods')).toBeVisible();

		// QRIS accepts micro-payments and must stay selectable.
		await expect(page.getByTestId('payment-method-SP')).toBeVisible();
		// VA/card/retail channels can't complete a Rp5.000 payment - the mock
		// gateway lists them, but the backend filters them for this amount.
		for (const code of ['BC', 'M2', 'VC', 'FT']) {
			await expect(page.getByTestId(`payment-method-${code}`)).toHaveCount(0);
		}
	});

	test('a forced sub-minimum VA payment surfaces the gateway reason', async () => {
		// Straight against the API (the UI no longer offers BC at this amount).
		const res = await api.post(`${API_BASE}/api/v1/invoices/${invoiceId}/pay`, {
			headers: authHeaders(client.accessToken),
			data: { method: 'BC' }
		});
		expect(res.ok()).toBeFalsy();
		const body = await res.json();
		expect(body.error?.message ?? '').toContain('Minimum Payment 10000 IDR');
	});

	test('the same invoice is payable via QRIS end-to-end', async () => {
		await payInvoiceViaMock(api, client.accessToken, invoiceId, 'SP');
	});
});
