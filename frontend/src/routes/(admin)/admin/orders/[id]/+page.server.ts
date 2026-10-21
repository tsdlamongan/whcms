import { clientNamesFor } from '$lib/server/adminNames';
import { apiFetch } from '$lib/server/api';
import { error, fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad, RequestEvent } from './$types';

/** domain.Order JSON tags. */
interface AdminOrder {
	id: number;
	order_number: string;
	client_id: number;
	status: string;
	subtotal: number;
	discount: number;
	tax_total: number;
	total: number;
	coupon_id?: number | null;
	ip: string;
	notes: string;
	created_at: string;
	updated_at: string;
}

/** domain.OrderItem JSON tags. */
interface AdminOrderItem {
	id: number;
	order_id: number;
	item_type: string;
	product_id?: number | null;
	description: string;
	domain: string;
	cycle: string;
	unit_price: number;
	setup_fee: number;
	service_id?: number | null;
	domain_id?: number | null;
}

/** Joined client summary if the detail aggregate embeds one. */
interface OrderClient {
	id: number;
	first_name?: string;
	last_name?: string;
	company?: string;
	email?: string;
}

/** Linked invoice summary if the detail aggregate embeds one. */
interface OrderInvoice {
	id: number;
	invoice_number?: string;
	status?: string;
	total?: number;
	due_date?: string;
}

function parseId(raw: string): number {
	const id = Number(raw);
	if (!Number.isInteger(id) || id <= 0) error(404, 'order not found');
	return id;
}

export const load: PageServerLoad = async (event) => {
	const id = parseId(event.params.id);

	// Detail aggregate may come flat or nested under {order} - normalize both.
	const res = await apiFetch<unknown>(event, `/api/v1/admin/orders/${id}`);
	if (res.status === 404) error(404, 'order not found');

	let order: AdminOrder | null = null;
	let items: AdminOrderItem[] = [];
	let client: OrderClient | null = null;
	let invoice: OrderInvoice | null = null;
	let invoiceId: number | null = null;

	if (res.data && typeof res.data === 'object') {
		const raw = res.data as Record<string, unknown>;
		order = (raw.order && typeof raw.order === 'object' ? raw.order : raw) as AdminOrder;
		const rawItems =
			raw.items ?? raw.order_items ?? (order as unknown as Record<string, unknown>).items;
		if (Array.isArray(rawItems)) items = rawItems as AdminOrderItem[];
		if (raw.client && typeof raw.client === 'object') client = raw.client as OrderClient;
		if (raw.invoice && typeof raw.invoice === 'object') invoice = raw.invoice as OrderInvoice;
		const rawInvoiceId = raw.invoice_id ?? invoice?.id;
		if (typeof rawInvoiceId === 'number' && rawInvoiceId > 0) invoiceId = rawInvoiceId;
	}

	// The detail aggregate carries only client_id; resolve the display name.
	const clientName = order
		? ((await clientNamesFor(event, [order.client_id])).get(order.client_id) ?? null)
		: null;

	return {
		orderId: id,
		order,
		items,
		client,
		clientName,
		invoice,
		invoiceId,
		loadError: res.error ? res.error.message : null
	};
};

async function orderAction(event: RequestEvent, action: 'accept' | 'cancel' | 'fraud') {
	const id = parseId(event.params.id);
	const res = await apiFetch(event, `/api/v1/admin/orders/${id}/${action}`, { method: 'POST' });
	if (res.error) {
		return fail(res.status >= 400 ? res.status : 400, {
			action,
			errorMessage: res.error.message
		});
	}
	return { action, ok: true };
}

export const actions: Actions = {
	accept: (event) => orderAction(event, 'accept'),
	cancel: (event) => orderAction(event, 'cancel'),
	fraud: (event) => orderAction(event, 'fraud')
};
