import { clientNamesFor } from '$lib/server/adminNames';
import { apiFetch } from '$lib/server/api';
import { error, fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import type { AdminSnipe, SnipeEvent } from '../types';

/** GET /admin/snipes/:id - snipe.Detail. */
interface SnipeDetail {
	snipe: AdminSnipe;
	events: SnipeEvent[];
}

export const load: PageServerLoad = async (event) => {
	const res = await apiFetch<SnipeDetail>(event, `/api/v1/admin/snipes/${event.params.id}`);
	if (res.error || !res.data) {
		error(res.status >= 400 ? res.status : 500, res.error?.message ?? 'error');
	}
	const { snipe, events } = res.data;
	const name = (await clientNamesFor(event, [snipe.client_id])).get(snipe.client_id);
	return { snipe: name ? { ...snipe, client_name: name } : snipe, events };
};

export const actions: Actions = {
	cancel: async (event) => {
		const form = await event.request.formData();
		const res = await apiFetch<unknown>(event, `/api/v1/admin/snipes/${event.params.id}/cancel`, {
			method: 'POST',
			body: { refund_fee: form.get('refund_fee') === 'on' }
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				action: 'cancel',
				errorMessage: res.error.message
			});
		}
		return { success: true, action: 'cancel' };
	}
};
