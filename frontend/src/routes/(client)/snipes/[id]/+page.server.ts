import { apiFetch } from '$lib/server/api';
import { error, fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import type { ClientEvent, ClientSnipe } from '../shared';

interface SnipeDetail {
	snipe: ClientSnipe;
	events: ClientEvent[];
}

export const load: PageServerLoad = async (event) => {
	const res = await apiFetch<SnipeDetail>(event, `/api/v1/snipes/${event.params.id}`);
	if (res.error || !res.data) {
		error(res.status >= 400 ? res.status : 500, res.error?.message ?? 'error');
	}
	return { snipe: res.data.snipe, events: res.data.events };
};

export const actions: Actions = {
	cancel: async (event) => {
		const res = await apiFetch<unknown>(event, `/api/v1/snipes/${event.params.id}/cancel`, {
			method: 'POST'
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, { errorMessage: res.error.message });
		}
		return { success: true };
	}
};
