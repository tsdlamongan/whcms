import { apiFetch } from '$lib/server/api';
import type { PageServerLoad } from './$types';
import { EMAIL_LOG_STATUSES, type EmailLogEntry } from './types';

export const load: PageServerLoad = async (event) => {
	const params = event.url.searchParams;
	const page = Math.max(1, Number(params.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(params.get('per_page')) || 10));
	const statusParam = params.get('status') ?? '';
	const status = (EMAIL_LOG_STATUSES as readonly string[]).includes(statusParam) ? statusParam : '';

	const res = await apiFetch<EmailLogEntry[]>(event, '/api/v1/account/email-log', {
		query: { page, per_page: perPage, status: status || undefined }
	});

	return {
		emails: res.data ?? [],
		meta: res.meta ?? { page, per_page: perPage, total: res.data?.length ?? 0 },
		loadError: res.error?.message ?? null,
		status
	};
};
