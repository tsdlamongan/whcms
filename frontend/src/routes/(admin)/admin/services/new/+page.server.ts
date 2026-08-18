import { apiFetch } from '$lib/server/api';
import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import type { AdminClientLite } from '../../invoices/types';

interface ProductOption {
	id: number;
	name: string;
	module?: string;
}

interface ServerOption {
	id: number;
	name: string;
	module?: string;
}

interface CreatedService {
	id: number;
}

export const load: PageServerLoad = async (event) => {
	const search = (event.url.searchParams.get('client_search') ?? '').trim();

	const [clientsRes, productsRes, serversRes] = await Promise.all([
		apiFetch<AdminClientLite[]>(event, '/api/v1/admin/clients', {
			query: { search: search || undefined, per_page: 20, page: 1 }
		}),
		apiFetch<ProductOption[]>(event, '/api/v1/admin/products', {
			query: { page: 1, per_page: 100 }
		}),
		apiFetch<ServerOption[]>(event, '/api/v1/admin/servers', {
			query: { page: 1, per_page: 100 }
		})
	]);

	return {
		clientSearch: search,
		clients: clientsRes.data ?? [],
		clientsError: clientsRes.error?.message ?? null,
		products: productsRes.data ?? [],
		servers: serversRes.data ?? []
	};
};

export const actions: Actions = {
	default: async (event) => {
		const form = await event.request.formData();
		const clientId = Number(form.get('client_id') ?? 0);
		const productId = Number(form.get('product_id') ?? 0);
		const serverId = Number(form.get('server_id') ?? 0);
		const domain = String(form.get('domain') ?? '').trim();
		const username = String(form.get('username') ?? '').trim();
		const password = String(form.get('password') ?? '');
		const billingCycle = String(form.get('billing_cycle') ?? '').trim();
		const recurringAmount = Math.trunc(Number(form.get('recurring_amount') ?? 0)) || 0;
		const nextDueDate = String(form.get('next_due_date') ?? '').trim();
		const registrationDate = String(form.get('registration_date') ?? '').trim();
		const notes = String(form.get('notes') ?? '').trim();

		const values = {
			clientId,
			productId,
			serverId,
			domain,
			username,
			billingCycle,
			recurringAmount,
			nextDueDate,
			registrationDate,
			notes
		};

		if (!clientId || clientId < 1) {
			return fail(400, { errorMessage: 'Please select a client first.', ...values });
		}
		if (!productId || productId < 1) {
			return fail(400, { errorMessage: 'Please select a product.', ...values });
		}
		if (!billingCycle) {
			return fail(400, { errorMessage: 'The billing cycle is required.', ...values });
		}
		if (billingCycle !== 'one_time' && !nextDueDate) {
			return fail(400, {
				errorMessage: 'A next due date is required for recurring billing cycles.',
				...values
			});
		}

		const res = await apiFetch<CreatedService>(event, '/api/v1/admin/services', {
			method: 'POST',
			body: {
				client_id: clientId,
				product_id: productId,
				server_id: serverId > 0 ? serverId : undefined,
				domain: domain || undefined,
				username: username || undefined,
				password: password || undefined,
				billing_cycle: billingCycle,
				recurring_amount: recurringAmount,
				next_due_date: nextDueDate || undefined,
				registration_date: registrationDate || undefined,
				notes: notes || undefined
			}
		});

		if (res.error || !res.data) {
			return fail(res.status >= 400 ? res.status : 500, {
				errorMessage: res.error?.message ?? 'Failed to add the service',
				...values
			});
		}

		redirect(303, `/admin/services/${res.data.id}`);
	}
};
