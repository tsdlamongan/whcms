import { apiFetch } from '$lib/server/api';
import { jakartaToISO } from '$lib/snipe';
import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import type { ClientSnipe, SnipePlanView } from '../shared';

export const load: PageServerLoad = async (event) => {
	const res = await apiFetch<SnipePlanView[]>(event, '/api/v1/snipe/plans');
	return { plans: res.data ?? [], plansError: res.error?.message ?? null };
};

export const actions: Actions = {
	default: async (event) => {
		const form = await event.request.formData();
		const domain = String(form.get('domain') ?? '').trim();
		const planId = Number(form.get('plan_id') ?? 0);
		const years = Math.trunc(Number(form.get('years') ?? 1)) || 1;
		const useWindow = form.get('use_window') === 'on';
		const huntFrom = useWindow ? String(form.get('hunt_from') ?? '').trim() : '';
		const huntUntil = useWindow ? String(form.get('hunt_until') ?? '').trim() : '';
		const nameservers: string[] = [];
		for (const key of ['ns1', 'ns2', 'ns3', 'ns4']) {
			const v = String(form.get(key) ?? '').trim();
			if (v) nameservers.push(v);
		}
		const values = { domain, planId, years, useWindow, huntFrom, huntUntil, nameservers };

		if (!domain) return fail(400, { errorMessage: 'domain', serverError: false, ...values });
		if (!planId) return fail(400, { errorMessage: 'plan', serverError: false, ...values });
		// A drop window needs both ends (an open-ended one would fire registration
		// attempts for the whole plan); the API also caps its length.
		if (useWindow && (!huntFrom || !huntUntil))
			return fail(400, { errorMessage: 'window', serverError: false, ...values });
		for (const raw of [huntFrom, huntUntil]) {
			if (raw && !jakartaToISO(raw))
				return fail(400, { errorMessage: 'window', serverError: false, ...values });
		}

		const res = await apiFetch<ClientSnipe>(event, '/api/v1/snipes', {
			method: 'POST',
			body: {
				domain,
				plan_id: planId,
				years,
				hunt_from: jakartaToISO(huntFrom),
				hunt_until: jakartaToISO(huntUntil),
				nameservers: nameservers.length > 0 ? nameservers : undefined
			}
		});
		if (res.error || !res.data) {
			return fail(res.status >= 400 ? res.status : 500, {
				errorMessage: res.error?.message ?? 'failed',
				serverError: true,
				...values
			});
		}

		// Pay the up-front invoice; the hunt starts by itself once it is paid.
		redirect(
			303,
			res.data.invoice_id ? `/billing/invoices/${res.data.invoice_id}` : `/snipes/${res.data.id}`
		);
	}
};
