import { apiFetch } from '$lib/server/api';
import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import {
	CONTACT_FIELDS,
	DNS_TYPES,
	DOMAIN_TABS,
	type DnsRecord,
	type DomainAddonOption,
	type DomainRow,
	type DomainTab,
	type RegistrantContact
} from '../shared';

/** Hostname validation for nameservers (ns1.example.com). */
const HOST_RE =
	/^(?=.{4,253}$)(([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])\.)+[a-zA-Z]{2,63}$/;
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

function parseNameservers(raw: unknown): string[] {
	if (!Array.isArray(raw)) return [];
	return raw.filter((v): v is string => typeof v === 'string');
}

export const load: PageServerLoad = async (event) => {
	const { id } = event.params;
	const tabParam = event.url.searchParams.get('tab') ?? 'overview';
	const tab: DomainTab = (DOMAIN_TABS as readonly string[]).includes(tabParam)
		? (tabParam as DomainTab)
		: 'overview';

	// The tab fetches depend only on the route/tab params - start them all
	// concurrently with the domain fetch and only use the results when the
	// domain actually loaded.
	const domainPromise = apiFetch<DomainRow>(event, `/api/v1/domains/${id}`);
	const dnsPromise =
		tab === 'dns'
			? apiFetch<DnsRecord[] | { records: DnsRecord[] }>(event, `/api/v1/domains/${id}/dns`)
			: null;
	const contactPromise =
		// Not in CONTRACTS §9 - RESTful guess; the form still renders when it 404s.
		tab === 'contact' ? apiFetch<RegistrantContact>(event, `/api/v1/domains/${id}/contact`) : null;
	const addonsPromise =
		tab === 'addons' || tab === 'dns'
			? apiFetch<DomainAddonOption[]>(event, '/api/v1/domains/addons')
			: null;
	const res = await domainPromise;

	let dns: DnsRecord[] | null = null;
	let dnsError: string | null = null;
	let dnsAddonRequired = false;
	let contact: RegistrantContact | null = null;
	let contactError: string | null = null;
	let addons: DomainAddonOption[] = [];

	if (res.data && dnsPromise) {
		const dnsRes = await dnsPromise;
		if (dnsRes.error) {
			if (dnsRes.error.code === 'PAYMENT_REQUIRED') {
				dnsAddonRequired = true;
			} else {
				dnsError = dnsRes.error.message;
			}
		} else {
			const d = dnsRes.data;
			dns = Array.isArray(d) ? d : (d?.records ?? []);
		}
	}

	if (res.data && contactPromise) {
		const contactRes = await contactPromise;
		if (contactRes.error) contactError = contactRes.error.message;
		else contact = contactRes.data;
	}

	if (res.data && addonsPromise) {
		const addonsRes = await addonsPromise;
		addons = addonsRes.data ?? [];
	}

	return {
		domain: res.data,
		domainError: res.error ? { message: res.error.message, status: res.status } : null,
		nameservers: parseNameservers(res.data?.nameservers),
		tab,
		dns,
		dnsError,
		dnsAddonRequired,
		contact,
		contactError,
		addons
	};
};

