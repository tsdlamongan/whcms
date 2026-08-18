import { apiFetch } from '$lib/server/api';
import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import type { AdminClientLite } from '../../invoices/types';

interface CreatedDomain {
	id: number;
}

export const load: PageServerLoad = async (event) => {
	const search = (event.url.searchParams.get('client_search') ?? '').trim();

	const res = await apiFetch<AdminClientLite[]>(event, '/api/v1/admin/clients', {
		query: { search: search || undefined, per_page: 20, page: 1 }
	});

	return {
		clientSearch: search,
		clients: res.data ?? [],
		clientsError: res.error?.message ?? null
	};
};

export const actions: Actions = {
	default: async (event) => {
		const form = await event.request.formData();
		const clientId = Number(form.get('client_id') ?? 0);
		const name = String(form.get('name') ?? '').trim();
		const registrationDate = String(form.get('registration_date') ?? '').trim();
		const expiryDate = String(form.get('expiry_date') ?? '').trim();
		const nextDueDate = String(form.get('next_due_date') ?? '').trim();
		const recurringAmount = Math.trunc(Number(form.get('recurring_amount') ?? 0)) || 0;
		const autoRenew = form.get('auto_renew') === 'on';
		const nameservers: string[] = [];
		for (const key of ['ns1', 'ns2', 'ns3', 'ns4']) {
			const v = String(form.get(key) ?? '').trim();
			if (v) nameservers.push(v);
		}

		const values = {
			clientId,
			name,
			registrationDate,
			expiryDate,
			nextDueDate,
			recurringAmount,
			autoRenew,
			ns1: nameservers[0] ?? '',
			ns2: nameservers[1] ?? '',
			ns3: nameservers[2] ?? '',
			ns4: nameservers[3] ?? ''
		};

		if (!clientId || clientId < 1) {
			return fail(400, { errorMessage: 'Please select a client first.', ...values });
		}
		if (!name) {
			return fail(400, { errorMessage: 'The domain name is required.', ...values });
		}
		if (!nextDueDate) {
			return fail(400, {
				errorMessage: 'The next due date is required so renewal invoicing works.',
				...values
			});
		}
		if (nameservers.length === 1) {
			return fail(400, {
				errorMessage: 'Provide at least 2 nameservers (or leave all empty).',
				...values
			});
		}

		const res = await apiFetch<CreatedDomain>(event, '/api/v1/admin/domains', {
			method: 'POST',
			body: {
				client_id: clientId,
				name,
				registration_date: registrationDate || undefined,
				expiry_date: expiryDate || undefined,
				next_due_date: nextDueDate,
				recurring_amount: recurringAmount,
				auto_renew: autoRenew,
				nameservers: nameservers.length > 0 ? nameservers : undefined
			}
		});

		if (res.error || !res.data) {
			return fail(res.status >= 400 ? res.status : 500, {
				errorMessage: res.error?.message ?? 'Failed to add the domain',
				...values
			});
		}

		redirect(303, `/admin/domains/${res.data.id}`);
	}
};
