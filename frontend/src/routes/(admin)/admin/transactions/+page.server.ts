import { withInvoiceInfo } from '$lib/server/adminNames';
import { apiFetch, type PageMeta } from '$lib/server/api';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import {
	GATEWAYS,
	TRANSACTION_STATUSES,
	type AdminTransaction,
	type Gateway,
	type TransactionStatus
} from '../invoices/types';

export const load: PageServerLoad = async (event) => {
	const { url } = event;

	const page = Math.max(1, Number(url.searchParams.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(url.searchParams.get('per_page')) || 10));
	const rawStatus = url.searchParams.get('status') ?? '';
	const status = (TRANSACTION_STATUSES as string[]).includes(rawStatus)
		? (rawStatus as TransactionStatus)
		: '';
	const rawGateway = url.searchParams.get('gateway') ?? '';
	const gateway = (GATEWAYS as string[]).includes(rawGateway) ? (rawGateway as Gateway) : '';
	const search = (url.searchParams.get('search') ?? '').trim();
	const dateFrom = url.searchParams.get('date_from') ?? '';
	const dateTo = url.searchParams.get('date_to') ?? '';

	let transactions: AdminTransaction[] = [];
	let meta: PageMeta | null = null;
	let errorMessage: string | null = null;

	const res = await apiFetch<AdminTransaction[]>(event, '/api/v1/admin/transactions', {
		query: {
			page,
			per_page: perPage,
			status: status || undefined,
			gateway: gateway || undefined,
			search: search || undefined,
			date_from: dateFrom || undefined,
			date_to: dateTo || undefined
		}
	});

	if (res.error) {
		errorMessage = res.error.message;
	} else {
		transactions = await withInvoiceInfo(event, res.data ?? []);
		meta = res.meta;
	}

	return {
		transactions,
		page: meta?.page ?? page,
		perPage: meta?.per_page ?? perPage,
		total: meta?.total ?? 0,
		status,
		gateway,
		search,
		dateFrom,
		dateTo,
		statusOptions: TRANSACTION_STATUSES,
		gatewayOptions: GATEWAYS,
		errorMessage
	};
};

export const actions: Actions = {
	// Settles a pending manual bank-transfer transaction once an admin has
	// confirmed the funds arrived - POST /admin/transactions/:id/confirm.
	// Distinct from the freeform "Add Payment" flow on the invoice detail
	// page: this only ever succeeds against a gateway=manual, status=pending
	// row (409 otherwise), settling that exact transaction rather than
	// recording a new one.
	confirm: async (event) => {
		const form = await event.request.formData();
		const id = Number(form.get('id'));
		if (!id) return fail(400, { errorMessage: 'invalid transaction id' });

		const res = await apiFetch(event, `/api/v1/admin/transactions/${id}/confirm`, {
			method: 'POST',
			body: {}
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, { errorMessage: res.error.message });
		}
		return { success: true };
	}
};
