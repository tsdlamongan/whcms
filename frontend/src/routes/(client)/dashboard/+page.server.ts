import { apiFetch } from '$lib/server/api';
import { loadRequireEmailVerification } from '$lib/server/captcha';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';

/** Invoice list row - field names per backend/internal/domain/entities.go JSON tags. */
export interface InvoiceRow {
	id: number;
	invoice_number: string;
	client_id: number;
	status: string;
	subtotal: number;
	discount: number;
	tax_total: number;
	credit_applied: number;
	total: number;
	currency: string;
	due_date: string;
	paid_at: string | null;
	created_at: string;
}

export const load: PageServerLoad = async (event) => {
	// KPI counts come from list endpoints' meta.total (per_page=1); unpaid invoices are
	// fetched with a larger page so the outstanding amount can be summed client-side.
	const [services, domains, unpaid, tickets, recent, requireVerify] = await Promise.all([
		apiFetch<unknown[]>(event, '/api/v1/services', {
			query: { status: 'active', page: 1, per_page: 1 }
		}),
		apiFetch<unknown[]>(event, '/api/v1/domains', { query: { page: 1, per_page: 1 } }),
		apiFetch<InvoiceRow[]>(event, '/api/v1/invoices', {
			query: { status: 'unpaid', page: 1, per_page: 100 }
		}),
		apiFetch<unknown[]>(event, '/api/v1/tickets', {
			query: { status: 'open', page: 1, per_page: 1 }
		}),
		apiFetch<InvoiceRow[]>(event, '/api/v1/invoices', { query: { page: 1, per_page: 5 } }),
		// The unverified-email banner mirrors the checkout gate - irrelevant
		// (and hidden) when the admin has disabled the requirement.
		loadRequireEmailVerification(event)
	]);

	const unpaidRows = unpaid.data ?? [];
	const firstError = [services, domains, unpaid, tickets, recent].find((r) => r.error)?.error;

	return {
		stats: {
			activeServices: services.meta?.total ?? null,
			domains: domains.meta?.total ?? null,
			unpaidCount: unpaid.meta?.total ?? (unpaid.data ? unpaidRows.length : null),
			unpaidTotal: unpaid.data ? unpaidRows.reduce((sum, row) => sum + (row.total ?? 0), 0) : null,
			openTickets: tickets.meta?.total ?? null
		},
		recentInvoices: recent.data ?? [],
		loadError: firstError ? firstError.message : null,
		showVerifyBanner: requireVerify && !(event.locals.user?.email_verified ?? true)
	};
};

export const actions: Actions = {
	/** Re-send the verification email for the logged-in, still-unverified user
	 *  (checkout is gated on verification; the dashboard banner offers this). */
	resendVerification: async (event) => {
		if (!event.locals.user) {
			return fail(401, { resendError: true, resendSuccess: false });
		}
		const res = await apiFetch(event, '/api/v1/auth/resend-verification', {
			method: 'POST',
			body: { email: event.locals.user.email }
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, { resendError: true, resendSuccess: false });
		}
		return { resendSuccess: true, resendError: false };
	}
};
