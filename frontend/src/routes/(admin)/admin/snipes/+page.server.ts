import { withClientNames } from '$lib/server/adminNames';
import { apiFetch } from '$lib/server/api';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import { SNIPE_SETTINGS, type AdminSnipe, type SnipeOverview } from './types';

/** GET /admin/settings is grouped by key prefix; only the snipe group matters here. */
interface SettingsGroups {
	snipe?: Record<string, unknown>;
}

export const load: PageServerLoad = async (event) => {
	const q = event.url.searchParams;
	const page = Math.max(1, Number(q.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(q.get('per_page')) || 10));
	const search = (q.get('search') ?? '').trim();
	const status = q.get('status') ?? '';

	const [list, overview, settings] = await Promise.all([
		apiFetch<AdminSnipe[]>(event, '/api/v1/admin/snipes', {
			query: { page, per_page: perPage, search: search || undefined, status: status || undefined }
		}),
		apiFetch<SnipeOverview>(event, '/api/v1/admin/snipes/status'),
		apiFetch<SettingsGroups>(event, '/api/v1/admin/settings')
	]);

	// Staff without the "settings" permission get a 403: hide the panel, don't error.
	const group = settings.data?.snipe ?? {};
	const values: Record<string, boolean | number> = {};
	for (const def of SNIPE_SETTINGS) {
		const raw = group[def.key.slice('snipe.'.length)];
		const wellTyped = def.kind === 'bool' ? typeof raw === 'boolean' : typeof raw === 'number';
		values[def.key] = wellTyped ? (raw as boolean | number) : def.def;
	}

	return {
		snipes: await withClientNames(event, list.data ?? []),
		meta: list.meta,
		listError: list.error?.message ?? null,
		overview: overview.data,
		overviewError: overview.error?.message ?? null,
		canEditSettings: settings.status !== 403 && !settings.error,
		settingValues: values,
		page,
		perPage,
		search,
		status
	};
};

export const actions: Actions = {
	saveSettings: async (event) => {
		const form = await event.request.formData();
		const updates: Record<string, boolean | number> = {};
		for (const def of SNIPE_SETTINGS) {
			if (def.kind === 'bool') {
				updates[def.key] = form.get(def.key) === 'on';
			} else {
				// Number('') and Number(null) are 0: a blank box must not silently save 0.
				const raw = String(form.get(def.key) ?? '').trim();
				const n = Math.round(Number(raw));
				if (raw === '' || !Number.isFinite(n)) {
					return fail(400, {
						action: 'saveSettings',
						errorMessage: `${def.label} must be a number.`
					});
				}
				updates[def.key] = n;
			}
		}
		const res = await apiFetch<unknown>(event, '/api/v1/admin/settings', {
			method: 'PUT',
			body: updates
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				action: 'saveSettings',
				errorMessage: res.error.message
			});
		}
		return { success: true, action: 'saveSettings' };
	}
};
