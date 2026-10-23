import { withClientNames } from '$lib/server/adminNames';
import { apiFetch, type PageMeta } from '$lib/server/api';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import { INVOICE_STATUSES, type AdminInvoice, type InvoiceStatus } from './types';

/** Upper bound on one bulk request (a page is at most 1000 rows; this keeps a single request bounded). */
const BULK_MAX = 200;

export interface BulkFailure {
	id: number;
	message: string;
}

export const load: PageServerLoad = async (event) => {
	const { url } = event;

	const page = Math.max(1, Number(url.searchParams.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(url.searchParams.get('per_page')) || 10));
	const rawStatus = url.searchParams.get('status') ?? '';
	const status = (INVOICE_STATUSES as string[]).includes(rawStatus)
		? (rawStatus as InvoiceStatus)
		: '';
	const search = (url.searchParams.get('search') ?? '').trim();
	const dateFrom = url.searchParams.get('date_from') ?? '';
	const dateTo = url.searchParams.get('date_to') ?? '';

	let invoices: AdminInvoice[] = [];
	let meta: PageMeta | null = null;
	let errorMessage: string | null = null;

	const res = await apiFetch<AdminInvoice[]>(event, '/api/v1/admin/invoices', {
		query: {
			page,
			per_page: perPage,
			status: status || undefined,
			search: search || undefined,
			date_from: dateFrom || undefined,
			date_to: dateTo || undefined
		}
	});

	if (res.error) {
		errorMessage = res.error.message;
	} else {
		invoices = await withClientNames(event, res.data ?? []);
		meta = res.meta;
	}

	return {
		invoices,
		page: meta?.page ?? page,
		perPage: meta?.per_page ?? perPage,
		total: meta?.total ?? 0,
		status,
		search,
		dateFrom,
		dateTo,
		statusOptions: INVOICE_STATUSES,
		errorMessage
	};
};

export const actions: Actions = {
	/**
	 * "With Selected": Mark Paid records a manual payment for the invoice's full
	 * total (POST /admin/invoices/:id/payment); Mark Cancelled cancels it
	 * (POST /admin/invoices/:id/cancel). The API enforces the state machine, so
	 * an invoice that cannot take the action (e.g. already paid) is reported in
	 * `failures` while the rest still go through.
	 */
	bulk: async (event) => {
		const form = await event.request.formData();
		const op = String(form.get('op') ?? '');
		if (op !== 'markPaid' && op !== 'cancel') {
			return fail(400, { op: 'bulk', errorMessage: 'Unknown bulk action.' });
		}

		let ids: number[] = [];
		try {
			const parsed = JSON.parse(String(form.get('ids') ?? '[]')) as unknown;
			if (Array.isArray(parsed)) {
				ids = parsed.map(Number).filter((n) => Number.isInteger(n) && n > 0);
			}
		} catch {
			ids = [];
		}
		ids = [...new Set(ids)];
		if (ids.length === 0) {
			return fail(400, { op: 'bulk', errorMessage: 'Select at least one invoice.' });
		}
		if (ids.length > BULK_MAX) {
			return fail(400, {
				op: 'bulk',
				errorMessage: `Select at most ${BULK_MAX} invoices at a time.`
			});
		}

		let done = 0;
		const failures: BulkFailure[] = [];
		// Sequential on purpose: each call takes a row lock server-side and a
		// burst of parallel mutations would only contend on the same tables.
		for (const id of ids) {
			if (op === 'markPaid') {
				const current = await apiFetch<{ invoice: AdminInvoice }>(
					event,
					`/api/v1/admin/invoices/${id}`
				);
				if (current.error || !current.data?.invoice) {
					failures.push({ id, message: current.error?.message ?? 'Invoice not found.' });
					continue;
				}
				const res = await apiFetch(event, `/api/v1/admin/invoices/${id}/payment`, {
					method: 'POST',
					body: { amount: current.data.invoice.total, method: 'manual' }
				});
				if (res.error) failures.push({ id, message: res.error.message });
				else done++;
			} else {
				const res = await apiFetch(event, `/api/v1/admin/invoices/${id}/cancel`, {
					method: 'POST',
					body: {}
				});
				if (res.error) failures.push({ id, message: res.error.message });
				else done++;
			}
		}

		return { op: 'bulk' as const, action: op, done, failures };
	}
};
