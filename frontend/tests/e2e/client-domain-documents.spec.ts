import { expect, test, type APIRequestContext, type Locator, type Page } from './fixtures';
import {
	API_BASE,
	MOCK_BASE,
	adminToken,
	authHeaders,
	ensureSellableTld,
	createOrder,
	newApi,
	payInvoiceViaMock,
	pollListStatus,
	registerVerifyLogin,
	setSessionCookies,
	uniqueDomainLabel,
	waitForMailMatch,
	type Client
} from './helpers';

/**
 * Domain document / verification lifecycle, client side (RDash "Auto
 * Activation", docs.dewabiz.co.id):
 *   - .co.id/.or.id/.ac.id/.sch.id/.ponpes.id register ACTIVE, then the client
 *     uploads documents on the registrar-hosted page (the API only hands out a
 *     link) - bounced reviews, the day-5 suspension and the final verification
 *     all show up through the registrar sync;
 *   - .id/.my.id/.biz.id/.web.id only need an email verification (resend);
 *   - when the registrar rejects a domain the client is refunded as account
 *     credit - EXCEPT when the required documents were never verified (the
 *     documented "no refund" case).
 *
 * The real worker registers the domains against the RDash mock; the mock's
 * reviewer is played through POST /mock/rdash/domain-state (status ints:
 * 1 Active, 7 Suspended, 8 Rejected; verification 0 Waiting, 2 Document
 * Validating, 3 Active) and the client's "Check status" button forces the
 * registrar sync that the cron would otherwise run.
 */

/** Orders + pays a domain registration and waits until the worker made it active. */
async function activeDomain(
	api: APIRequestContext,
	client: Client,
	name: string
): Promise<{ domainId: number; invoiceTotal: number }> {
	const invoiceId = await createOrder(api, client.accessToken, [
		{ item_type: 'domain_register', domain: name, domain_years: 1 }
	]);
	const inv = await api.get(`${API_BASE}/api/v1/invoices/${invoiceId}`, {
		headers: authHeaders(client.accessToken)
	});
	const invBody = (await inv.json()).data as { invoice?: { total: number }; total?: number };
	const invoiceTotal = invBody.invoice?.total ?? invBody.total ?? 0;

	await payInvoiceViaMock(api, client.accessToken, invoiceId);
	const row = await pollListStatus(
		api,
		client.accessToken,
		'/api/v1/domains',
		(r) => r.name === name,
		'active',
		90_000
	);
	return { domainId: row.id as number, invoiceTotal };
}

/** Plays the registrar: change the mock-side state of a domain. */
async function mockState(api: APIRequestContext, body: Record<string, unknown>): Promise<void> {
	const res = await api.post(`${MOCK_BASE}/mock/rdash/domain-state`, { data: body });
	expect(res.ok(), `mock domain-state: ${await res.text()}`).toBeTruthy();
}

async function creditBalance(api: APIRequestContext, client: Client): Promise<number> {
	const res = await api.get(`${API_BASE}/api/v1/account/credit`, {
		headers: authHeaders(client.accessToken)
	});
	expect(res.ok(), `credit: ${await res.text()}`).toBeTruthy();
	return ((await res.json()).data as { balance: number }).balance;
}

/** Resolves once `btn` is idle - or gone (the panel unmounts when the domain verifies). */
async function waitUntilIdle(btn: Locator): Promise<void> {
	await expect(async () => {
		if ((await btn.count()) > 0) {
			expect(await btn.getAttribute('aria-busy')).not.toBe('true');
		}
	}).toPass({ timeout: 20_000 });
}

/** Click "Check status" and wait for the form action to finish. */
async function checkStatus(page: Page): Promise<void> {
	const btn = page.getByTestId('document-refresh-button');
	await btn.click();
	await waitUntilIdle(btn);
}

