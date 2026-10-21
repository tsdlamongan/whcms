import { apiFetch } from '$lib/server/api';
import { fail, type RequestEvent } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';

/** domain.TicketDepartment JSON tags. */
export interface Department {
	id: number;
	name: string;
	email: string;
	active: boolean;
	sort: number;
	created_at: string;
	updated_at: string;
}

export const load: PageServerLoad = async (event) => {
	// Admin list (includes inactive departments) - guessed RESTful path.
	const res = await apiFetch<Department[]>(event, '/api/v1/admin/ticket-departments', {
		query: { per_page: 1000 }
	});

	return {
		departments: res.data ?? [],
		listError: res.error?.message ?? null
	};
};

interface DeptInput {
	name: string;
	email: string;
	sort: number;
	active: boolean;
}

function readInput(form: FormData): { input: DeptInput; errors: Record<string, string> } {
	const name = String(form.get('name') ?? '').trim();
	const email = String(form.get('email') ?? '').trim();
	const sort = Number(form.get('sort') ?? 0) || 0;
	const active = form.get('active') === 'on';

	const errors: Record<string, string> = {};
	if (!name) errors.name = 'adminsupport.common.requiredField';
	if (!email) errors.email = 'adminsupport.common.requiredField';

	return { input: { name, email, sort, active }, errors };
}

async function save(event: RequestEvent, method: 'POST', path: string) {
	const form = await event.request.formData();
	const { input, errors } = readInput(form);
	if (Object.keys(errors).length > 0) {
		return fail(400, { fieldErrors: errors, values: input });
	}

	const res = await apiFetch(event, path, { method, body: input });
	if (res.error) {
		return fail(res.status >= 400 ? res.status : 500, {
			errorMessage: res.error.message,
			values: input
		});
	}
	return { saved: true };
}

export const actions: Actions = {
	create: (event) => save(event, 'POST', '/api/v1/admin/ticket-departments'),

	update: async (event) => {
		const form = await event.request.formData();
		const id = Number(form.get('id') ?? 0);
		if (!Number.isInteger(id) || id <= 0) {
			return fail(400, { errorMessage: 'invalid department id' });
		}
		const { input, errors } = readInput(form);
		if (Object.keys(errors).length > 0) {
			return fail(400, { fieldErrors: errors, values: input });
		}
		const res = await apiFetch(event, `/api/v1/admin/ticket-departments/${id}`, {
			// The API registers department updates as PATCH (tickets/handler.go).
			method: 'PATCH',
			body: input
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				errorMessage: res.error.message,
				values: input
			});
		}
		return { saved: true, updated: true };
	},

	delete: async (event) => {
		const form = await event.request.formData();
		const id = Number(form.get('id') ?? 0);
		if (!Number.isInteger(id) || id <= 0) {
			return fail(400, { deleteErrorMessage: 'invalid department id' });
		}
		const res = await apiFetch(event, `/api/v1/admin/ticket-departments/${id}`, {
			method: 'DELETE'
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				deleteErrorMessage: res.error.message
			});
		}
		return { deleted: true };
	}
};
