import { apiFetch } from '$lib/server/api';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';

/** domain.Coupon JSON tags. */
interface CouponRow {
	id: number;
	code: string;
	type: 'percentage' | 'fixed';
	value: number;
	applies_to: number[] | null;
	max_uses: number;
	used_count: number;
	recurring: boolean;
	expires_at: string | null;
	active: boolean;
	created_at: string;
	updated_at: string;
}

/** Minimal product shape for the applies-to multiselect. */
interface ProductOption {
	id: number;
	name: string;
}

export const load: PageServerLoad = async (event) => {
	const q = event.url.searchParams;
	const page = Math.max(1, Number(q.get('page')) || 1);
	const perPage = Math.min(1000, Math.max(1, Number(q.get('per_page')) || 10));
	const search = (q.get('search') ?? '').trim();

	const [couponsRes, productsRes] = await Promise.all([
		apiFetch<CouponRow[]>(event, '/api/v1/admin/coupons', {
			query: { page, per_page: perPage, search: search || undefined }
		}),
		apiFetch<ProductOption[]>(event, '/api/v1/admin/products', {
			query: { per_page: 1000 }
		})
	]);

	return {
		coupons: couponsRes.data ?? [],
		meta: couponsRes.meta,
		listError: couponsRes.error ? couponsRes.error.message : null,
		products: productsRes.data ?? [],
		page,
		perPage,
		search
	};
};

interface ParsedCoupon {
	body: {
		code: string;
		type: string;
		value: number;
		applies_to: number[];
		max_uses: number;
		recurring: boolean;
		expires_at: string | null;
		active: boolean;
	};
	errorKey: string | null;
}

function parseCouponForm(form: FormData): ParsedCoupon {
	const code = String(form.get('code') ?? '')
		.trim()
		.toUpperCase();
	const type = String(form.get('type') ?? 'percentage');
	const value = Math.round(Number(form.get('value') ?? 0) || 0);
	const appliesTo = form
		.getAll('applies_to')
		.map((v) => Number(v))
		.filter((n) => Number.isInteger(n) && n > 0);
	const expiresRaw = String(form.get('expires_at') ?? '').trim();

	let errorKey: string | null = null;
	if (!code || !value) errorKey = 'adcatalog.coupons.fillRequired';
	else if (value <= 0) errorKey = 'adcatalog.coupons.invalidValue';
	else if (type === 'percentage' && (value < 1 || value > 100)) {
		errorKey = 'adcatalog.coupons.invalidPercentage';
	}

	return {
		body: {
			code,
			type,
			value,
			applies_to: appliesTo,
			max_uses: Math.max(0, Number(form.get('max_uses') ?? 0) || 0),
			recurring: form.get('recurring') !== null,
			// Date-only input -> end of that day UTC so the coupon stays valid on its last day.
			expires_at: expiresRaw ? `${expiresRaw}T23:59:59Z` : null,
			active: form.get('active') !== null
		},
		errorKey
	};
}

export const actions: Actions = {
	create: async (event) => {
		const form = await event.request.formData();
		const { body, errorKey } = parseCouponForm(form);
		if (errorKey) return fail(400, { op: 'save', errorKey });
		const res = await apiFetch(event, '/api/v1/admin/coupons', { method: 'POST', body });
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, {
				op: 'save',
				errorMessage: res.error.message
			});
		}
		return { op: 'save', success: true };
	},

	update: async (event) => {
		const form = await event.request.formData();
		const id = Number(form.get('id'));
		const { body, errorKey } = parseCouponForm(form);
		if (!id) return fail(400, { op: 'save', errorKey: 'adcatalog.coupons.saveFailed' });
		if (errorKey) return fail(400, { op: 'save', errorKey });
		const res = await apiFetch(event, `/api/v1/admin/coupons/${id}`, { method: 'PATCH', body });
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, {
				op: 'save',
				errorMessage: res.error.message
			});
		}
		return { op: 'save', success: true };
	},

	delete: async (event) => {
		const form = await event.request.formData();
		const id = Number(form.get('id'));
		if (!id) return fail(400, { op: 'delete', errorKey: 'adcatalog.coupons.deleteFailed' });
		const res = await apiFetch(event, `/api/v1/admin/coupons/${id}`, { method: 'DELETE' });
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, {
				op: 'delete',
				errorMessage: res.error.message
			});
		}
		return { op: 'delete', success: true };
	}
};
