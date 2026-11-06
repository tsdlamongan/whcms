import { withClientNames } from '$lib/server/adminNames';
import { apiFetch } from '$lib/server/api';
import type { PageServerLoad } from './$types';

/** Admin service list row - domain.Service JSON tags (+ optional joined display fields). */
export interface AdminServiceRow {
	id: number;
	client_id: number;
	product_id: number;
	server_id: number | null;
	domain: string;
	username: string;
	status: string;
	billing_cycle: string;
	recurring_amount: number;
	setup_fee: number;
	next_due_date: string | null;
	registration_date: string | null;
	created_at: string;
	// Joined display fields (backend may include them; fall back to #id).
	product_name?: string;
	client_name?: string;
	server_name?: string;
}

interface ProductOption {
	id: number;
	name: string;
}

interface ServerOption {
	id: number;
	name: string;
}

export const load: PageServerLoad = async (event) => {
	const q = event.url.searchParams;
	const page = Math.max(1, Number(q.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(q.get('per_page')) || 10));
	const status = q.get('status') ?? '';
	const productId = q.get('product_id') ?? '';
	const serverId = q.get('server_id') ?? '';

	const [servicesRes, productsRes, serversRes] = await Promise.all([
		apiFetch<AdminServiceRow[]>(event, '/api/v1/admin/services', {
			query: {
				page,
				per_page: perPage,
				status: status || undefined,
				product_id: productId || undefined,
				server_id: serverId || undefined
			}
		}),
		apiFetch<ProductOption[]>(event, '/api/v1/admin/products', {
			query: { page: 1, per_page: 1000 }
		}),
		apiFetch<ServerOption[]>(event, '/api/v1/admin/servers', {
			query: { page: 1, per_page: 1000 }
		})
	]);

	return {
		services: await withClientNames(event, servicesRes.data ?? []),
		meta: servicesRes.meta,
		listError: servicesRes.error ? servicesRes.error.message : null,
		products: productsRes.data ?? [],
		servers: serversRes.data ?? [],
		page,
		perPage,
		status,
		productId,
		serverId
	};
};
