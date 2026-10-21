import { apiFetch } from '$lib/server/api';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import type { AdminClientLite, AdminInvoice, AdminInvoiceItem, AdminTransaction } from '../types';

/**
 * The real backend response for GET /admin/invoices/:id is
 * `{invoice: {...}, items: [...]}` - NOT the flat invoice directly. (Every
 * OTHER admin detail endpoint used by this app returns its entity flat, so
 * this one nested exception is easy to miss; a prior version of this loader
 * assumed flat and set `invoice = res.data`, which silently produced an
 * object with no `status`/`invoice_number`/etc. - anything that merely
 * displayed those fields as text rendered blank, but the status banner's
 * `invoice.status.toUpperCase()` crashed the whole page with a 500.)
 */
interface AdminInvoiceDetailResponse {
	invoice: AdminInvoice;
	items?: AdminInvoiceItem[];
	/** This invoice's own payment attempts (billing.InvoiceDetail.transactions). */
	transactions?: AdminTransaction[];
}

export const load: PageServerLoad = async (event) => {
	const id = event.params.id;

	const res = await apiFetch<AdminInvoiceDetailResponse>(event, `/api/v1/admin/invoices/${id}`);

	if (res.error || !res.data?.invoice) {
		return {
			invoice: null as AdminInvoice | null,
			client: null as AdminClientLite | null,
			transactions: [] as AdminTransaction[],
			errorMessage: res.error?.message ?? 'Invoice not found',
			notFound: res.status === 404
		};
	}

	const invoice: AdminInvoice = { ...res.data.invoice, items: res.data.items ?? [] };

	// Client block: use the embedded client when present, otherwise fetch it.
	let client: AdminClientLite | null = invoice.client ?? null;
	if (!client && invoice.client_id) {
		const clientRes = await apiFetch<AdminClientLite>(
			event,
			`/api/v1/admin/clients/${invoice.client_id}`
		);
		client = clientRes.data ?? null;
	}

	return {
		invoice,
		client,
		// The detail endpoint already returns exactly this invoice's
		// transactions (no second, unfiltered list call needed).
		transactions: res.data.transactions ?? [],
		errorMessage: null as string | null,
		notFound: false
	};
};

function actionError(res: { status: number; error: { message: string } | null }) {
	return fail(res.status >= 400 ? res.status : 500, {
		errorMessage: res.error?.message ?? 'Request failed'
	});
}

export const actions: Actions = {
	update: async (event) => {
		const form = await event.request.formData();
		const dueDate = String(form.get('due_date') ?? '').trim();
		const notes = String(form.get('notes') ?? '').trim();

		if (!dueDate) {
			return fail(400, { errorKey: 'adminBilling.create.errDueDate' });
		}

		const res = await apiFetch<AdminInvoice>(event, `/api/v1/admin/invoices/${event.params.id}`, {
			method: 'PATCH',
			body: { due_date: dueDate, notes }
		});
		if (res.error) return actionError(res);
		return { success: 'update' as const };
	},

	addPayment: async (event) => {
		const form = await event.request.formData();
		const amount = Math.trunc(Number(form.get('amount') ?? 0));
		const method = String(form.get('method') ?? '').trim();

		if (!Number.isFinite(amount) || amount <= 0) {
			return fail(400, { errorKey: 'adminBilling.detail.errAmount' });
		}
		if (!method) {
			return fail(400, { errorKey: 'adminBilling.detail.errMethod' });
		}

		const res = await apiFetch(event, `/api/v1/admin/invoices/${event.params.id}/payment`, {
			method: 'POST',
			body: { amount, method }
		});
		if (res.error) return actionError(res);
		return { success: 'addPayment' as const };
	},

	refund: async (event) => {
		const form = await event.request.formData();
		const reason = String(form.get('reason') ?? '').trim();

		const res = await apiFetch(event, `/api/v1/admin/invoices/${event.params.id}/refund`, {
			method: 'POST',
			body: { reason }
		});
		if (res.error) return actionError(res);
		return { success: 'refund' as const };
	},

	cancel: async (event) => {
		const res = await apiFetch(event, `/api/v1/admin/invoices/${event.params.id}/cancel`, {
			method: 'POST',
			body: {}
		});
		if (res.error) return actionError(res);
		return { success: 'cancel' as const };
	}
};
