import { apiFetch } from '$lib/server/api';
import { error, fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad, RequestEvent } from './$types';

/** Pending upgrade payload stored on services.pending_upgrade (domain.ServiceUpgrade). */
interface ServiceUpgrade {
	product_id: number;
	cycle: string;
	recurring_amount: number;
	invoice_id: number;
}

/** Admin service detail - domain.Service JSON tags (+ optional joined display fields). */
interface AdminService {
	id: number;
	client_id: number;
	order_item_id: number | null;
	product_id: number;
	server_id: number | null;
	domain: string;
	username: string;
	status: string;
	billing_cycle: string;
	recurring_amount: number;
	setup_fee: number;
	next_due_date: string | null;
	registration_date: string | null;
	terminated_at: string | null;
	suspend_reason: string;
	pending_upgrade: ServiceUpgrade | null;
	notes: string;
	created_at: string;
	updated_at: string;
	product_name?: string;
	client_name?: string;
	server_name?: string;
}

interface ProductOption {
	id: number;
	name: string;
	// The backend Upgrade endpoint accepts spec selections for custom-spec
	// products, but this admin form has no spec-knob UI yet - the Upgrade
	// picker filters configurable products out (clients use the client-area
	// upgrade flow); Change Package (a raw, no-invoice swap) still allows them.
	configurable?: boolean;
}

/** billing.provisioning UpgradeResult JSON tags (POST .../upgrade response). */
interface UpgradeResult {
	applied: boolean;
	prorated_diff: number;
	credit_issued: number;
	invoice?: { id: number; invoice_number: string } | null;
}

interface ServerOption {
	id: number;
	name: string;
}

export const load: PageServerLoad = async (event) => {
	const [serviceRes, productsRes, serversRes] = await Promise.all([
		apiFetch<AdminService>(event, `/api/v1/admin/services/${event.params.id}`),
		apiFetch<ProductOption[]>(event, '/api/v1/admin/products', {
			query: { page: 1, per_page: 100 }
		}),
		apiFetch<ServerOption[]>(event, '/api/v1/admin/servers', {
			query: { page: 1, per_page: 100 }
		})
	]);

	if (serviceRes.error || !serviceRes.data) {
		error(serviceRes.status >= 400 ? serviceRes.status : 500, serviceRes.error?.message ?? 'error');
	}

	return {
		service: serviceRes.data,
		products: productsRes.data ?? [],
		servers: serversRes.data ?? []
	};
};

/** POST /api/v1/admin/services/:id/<action> and normalize the result for the page. */
async function moduleAction(
	event: RequestEvent,
	action: string,
	path: string,
	body?: Record<string, unknown>
) {
	const res = await apiFetch<unknown>(event, `/api/v1/admin/services/${event.params.id}${path}`, {
		method: 'POST',
		body
	});
	if (res.error) {
		return fail(res.status >= 400 ? res.status : 500, {
			action,
			errorMessage: res.error.message
		});
	}
	return { success: true, action };
}

export const actions: Actions = {
	provision: (event) => moduleAction(event, 'provision', '/create'),

	suspend: async (event) => {
		const form = await event.request.formData();
		const reason = String(form.get('reason') ?? '').trim();
		if (!reason) {
			return fail(400, { action: 'suspend', errorKey: 'adminops.services.reasonRequired' });
		}
		return moduleAction(event, 'suspend', '/suspend', { reason });
	},

	unsuspend: (event) => moduleAction(event, 'unsuspend', '/unsuspend'),

	terminate: (event) => moduleAction(event, 'terminate', '/terminate'),

	changePackage: async (event) => {
		const form = await event.request.formData();
		const productId = Number(form.get('product_id'));
		if (!productId) {
			return fail(400, { action: 'changePackage', errorKey: 'adminops.services.productRequired' });
		}
		return moduleAction(event, 'changePackage', '/change-package', { product_id: productId });
	},

	upgrade: async (event) => {
		const form = await event.request.formData();
		const productId = Number(form.get('product_id'));
		const cycle = String(form.get('cycle') ?? '').trim();
		if (!productId) {
			return fail(400, { action: 'upgrade', errorKey: 'adminops.services.productRequired' });
		}
		if (!cycle) {
			return fail(400, { action: 'upgrade', errorKey: 'adminops.services.cycleRequired' });
		}
		const res = await apiFetch<UpgradeResult>(
			event,
			`/api/v1/admin/services/${event.params.id}/upgrade`,
			{ method: 'POST', body: { product_id: productId, cycle } }
		);
		if (res.error || !res.data) {
			return fail(res.status >= 400 ? res.status : 500, {
				action: 'upgrade',
				errorMessage: res.error?.message ?? 'Failed to upgrade service.'
			});
		}
		return { success: true, action: 'upgrade', result: res.data };
	},

	changePassword: async (event) => {
		const form = await event.request.formData();
		const password = String(form.get('password') ?? '');
		if (!password) {
			return fail(400, {
				action: 'changePassword',
				errorKey: 'adminops.services.passwordRequired'
			});
		}
		return moduleAction(event, 'changePassword', '/change-password', { password });
	},

	update: async (event) => {
		const form = await event.request.formData();
		const domain = String(form.get('domain') ?? '').trim();
		const username = String(form.get('username') ?? '').trim();
		const serverId = Number(form.get('server_id'));
		const billingCycle = String(form.get('billing_cycle') ?? '').trim();
		const recurringAmount = form.get('recurring_amount');
		const registrationDate = String(form.get('registration_date') ?? '').trim();
		const nextDueDate = String(form.get('next_due_date') ?? '').trim();
		const terminatedAt = String(form.get('terminated_at') ?? '').trim();
		const suspendReason = String(form.get('suspend_reason') ?? '');
		const notes = String(form.get('notes') ?? '');

		// Structurally-important fields: only sent when non-empty, so an
		// accidental clear never blanks them (matches next_due_date's existing
		// behavior). Free-text fields (suspend_reason, notes) are always sent,
		// including blank, so they can be deliberately cleared.
		const body: Record<string, unknown> = { suspend_reason: suspendReason, notes };
		if (domain) body.domain = domain;
		if (username) body.username = username;
		if (serverId) body.server_id = serverId;
		if (billingCycle) body.billing_cycle = billingCycle;
		if (recurringAmount !== null && String(recurringAmount).trim() !== '') {
			body.recurring_amount = Number(recurringAmount);
		}
		if (registrationDate) body.registration_date = registrationDate;
		if (nextDueDate) body.next_due_date = nextDueDate;
		if (terminatedAt) body.terminated_at = terminatedAt;

		const res = await apiFetch<unknown>(event, `/api/v1/admin/services/${event.params.id}`, {
			method: 'PATCH',
			body
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				action: 'update',
				errorMessage: res.error.message
			});
		}
		return { success: true, action: 'update' };
	}
};