test.describe('client domain documents & verification', () => {
	test.describe.configure({ mode: 'parallel' });
	let api: APIRequestContext;
	let admin: string;
	// extensions this spec put on sale itself (removed again afterwards)
	const createdTlds: number[] = [];

	test.beforeAll(async () => {
		api = await newApi();
		admin = await adminToken(api);
		// A domain can only be ordered under an extension priced in Domain
		// Pricing. co.id comes from the seed, but admin-domain-pricing deletes
		// and re-imports my.id, so neither is assumed to still exist.
		for (const tld of ['co.id', 'my.id', 'com']) {
			const id = await ensureSellableTld(api, admin, tld);
			if (id !== null) createdTlds.push(id);
		}
	});
	test.afterAll(async () => {
		for (const id of createdTlds) {
			await api.delete(`${API_BASE}/api/v1/admin/tld-pricing/${id}`, {
				headers: authHeaders(admin)
			});
		}
		await api?.dispose();
	});

	test('document domain: upload page, bounced review, suspension, verification', async ({
		page,
		context
	}) => {
		test.setTimeout(240_000);
		const client = await registerVerifyLogin(api, 'docs');
		const name = `${uniqueDomainLabel()}.co.id`;
		const { domainId } = await activeDomain(api, client, name);
		await setSessionCookies(context, client.accessToken, client.refreshToken);

		// --- the list flags it, the detail page explains what is owed ---------
		await page.goto('/domains');
		await expect(page.getByTestId(`domain-docs-badge-${domainId}`)).toBeVisible();

		await page.goto(`/domains/${domainId}`);
		await page.waitForLoadState('networkidle');
		const panel = page.getByTestId('domain-verification-panel');
		await expect(panel).toBeVisible();
		await expect(panel).toHaveAttribute('data-kind', 'documents');
		await expect(page.getByTestId('domain-status')).toContainText(/active/i);
		await expect(page.getByTestId('verification-state')).toHaveAttribute('data-state', 'waiting');
		// the documented timeline: upload window, suspension, deletion (no refund)
		await expect(page.getByTestId('deadline-upload-by')).toBeVisible();
		await expect(page.getByTestId('deadline-suspend-on')).toBeVisible();
		await expect(page.getByTestId('deadline-delete-on')).toBeVisible();
		// .co.id requirements
		await expect(page.getByTestId('document-requirement-identity')).toBeVisible();
		await expect(page.getByTestId('document-requirement-companyLegal')).toBeVisible();
		await expect(page.getByTestId('document-requirement-intlBrand')).toBeVisible();
		await expect(page.getByTestId('document-requirement-schoolPermit')).toHaveCount(0);

		// --- upload: the API hands out the registrar-hosted page link ---------
		await page.getByTestId('document-upload-button').click();
		const link = page.getByTestId('document-upload-link');
		await expect(link).toBeVisible({ timeout: 20_000 });
		const href = await link.getAttribute('href');
		expect(href).toMatch(new RegExp(`^${MOCK_BASE}/documents/upload/[0-9a-f]{16,}$`));
		await expect(link).toHaveAttribute('target', '_blank');
		await expect(link).toHaveAttribute('rel', /noopener/);

		// the client uploads on the registrar's page (a separate tab)
		const upload = await context.newPage();
		await upload.goto(href!);
		await expect(upload.getByTestId('mock-upload-title')).toContainText(name);
		await upload.getByTestId('mock-upload-submit').click();
		await expect(upload.getByTestId('mock-upload-done')).toBeVisible();
		await upload.close();

		// back in the portal: "Check status" syncs with the registrar
		await checkStatus(page);
		await expect(page.getByTestId('verification-state')).toHaveAttribute(
			'data-state',
			'document_validating'
		);
		await expect(page.getByTestId('document-rejected-alert')).toHaveCount(0);

		// --- the review bounces the documents ---------------------------------
		await mockState(api, {
			domain: name,
			verification_status: 0,
			status_reason: 'KTP tidak terbaca'
		});
		await checkStatus(page);
		await expect(page.getByTestId('verification-state')).toHaveAttribute('data-state', 'waiting');
		const bounced = page.getByTestId('document-rejected-alert');
		await expect(bounced).toBeVisible();
		await expect(bounced).toContainText('KTP tidak terbaca');
		await expect(page.getByTestId('document-upload-button')).toContainText(/re-upload/i);
		// ... and the client is told by email
		await waitForMailMatch(api, client.email, /(KTP tidak terbaca)/);

		// --- day 5: the registrar suspends the domain -------------------------
		await mockState(api, { domain: name, status: 7, status_reason: 'Document not uploaded' });
		await checkStatus(page);
		await expect(page.getByTestId('domain-suspended-alert')).toBeVisible();
		await expect(page.getByTestId('domain-status')).toContainText(/suspended/i);
		// a suspended domain can still upload
		await expect(page.getByTestId('document-upload-button')).toBeEnabled();
		await waitForMailMatch(api, client.email, /(Document not uploaded)/);

		// --- documents verified: unsuspended, nothing left to do --------------
		await mockState(api, { domain: name, status: 1, verification_status: 3, status_reason: '' });
		await page.reload();
		await page.waitForLoadState('networkidle');
		// the stored state is still "suspended" until the registrar sync runs
		await expect(page.getByTestId('domain-suspended-alert')).toBeVisible();
		await checkStatus(page);
		await expect(page.getByTestId('domain-verification-panel')).toHaveCount(0);
		await expect(page.getByTestId('domain-status')).toContainText(/active/i);

		await page.goto('/domains');
		await expect(page.getByTestId(`domain-docs-badge-${domainId}`)).toHaveCount(0);
	});

	test('registrar rejects a refundable domain: client is credited back automatically', async ({
		page,
		context
	}) => {
		test.setTimeout(240_000);
		const client = await registerVerifyLogin(api, 'refund');
		const name = `${uniqueDomainLabel()}.com`;
		const { domainId, invoiceTotal } = await activeDomain(api, client, name);
		expect(invoiceTotal).toBeGreaterThan(0);
		const before = await creditBalance(api, client);
		await setSessionCookies(context, client.accessToken, client.refreshToken);

		await mockState(api, {
			domain: name,
			status: 8,
			status_reason: 'Registry rejected the order',
			is_refundable: true
		});
		// a domain without documents has no verification panel, so sync through the API
		const sync = await api.post(`${API_BASE}/api/v1/domains/${domainId}/refresh`, {
			headers: authHeaders(client.accessToken)
		});
		expect(sync.ok(), `refresh: ${await sync.text()}`).toBeTruthy();
		expect(((await sync.json()).data as { status: string }).status).toBe('rejected');

		await page.goto(`/domains/${domainId}`);
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('domain-status')).toContainText(/rejected/i);
		await expect(page.getByTestId('domain-rejected-panel')).toBeVisible();
		await expect(page.getByTestId('domain-rejected-reason')).toContainText(
			'Registry rejected the order'
		);
		const refunded = page.getByTestId('domain-refunded');
		await expect(refunded).toBeVisible();
		await expect(refunded).toHaveAttribute('data-amount', String(invoiceTotal));
		await expect(page.getByTestId('domain-no-refund-documents')).toHaveCount(0);

		// the money really moved: account credit, exactly once
		expect(await creditBalance(api, client)).toBe(before + invoiceTotal);
		const again = await api.post(`${API_BASE}/api/v1/domains/${domainId}/refresh`, {
			headers: authHeaders(client.accessToken)
		});
		expect(again.ok()).toBeTruthy();
		expect(await creditBalance(api, client)).toBe(before + invoiceTotal);

		// ... and the client is told what happened
		await waitForMailMatch(api, client.email, /(Rp\s?[\d.]+)/);
	});

	test('rejected after the documents were never verified: no refund (documented policy)', async ({
		page,
		context
	}) => {
		test.setTimeout(240_000);
		const client = await registerVerifyLogin(api, 'norefund');
		const name = `${uniqueDomainLabel()}.co.id`;
		const { domainId } = await activeDomain(api, client, name);
		const before = await creditBalance(api, client);
		await setSessionCookies(context, client.accessToken, client.refreshToken);

		// day 30: the registrar deletes the domain; even a "refundable" flag
		// cannot override "no refund when the documents were missing/rejected"
		await mockState(api, {
			domain: name,
			status: 8,
			status_reason: 'Document not uploaded',
			is_refundable: true
		});
		await page.goto(`/domains/${domainId}`);
		await page.waitForLoadState('networkidle');
		await checkStatus(page);

		await expect(page.getByTestId('domain-status')).toContainText(/rejected/i);
		await expect(page.getByTestId('domain-rejected-panel')).toBeVisible();
		await expect(page.getByTestId('domain-no-refund-documents')).toBeVisible();
		await expect(page.getByTestId('domain-refunded')).toHaveCount(0);
		expect(await creditBalance(api, client)).toBe(before);

		// the name is free again: the rejected domain no longer counts as the live one
		const list = await api.get(`${API_BASE}/api/v1/domains`, {
			headers: authHeaders(client.accessToken),
			params: { status: 'active' }
		});
		const rows = ((await list.json()).data ?? []) as Array<{ name: string }>;
		expect(rows.find((r) => r.name === name)).toBeUndefined();
	});

	test('email-verified TLD: resend the verification email, then it verifies', async ({
		page,
		context
	}) => {
		test.setTimeout(240_000);
		const client = await registerVerifyLogin(api, 'emailverify');
		const name = `${uniqueDomainLabel()}.my.id`;
		const { domainId } = await activeDomain(api, client, name);
		await setSessionCookies(context, client.accessToken, client.refreshToken);

		await page.goto(`/domains/${domainId}`);
		await page.waitForLoadState('networkidle');
		const panel = page.getByTestId('domain-verification-panel');
		await expect(panel).toBeVisible();
		await expect(panel).toHaveAttribute('data-kind', 'email');
		// email verification has no documents and no deletion
		await expect(page.getByTestId('document-upload-button')).toHaveCount(0);
		await expect(page.getByTestId('document-requirements')).toHaveCount(0);
		await expect(page.getByTestId('deadline-suspend-on')).toBeVisible();
		await expect(page.getByTestId('deadline-delete-on')).toHaveCount(0);

		const resend = page.getByTestId('verification-resend-button');
		await resend.click();
		await waitUntilIdle(resend);
		await expect
			.poll(
				async () => {
					const res = await api.get(`${MOCK_BASE}/mock/rdash/state`, { params: { domain: name } });
					return ((await res.json()) as { resend_count: number }).resend_count;
				},
				{ timeout: 20_000 }
			)
			.toBe(1);

		// the registrant clicked the link in the email
		await mockState(api, { domain: name, verification_status: 3 });
		await checkStatus(page);
		await expect(page.getByTestId('domain-verification-panel')).toHaveCount(0);
	});

	test('ordering: document extensions are flagged in search and in the cart', async ({ page }) => {
		test.setTimeout(120_000);
		const label = uniqueDomainLabel();
		const docName = `${label}.co.id`;

		await page.goto(`/order/domain?q=${encodeURIComponent(docName)}`);
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId(`domain-result-${docName}`)).toBeVisible({ timeout: 20_000 });
		await expect(page.getByTestId(`domain-docs-badge-${docName}`)).toBeVisible();

		// a gTLD row is not flagged
		const comRow = page.getByTestId(`domain-result-${label}.com`);
		if ((await comRow.count()) > 0) {
			await expect(page.getByTestId(`domain-docs-badge-${label}.com`)).toHaveCount(0);
		}

		const registerBtn = page.getByTestId(`domain-register-${docName}`);
		const inCart = page.getByTestId(`domain-in-cart-${docName}`);
		for (let attempt = 1; attempt <= 3; attempt++) {
			await registerBtn.click();
			try {
				await expect(inCart).toBeVisible({ timeout: 5_000 });
				break;
			} catch (err) {
				if (attempt === 3) throw err;
			}
		}
		await page.getByTestId('domain-continue-to-cart').click();
		await expect(page).toHaveURL(/\/order\/cart$/, { timeout: 15_000 });
		await expect(page.getByTestId('cart-docs-note-0')).toBeVisible();
	});
});
