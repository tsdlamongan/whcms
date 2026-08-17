import { apiFetch } from '$lib/server/api';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad, RequestEvent } from './$types';

/** provisioning.CancellationRequestView JSON tags. */
export interface CancellationRequestRow {
	id: number;
	service_id: number;
	client_id: number;
	mode: string;
	reason: string;
	status: string;
	requested_at: string;
	decided_at: string | null;
	decided_by: number | null;
	service_domain?: string;
	client_name?: string;
}

export const load: PageServerLoad = async (event) => {
	const q = event.url.searchParams;
	const page = Math.max(1, Number(q.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(q.get('per_page')) || 10));
	const status = q.get('status') ?? '';

	const res = await apiFetch<CancellationRequestRow[]>(
		event,
		'/api/v1/admin/services/cancellation-requests',
		{ query: { page, per_page: perPage, status: status || undefined } }
	);

	return {
		rows: res.data ?? [],
		meta: res.meta,
		listError: res.error?.message ?? null,
		page,
		perPage,
		filters: { status }
	};
};

async function decide(event: RequestEvent, op: 'accept' | 'reject') {
	const form = await event.request.formData();
	const id = String(form.get('id') ?? '').trim();
	if (!id) {
		return fail(400, { op, errorMessage: 'id is required' });
	}
	const res = await apiFetch(
		event,
		`/api/v1/admin/services/cancellation-requests/${id}/${op}`,
		{ method: 'POST' }
	);
	if (res.error) {
		return fail(res.status >= 400 ? res.status : 500, {
			op,
			errorMessage: res.error.message,
			targetId: id
		});
	}
	return { op, success: true, targetId: id };
}

export const actions: Actions = {
	accept: (event) => decide(event, 'accept'),
	reject: (event) => decide(event, 'reject')
};
