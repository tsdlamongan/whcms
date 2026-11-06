import { apiFetch, type PageMeta } from '$lib/server/api';
import { error, fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';

const TABS = [
	'summary',
	'services',
	'domains',
	'invoices',
	'tickets',
	'notes',
	'contacts'
] as const;
export type ClientTab = (typeof TABS)[number];

/** domain.Client JSON tags (+ optional joined user email). */
interface AdminClientDetail {
	id: number;
	user_id: number;
	first_name: string;
	last_name: string;
	company: string;
	address1: string;
	address2: string;
	city: string;
	state: string;
	postcode: string;
	country: string;
	phone: string;
	currency: string;
	credit_balance: number;
	status: string;
	notes_admin: string;
	email?: string;
	created_at: string;
	updated_at: string;
}

/** Account facts about the client's login user (only the ones the API actually returns). */
interface ClientUserInfo {
	email?: string;
	twofa_enabled?: boolean;
	email_verified_at?: string | null;
	last_login_at?: string | null;
}

/** clients.ClientCounts - per-client totals shown as tab badges. */
export interface ClientCounts {
	services: number;
	services_active: number;
	domains: number;
	invoices: number;
	invoices_unpaid: number;
	unpaid_total: number;
	tickets: number;
	tickets_open: number;
	transactions: number;
}

/** domain.Service JSON tags (+ optional joined product name). */
interface ServiceRow {
	id: number;
	product_id: number;
	product_name?: string;
	domain: string;
	status: string;
	billing_cycle: string;
	recurring_amount: number;
	next_due_date?: string | null;
}

/** domain.Domain JSON tags. */
interface DomainRow {
	id: number;
	name: string;
	status: string;
	expiry_date?: string | null;
	next_due_date?: string | null;
	recurring_amount: number;
	auto_renew: boolean;
}

/** domain.Invoice JSON tags. */
interface InvoiceRow {
	id: number;
	invoice_number: string;
	status: string;
	total: number;
	due_date: string;
	created_at: string;
}

/** domain.Ticket JSON tags. */
interface TicketRow {
	id: number;
	ticket_number: string;
	subject: string;
	status: string;
	priority: string;
	last_reply_at?: string | null;
	created_at: string;
}

/** domain.ClientContact JSON tags. */
interface ContactRow {
	id: number;
	client_id: number;
	first_name: string;
	last_name: string;
	email: string;
	phone: string;
}

/** billing.GeneratedInvoiceSummary JSON tags. */
interface GeneratedInvoiceSummary {
	type: 'service' | 'domain';
	id: number;
	invoice_id: number;
	invoice_number: string;
}

/** billing.SkippedItem JSON tags. */
interface SkippedItem {
	type: string;
	id: number;
	reason: string;
}

/** billing.GenerateSelectedInvoicesResult JSON tags. */
export interface GenerateSelectedInvoicesResult {
	created: GeneratedInvoiceSummary[];
	skipped: SkippedItem[];
}

/** Parses a JSON-encoded array of positive IDs from a hidden form field. */
function parseIdList(raw: FormDataEntryValue | null): number[] {
	if (typeof raw !== 'string' || !raw) return [];
	try {
		const parsed = JSON.parse(raw);
		if (!Array.isArray(parsed)) return [];
		return parsed.filter((n): n is number => Number.isInteger(n) && n > 0);
	} catch {
		return [];
	}
}

function parseId(raw: string): number {
	const id = Number(raw);
	if (!Number.isInteger(id) || id <= 0) error(404, 'client not found');
	return id;
}

/** Map backend VALIDATION error details ([{field, message}]) to a per-field error map. */
function fieldErrors(details: unknown[]): Record<string, string> {
	const out: Record<string, string> = {};
	for (const d of details) {
		if (d && typeof d === 'object') {
			const rec = d as Record<string, unknown>;
			const field = typeof rec.field === 'string' ? rec.field : undefined;
			const message =
				typeof rec.message === 'string'
					? rec.message
					: typeof rec.error === 'string'
						? rec.error
						: undefined;
			if (field) out[field] = message ?? 'invalid';
		}
	}
	return out;
}

export const load: PageServerLoad = async (event) => {
	const id = parseId(event.params.id);
	const q = event.url.searchParams;
	const rawTab = q.get('tab') ?? 'summary';
	const tab: ClientTab = (TABS as readonly string[]).includes(rawTab)
		? (rawTab as ClientTab)
		: 'summary';
	const listPage = Math.max(1, Number(q.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(q.get('per_page')) || 10));

	// Detail aggregate may come flat or nested under {client} - normalize both.
	// The tab list query depends only on URL params, so it runs concurrently
	// with the detail fetch (its result is only used when the client exists).
	const detailPromise = apiFetch<unknown>(event, `/api/v1/admin/clients/${id}`);
	const listQuery = { client_id: id, page: listPage, per_page: perPage };
	const tabPromise =
		tab === 'services'
			? apiFetch<ServiceRow[]>(event, '/api/v1/admin/services', { query: listQuery })
			: tab === 'domains'
				? apiFetch<DomainRow[]>(event, '/api/v1/admin/domains', { query: listQuery })
				: tab === 'invoices'
					? apiFetch<InvoiceRow[]>(event, '/api/v1/admin/invoices', { query: listQuery })
					: tab === 'tickets'
						? apiFetch<TicketRow[]>(event, '/api/v1/admin/tickets', { query: listQuery })
						: tab === 'contacts'
							? apiFetch<ContactRow[]>(event, `/api/v1/admin/clients/${id}/contacts`)
							: null;
	const detailRes = await detailPromise;
	if (detailRes.status === 404) error(404, 'client not found');

	let client: AdminClientDetail | null = null;
	let userInfo: ClientUserInfo | null = null;
	let counts: ClientCounts | null = null;
	let email = '';
	if (detailRes.data && typeof detailRes.data === 'object') {
		const raw = detailRes.data as Record<string, unknown>;
		client = (raw.client && typeof raw.client === 'object' ? raw.client : raw) as AdminClientDetail;
		if (raw.user && typeof raw.user === 'object') {
			userInfo = raw.user as ClientUserInfo;
		}
		// GET /admin/clients/:id returns the login facts at the top level
		// (adminops/clients.ClientDetail), not nested under `user`.
		if ('last_login_at' in raw) {
			userInfo = {
				...userInfo,
				last_login_at: typeof raw.last_login_at === 'string' ? raw.last_login_at : null
			};
		}
		if (raw.counts && typeof raw.counts === 'object') {
			counts = raw.counts as ClientCounts;
		}
		email =
			(typeof raw.email === 'string' ? raw.email : undefined) ??
			userInfo?.email ??
			client.email ??
			'';
	}

	let services: ServiceRow[] | null = null;
	let domains: DomainRow[] | null = null;
	let invoices: InvoiceRow[] | null = null;
	let tickets: TicketRow[] | null = null;
	let contacts: ContactRow[] | null = null;
	let tabMeta: PageMeta | null = null;
	let tabError: string | null = null;

	if (client && tabPromise) {
		const r = await tabPromise;
		tabMeta = r.meta;
		tabError = r.error ? r.error.message : null;
		if (tab === 'services') services = (r.data as ServiceRow[]) ?? [];
		else if (tab === 'domains') domains = (r.data as DomainRow[]) ?? [];
		else if (tab === 'invoices') invoices = (r.data as InvoiceRow[]) ?? [];
		else if (tab === 'tickets') tickets = (r.data as TicketRow[]) ?? [];
		else if (tab === 'contacts') contacts = (r.data as ContactRow[]) ?? [];
	}

	return {
		clientId: id,
		client,
		email,
		userInfo,
		counts,
		loadError: detailRes.error ? detailRes.error.message : null,
		tab,
		listPage,
		perPage,
		services,
		domains,
		invoices,
		tickets,
		contacts,
		tabMeta,
		tabError
	};
};

export const actions: Actions = {
	profile: async (event) => {
		const id = parseId(event.params.id);
		const form = await event.request.formData();
		const body = {
			first_name: String(form.get('first_name') ?? '').trim(),
			last_name: String(form.get('last_name') ?? '').trim(),
			company: String(form.get('company') ?? '').trim(),
			address1: String(form.get('address1') ?? '').trim(),
			address2: String(form.get('address2') ?? '').trim(),
			city: String(form.get('city') ?? '').trim(),
			state: String(form.get('state') ?? '').trim(),
			postcode: String(form.get('postcode') ?? '').trim(),
			country: String(form.get('country') ?? '').trim() || 'ID',
			phone: String(form.get('phone') ?? '').trim(),
			status: String(form.get('status') ?? '').trim()
		};
		if (!body.first_name || !body.last_name) {
			return fail(400, {
				action: 'profile',
				errorKey: 'admincore.clients.fillRequired',
				fields: {}
			});
		}
		const res = await apiFetch(event, `/api/v1/admin/clients/${id}`, {
			method: 'PATCH',
			body
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, {
				action: 'profile',
				errorMessage: res.error.message,
				fields: fieldErrors(res.error.details)
			});
		}
		return { action: 'profile', ok: true };
	},

	notes: async (event) => {
		const id = parseId(event.params.id);
		const form = await event.request.formData();
		const res = await apiFetch(event, `/api/v1/admin/clients/${id}`, {
			method: 'PATCH',
			body: { notes_admin: String(form.get('notes_admin') ?? '') }
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, {
				action: 'notes',
				errorMessage: res.error.message
			});
		}
		return { action: 'notes', ok: true };
	},

	credit: async (event) => {
		const id = parseId(event.params.id);
		const form = await event.request.formData();
		const amount = Math.trunc(Number(form.get('amount')));
		const mode = String(form.get('mode') ?? 'add');
		const reason = String(form.get('reason') ?? '').trim();
		if (!Number.isFinite(amount) || amount <= 0) {
			return fail(400, { action: 'credit', errorKey: 'admincore.clients.invalidAmount' });
		}
		if (!reason) {
			return fail(400, { action: 'credit', errorKey: 'admincore.clients.reasonRequired' });
		}
		const delta = mode === 'deduct' ? -amount : amount;
		const res = await apiFetch(event, `/api/v1/admin/clients/${id}/credit`, {
			method: 'POST',
			body: { delta, reason }
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, {
				action: 'credit',
				errorMessage: res.error.message
			});
		}
		return { action: 'credit', ok: true };
	},

	contactCreate: async (event) => {
		const id = parseId(event.params.id);
		const form = await event.request.formData();
		const body = {
			first_name: String(form.get('first_name') ?? '').trim(),
			last_name: String(form.get('last_name') ?? '').trim(),
			email: String(form.get('email') ?? '').trim(),
			phone: String(form.get('phone') ?? '').trim()
		};
		if (!body.first_name || !body.email) {
			return fail(400, { action: 'contact', errorKey: 'admincore.clients.fillRequired' });
		}
		const res = await apiFetch(event, `/api/v1/admin/clients/${id}/contacts`, {
			method: 'POST',
			body
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, {
				action: 'contact',
				errorMessage: res.error.message
			});
		}
		return { action: 'contact', ok: true };
	},

	contactUpdate: async (event) => {
		const id = parseId(event.params.id);
		const form = await event.request.formData();
		const contactId = Number(form.get('contact_id'));
		if (!Number.isInteger(contactId) || contactId <= 0) {
			return fail(400, { action: 'contact', errorKey: 'admincore.clients.saveFailed' });
		}
		const body = {
			first_name: String(form.get('first_name') ?? '').trim(),
			last_name: String(form.get('last_name') ?? '').trim(),
			email: String(form.get('email') ?? '').trim(),
			phone: String(form.get('phone') ?? '').trim()
		};
		if (!body.first_name || !body.email) {
			return fail(400, { action: 'contact', errorKey: 'admincore.clients.fillRequired' });
		}
		const res = await apiFetch(event, `/api/v1/admin/clients/${id}/contacts/${contactId}`, {
			method: 'PATCH',
			body
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, {
				action: 'contact',
				errorMessage: res.error.message
			});
		}
		return { action: 'contact', ok: true };
	},

	invoiceSelected: async (event) => {
		const form = await event.request.formData();
		const serviceIds = parseIdList(form.get('service_ids'));
		const domainIds = parseIdList(form.get('domain_ids'));
		if (serviceIds.length === 0 && domainIds.length === 0) {
			return fail(400, {
				action: 'invoiceSelected',
				errorMessage: 'Select at least one item first.'
			});
		}
		const res = await apiFetch<GenerateSelectedInvoicesResult>(
			event,
			'/api/v1/admin/invoices/generate-selected',
			{
				method: 'POST',
				body: { service_ids: serviceIds, domain_ids: domainIds }
			}
		);
		if (res.error || !res.data) {
			return fail(res.status >= 400 ? res.status : 400, {
				action: 'invoiceSelected',
				errorMessage: res.error?.message ?? 'Failed to generate invoices.'
			});
		}
		return { action: 'invoiceSelected', ok: true, result: res.data };
	},

	contactDelete: async (event) => {
		const id = parseId(event.params.id);
		const form = await event.request.formData();
		const contactId = Number(form.get('contact_id'));
		if (!Number.isInteger(contactId) || contactId <= 0) {
			return fail(400, { action: 'contactDelete', errorKey: 'admincore.clients.saveFailed' });
		}
		const res = await apiFetch(event, `/api/v1/admin/clients/${id}/contacts/${contactId}`, {
			method: 'DELETE'
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, {
				action: 'contactDelete',
				errorMessage: res.error.message
			});
		}
		return { action: 'contactDelete', ok: true };
	}
};
