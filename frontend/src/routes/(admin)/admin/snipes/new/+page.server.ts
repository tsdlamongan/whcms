import { apiFetch } from '$lib/server/api';
import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import type { AdminClientLite } from '../../invoices/types';
import { jakartaToISO } from '../types';

interface CreatedSnipe {
	id: number;
}

export const load: PageServerLoad = async (event) => {
	const search = (event.url.searchParams.get('client_search') ?? '').trim();
	const res = await apiFetch<AdminClientLite[]>(event, '/api/v1/admin/clients', {
		query: { search: search || undefined, per_page: 20, page: 1 }
	});
	return {
		clientSearch: search,
		clients: res.data ?? [],
		clientsError: res.error?.message ?? null
	};
};

export const actions: Actions = {
	default: async (event) => {
		const form = await event.request.formData();
		const clientId = Number(form.get('client_id') ?? 0);
		const domain = String(form.get('domain') ?? '').trim();
		const years = Math.trunc(Number(form.get('years') ?? 1)) || 1;
		const huntFrom = String(form.get('hunt_from') ?? '').trim();
		const huntUntil = String(form.get('hunt_until') ?? '').trim();
		const deadline = String(form.get('deadline_at') ?? '').trim();
		const windowDays = Math.trunc(Number(form.get('window_days') ?? 0)) || 0;
		const nameservers: string[] = [];
		for (const key of ['ns1', 'ns2', 'ns3', 'ns4']) {
			const v = String(form.get(key) ?? '').trim();
			if (v) nameservers.push(v);
		}

		const values = {
			clientId,
			domain,
			years,
			huntFrom,
			huntUntil,
			deadline,
			windowDays,
			ns1: nameservers[0] ?? '',
			ns2: nameservers[1] ?? '',
			ns3: nameservers[2] ?? '',
			ns4: nameservers[3] ?? ''
		};

		if (!clientId || clientId < 1) {
			return fail(400, { errorMessage: 'Please select a client first.', ...values });
		}
		if (!domain) {
			return fail(400, { errorMessage: 'The domain name is required.', ...values });
		}
		if (nameservers.length === 1) {
			return fail(400, {
				errorMessage: 'Provide at least 2 nameservers (or leave all empty).',
				...values
			});
		}
		for (const [label, raw] of [
			['Window start', huntFrom],
			['Window end', huntUntil],
			['Deadline', deadline]
		] as const) {
			if (raw && !jakartaToISO(raw)) {
				return fail(400, { errorMessage: `${label} is not a valid date and time.`, ...values });
			}
		}

		const res = await apiFetch<CreatedSnipe>(event, '/api/v1/admin/snipes', {
			method: 'POST',
			body: {
				client_id: clientId,
				domain,
				years,
				hunt_from: jakartaToISO(huntFrom),
				hunt_until: jakartaToISO(huntUntil),
				deadline_at: jakartaToISO(deadline),
				window_days: windowDays > 0 ? windowDays : undefined,
				nameservers: nameservers.length > 0 ? nameservers : undefined
			}
		});

		if (res.error || !res.data) {
			return fail(res.status >= 400 ? res.status : 500, {
				errorMessage: res.error?.message ?? 'Failed to start the hunt',
				...values
			});
		}

		redirect(303, `/admin/snipes/${res.data.id}`);
	}
};
