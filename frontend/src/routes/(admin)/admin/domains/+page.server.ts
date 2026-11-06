import { withClientNames } from '$lib/server/adminNames';
import { apiFetch } from '$lib/server/api';
import type { PageServerLoad } from './$types';

/** Admin domain list row - domain.Domain JSON tags (+ optional joined display fields). */
export interface AdminDomainRow {
	id: number;
	client_id: number;
	registrar_id: number;
	name: string;
	status: string;
	registration_date: string | null;
	expiry_date: string | null;
	next_due_date: string | null;
	recurring_amount: number;
	billing_cycle: string;
	auto_renew: boolean;
	created_at: string;
	client_name?: string;
	registrar_name?: string;
}

export const load: PageServerLoad = async (event) => {
	const q = event.url.searchParams;
	const page = Math.max(1, Number(q.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(q.get('per_page')) || 10));
	const search = (q.get('search') ?? '').trim();
	const status = q.get('status') ?? '';

	const res = await apiFetch<AdminDomainRow[]>(event, '/api/v1/admin/domains', {
		query: {
			page,
			per_page: perPage,
			search: search || undefined,
			status: status || undefined
		}
	});

	return {
		domains: await withClientNames(event, res.data ?? []),
		meta: res.meta,
		listError: res.error ? res.error.message : null,
		page,
		perPage,
		search,
		status
	};
};
