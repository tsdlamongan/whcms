import { apiFetch } from '$lib/server/api';
import type { PageServerLoad } from './$types';
import type { ClientSnipe } from './shared';

export const load: PageServerLoad = async (event) => {
	const sp = event.url.searchParams;
	const page = Math.max(1, Number(sp.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(sp.get('per_page')) || 10));
	const search = (sp.get('search') ?? '').trim();
	const status = sp.get('status') ?? '';

	const res = await apiFetch<ClientSnipe[]>(event, '/api/v1/snipes', {
		query: { page, per_page: perPage, search: search || undefined, status: status || undefined }
	});

	return {
		snipes: res.data ?? [],
		meta: res.meta ?? { page, per_page: perPage, total: res.data?.length ?? 0 },
		loadError: res.error?.message ?? null,
		search,
		status
	};
};
