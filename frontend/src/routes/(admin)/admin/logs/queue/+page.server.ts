import { apiFetch } from '$lib/server/api';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';

/** ports.ModuleAction JSON tags - the "Pending Module Actions" queue (WHMCS's
 *  Module Queue equivalent): provisioning + domain-registrar jobs that
 *  exhausted their automatic retries (or are still auto-retrying) and need a
 *  manual admin retry/dismiss. Payload is already redacted server-side for
 *  any type carrying a secret (panel password, EPP code). */
export interface ModuleActionRow {
	id: string;
	queue: string;
	type: string;
	payload: unknown;
	state: 'archived' | 'retry' | string;
	max_retry: number;
	retried: number;
	last_err: string;
	last_failed_at: string | null;
	next_process_at: string | null;
}

export const load: PageServerLoad = async (event) => {
	const q = event.url.searchParams;
	const page = Math.max(1, Number(q.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(q.get('per_page')) || 10));
	const type = q.get('type') ?? '';
	const state = q.get('state') ?? '';

	const res = await apiFetch<ModuleActionRow[]>(event, '/api/v1/admin/logs/queue', {
		query: {
			page,
			per_page: perPage,
			type: type || undefined,
			state: state || undefined
		}
	});

	return {
		actions: res.data ?? [],
		meta: res.meta,
		listError: res.error?.message ?? null,
		page,
		perPage,
		filters: { type, state }
	};
};

export const actions: Actions = {
	retry: async (event) => {
		const form = await event.request.formData();
		const queue = String(form.get('queue') ?? '').trim();
		const id = String(form.get('id') ?? '').trim();
		if (!queue || !id) {
			return fail(400, { op: 'retry', errorMessage: 'queue and id are required' });
		}
		const res = await apiFetch(event, `/api/v1/admin/logs/queue/${queue}/${id}/retry`, {
			method: 'POST'
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				op: 'retry',
				errorMessage: res.error.message,
				targetId: id
			});
		}
		return { op: 'retry', success: true, targetId: id };
	},

	delete: async (event) => {
		const form = await event.request.formData();
		const queue = String(form.get('queue') ?? '').trim();
		const id = String(form.get('id') ?? '').trim();
		if (!queue || !id) {
			return fail(400, { op: 'delete', errorMessage: 'queue and id are required' });
		}
		const res = await apiFetch(event, `/api/v1/admin/logs/queue/${queue}/${id}`, {
			method: 'DELETE'
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				op: 'delete',
				errorMessage: res.error.message,
				targetId: id
			});
		}
		return { op: 'delete', success: true, targetId: id };
	},

	dismissAll: async (event) => {
		const form = await event.request.formData();
		const type = String(form.get('type') ?? '').trim();
		const state = String(form.get('state') ?? '').trim();
		const res = await apiFetch<{ dismissed: number }>(event, '/api/v1/admin/logs/queue', {
			method: 'DELETE',
			query: { type: type || undefined, state: state || undefined }
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				op: 'dismissAll',
				errorMessage: res.error.message
			});
		}
		return { op: 'dismissAll', success: true, dismissedCount: res.data?.dismissed ?? 0 };
	}
};
