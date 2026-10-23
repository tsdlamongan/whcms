import { withClientNames } from '$lib/server/adminNames';
import { apiFetch } from '$lib/server/api';
import type { PageServerLoad } from './$types';

/** Admin order list row - domain.Order JSON tags (+ optional joined client name). */
interface AdminOrderRow {
	id: number;
	order_number: string;
	client_id: number;
	client_name?: string;
	status: string;
	subtotal: number;
	discount: number;
	tax_total: number;
	total: number;
	created_at: string;
}

export const load: PageServerLoad = async (event) => {
	const q = event.url.searchParams;
	const page = Math.max(1, Number(q.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(q.get('per_page')) || 10));
	const search = (q.get('search') ?? '').trim();
	const status = q.get('status') ?? '';

	const res = await apiFetch<AdminOrderRow[]>(event, '/api/v1/admin/orders', {
		query: {
			page,
			per_page: perPage,
			search: search || undefined,
			status: status || undefined
		}
	});

	return {
		orders: await withClientNames(event, res.data ?? []),
		meta: res.meta,
		listError: res.error ? res.error.message : null,
		page,
		perPage,
		search,
		status
	};
};
