import { expect, test, type APIRequestContext } from './fixtures';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { loginApi, withAuthRateLimitRetry } from './helpers';

/**
 * Flow 6 (PRD §13.2 / docs/E2E.md §5.6) - Support ticket:
 * as a client, open a ticket with an attachment, confirm the mock mail sink
 * received a notification, reply, confirm status transitions, close it.
 *
 * Runs against the already-live stack (mockserver:9090, backend api:8080,
 * worker, frontend:5173 - see docs/E2E.md). Uses unique, timestamp-suffixed
 * test data since other E2E specs run concurrently against the same backend
 * + database; only this test's own rows are asserted on, never global counts.
 */

const API_BASE = process.env.API_URL ?? 'http://localhost:8080';
const MOCK_BASE = process.env.MOCK_BASE ?? 'http://localhost:9090';
const APP_BASE = process.env.BASE_URL ?? 'http://localhost:5173';

const ADMIN_EMAIL = 'admin@e2e.test';
const ADMIN_PASSWORD = 'AdminE2E!2026';

interface TicketEnvelope {
	data: { ticket: { status: string } } | null;
}

async function fetchTicketStatus(
	request: APIRequestContext,
	token: string,
	ticketId: number
): Promise<string> {
	const res = await request.get(`${API_BASE}/api/v1/tickets/${ticketId}`, {
		headers: { authorization: `Bearer ${token}` }
	});
	expect(res.ok(), `GET ticket ${ticketId} failed: ${await res.text()}`).toBeTruthy();
	const body = (await res.json()) as TicketEnvelope;
	if (!body.data) throw new Error(`ticket ${ticketId} not found`);
	return body.data.ticket.status;
}

function makeAttachment(label: string, contents: string): string {
	const dir = mkdtempSync(join(tmpdir(), 'e2e-ticket-'));
	const path = join(dir, `${label}.txt`);
	writeFileSync(path, contents);
	return path;
}

