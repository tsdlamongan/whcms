import { apiFetch } from '$lib/server/api';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';

/** domain.SnipePlan JSON tags. */
export interface SnipePlan {
	id: number;
	name: string;
	window_days: number;
	fee: number;
	success_fee: number;
	active: boolean;
	sort: number;
	created_at: string;
	updated_at: string;
}

export const load: PageServerLoad = async (event) => {
	const res = await apiFetch<SnipePlan[]>(event, '/api/v1/admin/snipe-plans');
	return { plans: res.data ?? [], listError: res.error?.message ?? null };
};

function intField(form: FormData, key: string): number {
	return Math.round(Number(form.get(key) ?? 0)) || 0;
}

export const actions: Actions = {
	/** Create (no id) or update (id) a plan. */
	save: async (event) => {
		const form = await event.request.formData();
		const id = intField(form, 'id');
		const body = {
			name: String(form.get('name') ?? '').trim(),
			window_days: intField(form, 'window_days'),
			fee: intField(form, 'fee'),
			success_fee: intField(form, 'success_fee'),
			sort: intField(form, 'sort'),
			active: form.get('active') === 'on'
		};
		if (!body.name)
			return fail(400, { action: 'save', errorMessage: 'The plan name is required.' });
		if (body.window_days < 1 || body.window_days > 365) {
			return fail(400, {
				action: 'save',
				errorMessage: 'The hunting window must be between 1 and 365 days.'
			});
		}
		if (body.fee < 0 || body.success_fee < 0) {
			return fail(400, { action: 'save', errorMessage: 'Prices cannot be negative.' });
		}

		const res = await apiFetch<SnipePlan>(
			event,
			id ? `/api/v1/admin/snipe-plans/${id}` : '/api/v1/admin/snipe-plans',
			{ method: id ? 'PUT' : 'POST', body }
		);
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, {
				action: 'save',
				errorMessage: res.error.message
			});
		}
		return { success: true, action: 'save', created: !id };
	},

	/** Flip a plan's active flag, keeping everything else as it is. */
	toggle: async (event) => {
		const form = await event.request.formData();
		const id = intField(form, 'id');
		const res = await apiFetch<SnipePlan>(event, `/api/v1/admin/snipe-plans/${id}`, {
			method: 'PUT',
			body: {
				name: String(form.get('name') ?? ''),
				window_days: intField(form, 'window_days'),
				fee: intField(form, 'fee'),
				success_fee: intField(form, 'success_fee'),
				sort: intField(form, 'sort'),
				active: form.get('active') !== 'true'
			}
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, {
				action: 'toggle',
				errorMessage: res.error.message
			});
		}
		return { success: true, action: 'toggle' };
	}
};
