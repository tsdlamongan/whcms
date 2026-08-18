import { apiFetch } from '$lib/server/api';
import { error, fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';

/** Admin domain detail - domain.Domain JSON tags (+ optional joined display fields). */
interface AdminDomain {
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
	nameservers: string[] | null;
	id_protection: boolean;
	created_at: string;
	updated_at: string;
	client_name?: string;
	registrar_name?: string;
}

/** POST /admin/domains/:id/renew may return the created renewal invoice. */
interface RenewResult {
	id?: number;
	invoice_id?: number;
	invoice_number?: string;
}

export const load: PageServerLoad = async (event) => {
	const res = await apiFetch<AdminDomain>(event, `/api/v1/admin/domains/${event.params.id}`);
	if (res.error || !res.data) {
		error(res.status >= 400 ? res.status : 500, res.error?.message ?? 'error');
	}
	return { domain: res.data };
};

export const actions: Actions = {
	sync: async (event) => {
		const res = await apiFetch<unknown>(event, `/api/v1/admin/domains/${event.params.id}/sync`, {
			method: 'POST'
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				action: 'sync',
				errorMessage: res.error.message
			});
		}
		return { success: true, action: 'sync' };
	},

	renew: async (event) => {
		const res = await apiFetch<RenewResult>(
			event,
			`/api/v1/admin/domains/${event.params.id}/renew`,
			{ method: 'POST' }
		);
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				action: 'renew',
				errorMessage: res.error.message
			});
		}
		return {
			success: true,
			action: 'renew',
			invoiceId: res.data?.invoice_id ?? res.data?.id ?? null
		};
	},

	nameservers: async (event) => {
		const form = await event.request.formData();
		const nameservers: string[] = [];
		for (const key of ['ns1', 'ns2', 'ns3', 'ns4']) {
			const v = String(form.get(key) ?? '').trim();
			if (v) nameservers.push(v);
		}
		if (nameservers.length < 2) {
			return fail(400, { action: 'nameservers', errorKey: 'adminops.domains.nsRequired' });
		}

		const res = await apiFetch<unknown>(event, `/api/v1/admin/domains/${event.params.id}`, {
			method: 'PATCH',
			body: { nameservers }
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				action: 'nameservers',
				errorMessage: res.error.message
			});
		}
		return { success: true, action: 'nameservers' };
	},

	settings: async (event) => {
		const form = await event.request.formData();
		const status = String(form.get('status') ?? '').trim();
		const autoRenew = form.get('auto_renew') === 'on';

		const body: Record<string, unknown> = { auto_renew: autoRenew };
		if (status) body.status = status;
		// Billing fields (added for manually-recorded existing domains): the
		// backend treats an absent field as untouched and an empty date string
		// as "clear", so always send whatever the form carries.
		for (const key of ['registration_date', 'expiry_date', 'next_due_date'] as const) {
			const v = form.get(key);
			if (v !== null) body[key] = String(v).trim();
		}
		const amountRaw = form.get('recurring_amount');
		if (amountRaw !== null && String(amountRaw).trim() !== '') {
			const amount = Math.trunc(Number(amountRaw));
			if (Number.isFinite(amount) && amount >= 0) body.recurring_amount = amount;
		}
		const cycle = String(form.get('billing_cycle') ?? '').trim();
		if (cycle) body.billing_cycle = cycle;

		const res = await apiFetch<unknown>(event, `/api/v1/admin/domains/${event.params.id}`, {
			method: 'PATCH',
			body
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				action: 'settings',
				errorMessage: res.error.message
			});
		}
		return { success: true, action: 'settings' };
	}
};