test('client opens a ticket with an attachment, is notified, replies, transitions status, and closes it', async ({
	page,
	request
}) => {
	// Generous headroom: the shared /auth/* rate-limit bucket (see
	// withAuthRateLimitRetry) can require up to ~75s of backoff on its own
	// when many concurrent E2E specs are hammering the same IP-keyed bucket.
	test.setTimeout(240_000);

	const stamp = Date.now();
	const email = `e2e.ticket.${stamp}@example.test`;
	const password = 'TicketE2E!2026';
	const subject = `E2E support flow ${stamp}`;

	// ---------------------------------------------------------------------
	// Setup: a fresh client account. Registration itself is covered by its
	// own spec (flow 1); this creates the account directly against the real
	// API so this spec stays focused on the ticket flow. Login never checks
	// email_verified_at (backend/internal/modules/auth/service.go Login), so
	// no verification step is needed before logging in through the real UI.
	// ---------------------------------------------------------------------
	const registerRes = await withAuthRateLimitRetry(() =>
		request.post(`${API_BASE}/api/v1/auth/register`, {
			data: {
				email,
				password,
				captcha_token: 'e2e-dummy-captcha-token',
				first_name: 'E2E',
				last_name: 'Ticket',
				address1: 'Jl. Percobaan 1',
				city: 'Jakarta',
				state: 'DKI Jakarta',
				postcode: '12345',
				country: 'ID',
				phone: '+628123456789'
			}
		})
	);
	expect(registerRes.ok(), `register failed: ${await registerRes.text()}`).toBeTruthy();

	// ---------------------------------------------------------------------
	// 1. Log in through the real UI. Retry on the shared /auth/* rate-limit
	//    bucket (see withAuthRateLimitRetry in helpers.ts) the same way the API-driven
	//    calls in this file do - a transient 429 here is cross-test
	//    interference from concurrently-running specs, not a bug.
	// ---------------------------------------------------------------------
	let uiLoggedIn = false;
	for (let attempt = 1; attempt <= 8 && !uiLoggedIn; attempt++) {
		await page.goto('/login');
		// SvelteKit hydration race: filling before client JS hydration wires up
		// bind:value can get silently wiped by the hydration effect snapping
		// the field back to its initial empty state - wait for network-idle.
		await page.waitForLoadState('networkidle');
		await page.locator('input[name="email"]').fill(email);
		await page.locator('input[name="password"]').fill(password);
		await page.locator('button[type="submit"]').click();
		try {
			await expect(page).toHaveURL(/\/dashboard/, { timeout: 20_000 });
			uiLoggedIn = true;
		} catch (err) {
			if (attempt === 8) throw err;
		}
	}

	// ---------------------------------------------------------------------
	// 2. Open a new ticket with an attachment.
	// ---------------------------------------------------------------------
	await page.goto('/support/new');
	// SvelteKit SSR-renders the form immediately, but client hydration
	// (which wires up the <select>'s bind:value) finishes slightly later -
	// interacting before it settles lets the hydration effect silently
	// snap a just-picked <select> back to its initial empty value. Wait for
	// network idle (covers the client bundle/chunk fetches) before driving
	// any form control.
	await page.waitForLoadState('networkidle');
	await expect(page.getByTestId('ticket-form')).toBeVisible();

	const departmentSelect = page.locator('#field-department_id');
	// Departments load server-side (SSR) - options are present as soon as the
	// form renders; still, wait explicitly rather than assuming timing.
	await expect(departmentSelect.locator('option', { hasText: 'Support' })).toHaveCount(1);
	await departmentSelect.selectOption({ label: 'Support' });

	await page.locator('#field-subject').fill(subject);
	await page.locator('#field-priority').selectOption('high');
	const initialMessage = `Initial message for ${subject}`;
	await page.locator('#field-message').fill(initialMessage);

	const createAttachment = makeAttachment('create-attachment', `attachment for ${subject}`);
	await page.locator('#field-attachments').setInputFiles(createAttachment);

	await page.getByTestId('ticket-submit').locator('button').click();

	await expect(page).toHaveURL(/\/support\/\d+$/);
	const ticketId = Number(page.url().split('/').filter(Boolean).pop());
	expect(ticketId).toBeGreaterThan(0);

	await expect(page.getByTestId('ticket-detail')).toBeVisible();
	await expect(page.getByTestId('ticket-detail-subject')).toHaveText(subject);

	const thread = page.getByTestId('ticket-thread');
	await expect(thread.locator('[data-testid^="ticket-reply-"]')).toHaveCount(1);
	// The initial message's attachment should be listed in the thread (this
	// exercises the real backend AttachmentView.index-based shape, see the
	// frontend fix in support/[id]/types.ts + .../attachments/[idx]/+server.ts).
	await expect(page.locator('[data-testid^="ticket-attachment-"]')).toHaveCount(1);

	// ---------------------------------------------------------------------
	// 3. Confirm the mock mail sink received the "ticket opened" notification.
	// ---------------------------------------------------------------------
	await expect
		.poll(
			async () => {
				const res = await request.get(`${MOCK_BASE}/mail/messages`, { params: { to: email } });
				if (!res.ok()) return [];
				return (await res.json()) as Array<{ subject: string }>;
			},
			{ timeout: 15_000, message: 'expected a ticket-opened notification email to the client' }
		)
		.toEqual(expect.arrayContaining([expect.objectContaining({ subject: expect.stringContaining('TKT-') })]));

	// ---------------------------------------------------------------------
	// 4. Confirm the initial status via the API (decoupled from i18n labels).
	// ---------------------------------------------------------------------
	const clientToken = (await loginApi(request, email, password)).accessToken;
	await expect.poll(() => fetchTicketStatus(request, clientToken, ticketId)).toBe('open');

	// ---------------------------------------------------------------------
	// 5. Reply to the ticket as the client -> status becomes customer_reply.
	// ---------------------------------------------------------------------
	const replyMessage = `Follow-up from client ${stamp}`;
	await page.locator('#field-message').fill(replyMessage);
	const replyAttachment = makeAttachment('reply-attachment', `reply attachment for ${subject}`);
	await page.getByTestId('ticket-reply-attachments').setInputFiles(replyAttachment);
	await page.getByTestId('ticket-reply-submit').locator('button').click();

	await expect(thread.locator('[data-testid^="ticket-reply-"]')).toHaveCount(2);
	await expect(page.locator('[data-testid^="ticket-attachment-"]')).toHaveCount(2);
	await expect.poll(() => fetchTicketStatus(request, clientToken, ticketId)).toBe('customer_reply');

	// ---------------------------------------------------------------------
	// 6. Staff publicly replies (via the real admin API) -> status becomes
	// answered. Driving this through the admin API rather than a second
	// browser session keeps the spec focused on the client-side flow while
	// still exercising a genuine status transition end-to-end.
	// ---------------------------------------------------------------------
	const adminToken = (await loginApi(request, ADMIN_EMAIL, ADMIN_PASSWORD)).accessToken;
	const staffReplyRes = await request.post(`${API_BASE}/api/v1/admin/tickets/${ticketId}/replies`, {
		headers: { authorization: `Bearer ${adminToken}` },
		data: { message: `Staff response ${stamp}` }
	});
	expect(staffReplyRes.ok(), `staff reply failed: ${await staffReplyRes.text()}`).toBeTruthy();
	await expect.poll(() => fetchTicketStatus(request, clientToken, ticketId)).toBe('answered');

	// Reload so the client UI reflects the staff reply before closing.
	await page.reload();
	await page.waitForLoadState('networkidle');
	await expect(thread.locator('[data-testid^="ticket-reply-"]')).toHaveCount(3);
	await expect(page.getByTestId('ticket-close')).toBeVisible();

	// ---------------------------------------------------------------------
	// 6b. Admin ticket-detail page surfaces reply attachments and its download
	// proxy resolves. Regression test for the admin frontend still speaking
	// object_key/query-param instead of the real AttachmentView.index path
	// segment (docs/RECONCILE.md's ticket-attachments note) - that bug made
	// `reply.attachments` silently always empty in the admin UI and would
	// have 404'd even if a link were built by hand.
	// ---------------------------------------------------------------------
	const browser = page.context().browser();
	if (!browser) throw new Error('no browser available for admin ticket-detail check');
	const adminContext = await browser.newContext({ baseURL: APP_BASE });
	await adminContext.addCookies([{ name: 'access_token', value: adminToken, url: APP_BASE }]);
	const adminPage = await adminContext.newPage();
	await adminPage.goto(`/admin/tickets/${ticketId}`);
	await adminPage.waitForLoadState('networkidle');

	const adminThread = adminPage.getByTestId('ticket-thread');
	const attachmentLinks = adminThread.locator('[data-testid^="ticket-attachment-"]');
	// The initial message and the client's follow-up reply each carried one
	// attachment; the staff reply above had none.
	await expect(attachmentLinks).toHaveCount(2);

	const firstHref = await attachmentLinks.first().getAttribute('href');
	expect(firstHref, 'attachment href should be present').toBeTruthy();
	expect(firstHref).toMatch(new RegExp(`^/admin/tickets/${ticketId}/attachments/\\d+\\?filename=`));

	// Actually resolve the download proxy - it must redirect to a presigned
	// URL, never 404 (exactly what silently broke under the old
	// ?key=<object_key> shape, since the backend only ever registered
	// /:id/attachments/:idx).
	const download = await adminPage.request.get(firstHref!, { maxRedirects: 0 });
	expect([301, 302, 303, 307, 308]).toContain(download.status());

	await adminContext.close();

	// ---------------------------------------------------------------------
	// 7. Close the ticket via the client UI -> status becomes closed.
	// ---------------------------------------------------------------------
	await page.getByTestId('ticket-close').locator('button').click();
	const dialog = page.getByRole('dialog');
	await expect(dialog).toBeVisible();
	// Footer renders [Cancel, Confirm] - the danger-styled confirm is last.
	await dialog.getByRole('button').last().click();

	await expect(page.getByTestId('ticket-close')).toHaveCount(0);
	await expect.poll(() => fetchTicketStatus(request, clientToken, ticketId)).toBe('closed');

	// A closed ticket hides the reply form behind an info notice.
	await expect(page.getByTestId('ticket-reply-form')).toHaveCount(0);
});
