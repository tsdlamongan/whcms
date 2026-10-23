import { apiFetch } from '$lib/server/api';
import type { PageServerLoad } from './$types';

/** domain.EmailTemplate JSON tags (list may omit bodies). */
export interface EmailTemplateRow {
	id: number;
	key: string;
	locale: string;
	subject: string;
	body_html?: string;
	body_text?: string;
	created_at: string;
	updated_at: string;
}

export const load: PageServerLoad = async (event) => {
	const q = event.url.searchParams;
	const page = Math.max(1, Number(q.get('page')) || 1);
	// No pager in the UI: this page renders every template key grouped by
	// category (docs/DESIGN.md §8.5), so the fetch must cover the whole fixed set.
	const perPage = Math.min(1000, Math.max(1, Number(q.get('per_page')) || 1000));
	const search = (q.get('search') ?? '').trim();

	// The API returns every template (a small fixed set) and has no search
	// parameter, so the key/subject filter is applied here.
	const res = await apiFetch<EmailTemplateRow[]>(event, '/api/v1/admin/email-templates', {
		query: { page, per_page: perPage }
	});
	const needle = search.toLowerCase();
	const templates = (res.data ?? []).filter(
		(t) =>
			!needle || t.key.toLowerCase().includes(needle) || t.subject.toLowerCase().includes(needle)
	);

	return {
		templates,
		meta: res.meta,
		listError: res.error?.message ?? null,
		page,
		perPage,
		search
	};
};
