import { apiFetch } from '$lib/server/api';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import { PERMISSION_MODULES, mergePermissions, parsePermissions } from './permissions';

/** domain.User JSON tags (staff/admin rows; password never serialized). */
export interface StaffRow {
	id: number;
	email: string;
	role: 'admin' | 'staff' | 'client';
	status: 'active' | 'inactive';
	permissions: unknown;
	twofa_enabled: boolean;
	last_login_at: string | null;
	created_at: string;
}

/** GET/POST /api/v1/auth/confirm-password (auth.PasswordConfirmation). */
interface PasswordConfirmation {
	confirmed: boolean;
	expires_at?: string;
}

const NOT_CONFIRMED_MESSAGE = 'Please confirm your password to continue.';

/**
 * Administrators is a sensitive page, so it sits behind a "sudo mode": the
 * signed-in user re-types their password, the API verifies it and keeps a
 * short-lived confirmation server-side. Both the loader and every mutating
 * action ask the API whether that window is still open - nothing the browser
 * sends can unlock the page by itself.
 */
async function isPasswordConfirmed(event: Parameters<typeof apiFetch>[0]): Promise<boolean> {
	const res = await apiFetch<PasswordConfirmation>(event, '/api/v1/auth/confirm-password');
	return res.data?.confirmed === true;
}

export const load: PageServerLoad = async (event) => {
	if (!(await isPasswordConfirmed(event))) {
		return { confirmed: false, staff: [] as StaffRow[], listError: null as string | null };
	}

	const res = await apiFetch<StaffRow[]>(event, '/api/v1/admin/staff', {
		query: { per_page: 1000 }
	});

	return {
		confirmed: true,
		staff: (res.data ?? []).filter((u) => u.role !== 'client'),
		listError: res.error?.message ?? null
	};
};

interface StaffInput {
	email: string;
	password: string;
	status: 'active' | 'inactive';
	permissions: Record<string, boolean>;
}

function readInput(form: FormData): StaffInput {
	const permissions: Record<string, boolean> = {};
	for (const mod of PERMISSION_MODULES) {
		permissions[mod] = form.get(`perm_${mod}`) === 'on';
	}
	return {
		email: String(form.get('email') ?? '').trim(),
		password: String(form.get('password') ?? ''),
		status: form.get('active') === 'on' ? 'active' : 'inactive',
		permissions
	};
}

export const actions: Actions = {
	// Opens the confirmation window (POST /auth/confirm-password verifies the
	// password server-side, with its own attempt lockout).
	unlock: async (event) => {
		const form = await event.request.formData();
		const password = String(form.get('password') ?? '');
		if (!password) return fail(400, { gateError: 'Password is required.' });

		const res = await apiFetch(event, '/api/v1/auth/confirm-password', {
			method: 'POST',
			body: { password }
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, { gateError: res.error.message });
		}
		return { unlocked: true };
	},

	create: async (event) => {
		if (!(await isPasswordConfirmed(event))) {
			return fail(403, { errorMessage: NOT_CONFIRMED_MESSAGE, notConfirmed: true });
		}
		const form = await event.request.formData();
		const input = readInput(form);

		const errors: Record<string, string> = {};
		if (!input.email) errors.email = 'adminsupport.staff.emailRequired';
		if (!input.password) errors.password = 'adminsupport.staff.passwordRequired';
		if (Object.keys(errors).length > 0) {
			return fail(400, { fieldErrors: errors, values: { email: input.email } });
		}

		const res = await apiFetch(event, '/api/v1/admin/staff', {
			method: 'POST',
			body: {
				email: input.email,
				password: input.password,
				status: input.status,
				permissions: input.permissions
			}
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				errorMessage: res.error.message,
				values: { email: input.email }
			});
		}
		return { saved: true };
	},

	update: async (event) => {
		if (!(await isPasswordConfirmed(event))) {
			return fail(403, { errorMessage: NOT_CONFIRMED_MESSAGE, notConfirmed: true });
		}
		const form = await event.request.formData();
		const id = Number(form.get('id') ?? 0);
		if (!Number.isInteger(id) || id <= 0) {
			return fail(400, { errorMessage: 'invalid staff id' });
		}
		const input = readInput(form);

		const errors: Record<string, string> = {};
		if (!input.email) errors.email = 'adminsupport.staff.emailRequired';
		if (Object.keys(errors).length > 0) {
			return fail(400, { fieldErrors: errors, values: { email: input.email } });
		}

		// PATCH replaces the stored permission map wholesale: fetch it and overlay
		// the modules this form shows so hidden keys are preserved.
		const current = await apiFetch<StaffRow>(event, `/api/v1/admin/staff/${id}`);
		if (current.error) {
			return fail(current.status >= 400 ? current.status : 500, {
				errorMessage: current.error.message,
				values: { email: input.email }
			});
		}

		const body: Record<string, unknown> = {
			email: input.email,
			status: input.status,
			permissions: mergePermissions(parsePermissions(current.data?.permissions), input.permissions)
		};
		// Blank password means "keep the current one".
		if (input.password) body.password = input.password;

		const res = await apiFetch(event, `/api/v1/admin/staff/${id}`, {
			method: 'PATCH',
			body
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				errorMessage: res.error.message,
				values: { email: input.email }
			});
		}
		return { saved: true, updated: true };
	},

	// The API has no hard delete for staff: an account is deactivated (it can no
	// longer sign in) and can be re-enabled later from the edit form.
	deactivate: async (event) => {
		if (!(await isPasswordConfirmed(event))) {
			return fail(403, { deactivateErrorMessage: NOT_CONFIRMED_MESSAGE, notConfirmed: true });
		}
		const form = await event.request.formData();
		const id = Number(form.get('id') ?? 0);
		if (!Number.isInteger(id) || id <= 0) {
			return fail(400, { deactivateErrorMessage: 'invalid staff id' });
		}

		const res = await apiFetch(event, `/api/v1/admin/staff/${id}/deactivate`, { method: 'POST' });
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				deactivateErrorMessage: res.error.message
			});
		}
		return { deactivated: true };
	}
};