export const actions: Actions = {
	/** Toggle auto-renew from the overview tab - PATCH /domains/:id {auto_renew}. */
	autorenew: async (event) => {
		const form = await event.request.formData();
		const value = String(form.get('value') ?? '') === 'true';

		const res = await apiFetch<unknown>(event, `/api/v1/domains/${event.params.id}`, {
			method: 'PATCH',
			body: { auto_renew: value }
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				section: 'overview' as const,
				errorMessage: res.error.message
			});
		}
		return { section: 'overview' as const, success: true, autoRenew: value };
	},

	/** Renew now - POST /domains/:id/renew, then redirect to the created invoice. */
	renew: async (event) => {
		const form = await event.request.formData();
		const years = Math.min(10, Math.max(1, Number(form.get('years')) || 1));

		const res = await apiFetch<Record<string, unknown>>(
			event,
			`/api/v1/domains/${event.params.id}/renew`,
			{ method: 'POST', body: { years } }
		);

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				section: 'renew' as const,
				errorMessage: res.error.message
			});
		}

		const data = res.data ?? {};
		const invoice = data.invoice as Record<string, unknown> | undefined;
		const invoiceId = data.invoice_id ?? invoice?.id ?? data.id;
		if (typeof invoiceId === 'number' || typeof invoiceId === 'string') {
			redirect(303, `/billing/invoices/${invoiceId}`);
		}
		return { section: 'renew' as const, success: true };
	},

	/** Save nameservers - PATCH /domains/:id/nameservers {nameservers: []}. */
	nameservers: async (event) => {
		const form = await event.request.formData();
		const values = {
			ns1: String(form.get('ns1') ?? '').trim(),
			ns2: String(form.get('ns2') ?? '').trim(),
			ns3: String(form.get('ns3') ?? '').trim(),
			ns4: String(form.get('ns4') ?? '').trim()
		};
		const list = [values.ns1, values.ns2, values.ns3, values.ns4].filter(Boolean);

		if (list.length < 2) {
			return fail(400, {
				section: 'nameservers' as const,
				errorKey: 'clientDomains.ns.needTwo',
				values
			});
		}
		if (list.some((host) => !HOST_RE.test(host))) {
			return fail(400, {
				section: 'nameservers' as const,
				errorKey: 'clientDomains.ns.invalidHost',
				values
			});
		}

		const res = await apiFetch<unknown>(event, `/api/v1/domains/${event.params.id}/nameservers`, {
			method: 'PATCH',
			body: { nameservers: list }
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				section: 'nameservers' as const,
				errorMessage: res.error.message,
				values
			});
		}
		return { section: 'nameservers' as const, success: true };
	},

	/** Replace the full DNS record set - PUT /domains/:id/dns {records: []}. */
	dns: async (event) => {
		const form = await event.request.formData();

		let parsed: unknown;
		try {
			parsed = JSON.parse(String(form.get('records') ?? '[]'));
		} catch {
			return fail(400, { section: 'dns' as const, errorKey: 'clientDomains.dns.invalid' });
		}
		if (!Array.isArray(parsed)) {
			return fail(400, { section: 'dns' as const, errorKey: 'clientDomains.dns.invalid' });
		}

		const records: DnsRecord[] = [];
		for (const [i, raw] of parsed.entries()) {
			const rowFail = () =>
				fail(400, {
					section: 'dns' as const,
					errorKey: 'clientDomains.dns.invalidRow',
					errorParams: { row: i + 1 }
				});

			if (typeof raw !== 'object' || raw === null) return rowFail();
			const rec = raw as Record<string, unknown>;
			const type = rec.type;
			const host = rec.host;
			const value = rec.value;
			const ttl = Number(rec.ttl);
			const prio = Number(rec.prio ?? 0);

			if (typeof type !== 'string' || !(DNS_TYPES as readonly string[]).includes(type)) {
				return rowFail();
			}
			if (typeof host !== 'string' || !host.trim()) return rowFail();
			if (typeof value !== 'string' || !value.trim()) return rowFail();
			if (!Number.isInteger(ttl) || ttl < 0 || ttl > 604800) return rowFail();
			if (!Number.isInteger(prio) || prio < 0 || prio > 65535) return rowFail();

			records.push({ type, host: host.trim(), value: value.trim(), ttl, prio });
		}

		const res = await apiFetch<unknown>(event, `/api/v1/domains/${event.params.id}/dns`, {
			method: 'PUT',
			body: { records }
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				section: 'dns' as const,
				errorMessage: res.error.message
			});
		}
		return { section: 'dns' as const, success: true };
	},

	/** Reveal the EPP code - GET /domains/:id/epp (kept server-side; returned as form result). */
	epp: async (event) => {
		const res = await apiFetch<{ epp_code?: string; code?: string } | string>(
			event,
			`/api/v1/domains/${event.params.id}/epp`
		);

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				section: 'epp' as const,
				errorMessage: res.error.message
			});
		}

		const data = res.data;
		const eppCode = typeof data === 'string' ? data : (data?.epp_code ?? data?.code ?? '');
		if (!eppCode) {
			return fail(502, { section: 'epp' as const, errorKey: 'clientDomains.epp.unavailable' });
		}
		return { section: 'epp' as const, eppCode };
	},

	/** Update registrant contact - PATCH /domains/:id {contact: {...}}. */
	contact: async (event) => {
		const form = await event.request.formData();

		const values = {} as RegistrantContact;
		for (const field of CONTACT_FIELDS) {
			values[field] = String(form.get(field) ?? '').trim();
		}
		values.country = values.country.toUpperCase();

		const fieldErrors: Record<string, string> = {};
		const required: (keyof RegistrantContact)[] = [
			'first_name',
			'last_name',
			'email',
			'phone',
			'address1',
			'city',
			'postcode',
			'country'
		];
		for (const field of required) {
			if (!values[field]) fieldErrors[field] = 'clientDomains.contact.required';
		}
		if (values.email && !EMAIL_RE.test(values.email)) {
			fieldErrors.email = 'clientDomains.contact.invalidEmail';
		}
		if (values.country && !/^[A-Z]{2}$/.test(values.country)) {
			fieldErrors.country = 'clientDomains.contact.invalidCountry';
		}

		if (Object.keys(fieldErrors).length > 0) {
			return fail(400, { section: 'contact' as const, fieldErrors, values });
		}

		const res = await apiFetch<unknown>(event, `/api/v1/domains/${event.params.id}`, {
			method: 'PATCH',
			body: { contact: values }
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				section: 'contact' as const,
				errorMessage: res.error.message,
				values
			});
		}
		return { section: 'contact' as const, success: true };
	},

	/** Replace the domain's active addons - POST /domains/:id/addons {addons: []}. */
	addons: async (event) => {
		const form = await event.request.formData();
		const addons = form.getAll('addons').map((v) => String(v));

		const res = await apiFetch<DomainRow>(event, `/api/v1/domains/${event.params.id}/addons`, {
			method: 'POST',
			body: { addons }
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				section: 'addons' as const,
				errorMessage: res.error.message
			});
		}
		return { section: 'addons' as const, success: true };
	},

	/**
	 * Ask the registrar for the hosted page where this domain's documents are
	 * uploaded - POST /domains/:id/documents/link. The registrar has no upload
	 * API, so the link IS the upload step. It is a bearer secret: it only ever
	 * lives in this action's result (never stored), and only a plain http(s)
	 * URL is handed to the browser.
	 */
	documentLink: async (event) => {
		const res = await apiFetch<{ url?: string }>(
			event,
			`/api/v1/domains/${event.params.id}/documents/link`,
			{ method: 'POST' }
		);
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				section: 'documents' as const,
				errorMessage: res.error.message
			});
		}
		let uploadUrl = '';
		try {
			const u = new URL(res.data?.url ?? '');
			if (u.protocol === 'https:' || u.protocol === 'http:') uploadUrl = u.toString();
		} catch {
			/* fall through to the failure below */
		}
		if (!uploadUrl) {
			return fail(502, {
				section: 'documents' as const,
				errorKey: 'clientDomains.docs.errors.noLink'
			});
		}
		return { section: 'documents' as const, success: true, uploadUrl };
	},

	/** Re-sync with the registrar right now - POST /domains/:id/refresh. */
	refresh: async (event) => {
		const res = await apiFetch<DomainRow>(event, `/api/v1/domains/${event.params.id}/refresh`, {
			method: 'POST'
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				section: 'documents' as const,
				errorMessage: res.error.message
			});
		}
		return { section: 'documents' as const, success: true, refreshed: true as const };
	},

	/** Re-send the registrant's verification email - POST /domains/:id/verification/resend. */
	resendVerification: async (event) => {
		const res = await apiFetch<unknown>(
			event,
			`/api/v1/domains/${event.params.id}/verification/resend`,
			{ method: 'POST' }
		);
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				section: 'documents' as const,
				errorMessage: res.error.message
			});
		}
		return { section: 'documents' as const, success: true, resent: true as const };
	}
};
