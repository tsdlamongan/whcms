import { apiFetch } from '$lib/server/api';
import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import type { ProductGroupRow, ProductRow } from './catalog';

export const load: PageServerLoad = async (event) => {
	const q = event.url.searchParams;
	const page = Math.max(1, Number(q.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(q.get('per_page')) || 10));
	const search = (q.get('search') ?? '').trim();
	const groupId = Number(q.get('group_id')) || 0;
	const type = q.get('type') ?? '';

	const [productsRes, groupsRes] = await Promise.all([
		apiFetch<ProductRow[]>(event, '/api/v1/admin/products', {
			query: {
				page,
				per_page: perPage,
				search: search || undefined,
				group_id: groupId > 0 ? groupId : undefined,
				type: type || undefined
			}
		}),
		apiFetch<ProductGroupRow[]>(event, '/api/v1/admin/product-groups')
	]);

	return {
		products: productsRes.data ?? [],
		meta: productsRes.meta,
		listError: productsRes.error ? productsRes.error.message : null,
		groups: groupsRes.data ?? [],
		page,
		perPage,
		search,
		groupId,
		type
	};
};

/** One group's new position, sent by the drag-and-drop group reorder UI. */
interface GroupSortChange {
	id: number;
	sort: number;
}

/**
 * Persists a product-group drag-and-drop reorder. The client reassigns
 * sequential sort values (0..N-1) to the groups in their new visual order and
 * posts only the ones whose sort actually changed here; each change is a PATCH
 * to the same `/api/v1/admin/product-groups/:id` endpoint (with only the
 * `sort` field set) used by the classic product-groups admin page's `update`
 * action - see `(admin)/admin/product-groups/+page.server.ts`.
 */
export const actions: Actions = {
	/** Clones a product (pricing + specs, created hidden) and opens the copy. */
	duplicate: async (event) => {
		const form = await event.request.formData();
		const id = Number(form.get('id'));
		if (!Number.isInteger(id) || id <= 0) {
			return fail(400, { op: 'duplicate', errorMessage: 'Invalid product id' });
		}
		const res = await apiFetch<ProductRow>(event, `/api/v1/admin/products/${id}/duplicate`, {
			method: 'POST'
		});
		if (res.error || !res.data) {
			return fail(res.status >= 400 ? res.status : 400, {
				op: 'duplicate',
				errorMessage: res.error?.message ?? 'Failed to duplicate the product'
			});
		}
		redirect(303, `/admin/products/${res.data.id}?duplicated=1`);
	},

	reorderGroups: async (event) => {
		const form = await event.request.formData();
		let changes: GroupSortChange[];
		try {
			changes = JSON.parse(String(form.get('changes') ?? '[]'));
		} catch {
			return fail(400, { op: 'reorder', errorMessage: 'Invalid reorder payload' });
		}
		if (!Array.isArray(changes) || changes.length === 0) {
			return fail(400, { op: 'reorder', errorMessage: 'No changes to apply' });
		}

		for (const change of changes) {
			const id = Number(change?.id);
			const sort = Number(change?.sort);
			if (!Number.isInteger(id) || id <= 0 || !Number.isInteger(sort)) continue;
			const res = await apiFetch(event, `/api/v1/admin/product-groups/${id}`, {
				method: 'PATCH',
				body: { sort }
			});
			if (res.error) {
				return fail(res.status >= 400 ? res.status : 400, {
					op: 'reorder',
					errorMessage: res.error.message
				});
			}
		}
		return { op: 'reorder', success: true };
	}
};
