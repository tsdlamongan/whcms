import { withClientNames } from '$lib/server/adminNames';
import { apiFetch } from '$lib/server/api';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import type { StaffUser, Ticket, TicketDepartment } from './types';

/** Upper bound on one bulk request. */
const BULK_MAX = 200;

export interface BulkFailure {
	id: number;
	message: string;
}

export const load: PageServerLoad = async (event) => {
	const q = event.url.searchParams;
	const page = Math.max(1, Number(q.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(q.get('per_page')) || 10));
	const status = q.get('status') ?? '';
	const departmentId = q.get('department_id') ?? '';
	const priority = q.get('priority') ?? '';
	const assigned = q.get('assigned_user_id') ?? '';
	const search = (q.get('search') ?? '').trim();

	const [ticketsRes, deptRes, staffRes] = await Promise.all([
		apiFetch<Ticket[]>(event, '/api/v1/admin/tickets', {
			query: {
				page,
				per_page: perPage,
				status: status || undefined,
				department_id: departmentId || undefined,
				priority: priority || undefined,
				assigned_user_id: assigned || undefined,
				search: search || undefined
			}
		}),
		apiFetch<TicketDepartment[]>(event, '/api/v1/ticket-departments'),
		apiFetch<StaffUser[]>(event, '/api/v1/admin/staff')
	]);

	return {
		tickets: await withClientNames(event, ticketsRes.data ?? []),
		meta: ticketsRes.meta,
		listError: ticketsRes.error?.message ?? null,
		departments: deptRes.data ?? [],
		staff: (staffRes.data ?? []).filter((u) => u.role !== 'client'),
		page,
		perPage,
		filters: { status, departmentId, priority, assigned, search }
	};
};

export const actions: Actions = {
	/**
	 * "With Selected: Close". Closing is the `closed` status (POST
	 * /admin/tickets/:id/status). A ticket that is already closed answers
	 * CONFLICT, which is counted as `skipped` rather than a failure.
	 */
	close: async (event) => {
		const form = await event.request.formData();
		let ids: number[] = [];
		try {
			const parsed = JSON.parse(String(form.get('ids') ?? '[]')) as unknown;
			if (Array.isArray(parsed)) {
				ids = parsed.map(Number).filter((n) => Number.isInteger(n) && n > 0);
			}
		} catch {
			ids = [];
		}
		ids = [...new Set(ids)];
		if (ids.length === 0) {
			return fail(400, { op: 'bulk', errorMessage: 'Select at least one ticket.' });
		}
		if (ids.length > BULK_MAX) {
			return fail(400, {
				op: 'bulk',
				errorMessage: `Select at most ${BULK_MAX} tickets at a time.`
			});
		}

		let done = 0;
		let skipped = 0;
		const failures: BulkFailure[] = [];
		for (const id of ids) {
			const res = await apiFetch(event, `/api/v1/admin/tickets/${id}/status`, {
				method: 'POST',
				body: { status: 'closed' }
			});
			if (!res.error) done++;
			else if (res.error.code === 'CONFLICT') skipped++;
			else failures.push({ id, message: res.error.message });
		}

		return { op: 'bulk' as const, done, skipped, failures };
	}
};
