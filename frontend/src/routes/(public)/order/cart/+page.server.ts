import { apiFetch } from '$lib/server/api';
import {
	captchaToken,
	loadCaptchaConfig,
	loadRequireEmailVerification,
	loadTaxConfig
} from '$lib/server/captcha';
import { setSessionCookies } from '$lib/server/session';
import type { AppliedCoupon, BillingCycle, CartItemType } from '$lib/stores/cart.svelte';
import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';

interface LoginResponse {
	access_token: string;
	refresh_token: string;
	user?: { id: number; email: string; role: string };
}

/** POST /api/v1/orders response (shape guessed - see report). */
interface CheckoutResponse {
	order?: { id: number; order_number?: string };
	invoice?: { id: number; invoice_number?: string };
	order_id?: number;
	invoice_id?: number;
}

/** Coupon entity per entities.go JSON tags. */
interface CouponResponse {
	code: string;
	type: 'percentage' | 'fixed';
	value: number;
	applies_to: unknown;
	recurring: boolean;
}

interface OrderItemInput {
	item_type: CartItemType;
	product_id?: number;
	domain?: string;
	cycle: BillingCycle;
	options?: Array<{ option_id: number; value_id: number }>;
	specs?: Array<{ key: string; qty: number; unlimited?: boolean }>;
	epp_code?: string;
	domain_years?: number;
	domain_addons?: string[];
}

const ITEM_TYPES: CartItemType[] = ['product', 'domain_register', 'domain_transfer'];
const CYCLES: BillingCycle[] = [
	'one_time',
	'monthly',
	'quarterly',
	'semiannually',
	'annually',
	'biennially'
];

/** Normalize coupons.applies_to JSONB into a product-id list (null = all). */
function normalizeAppliesTo(raw: unknown): number[] | null {
	let list: unknown = raw;
	if (raw && typeof raw === 'object' && !Array.isArray(raw)) {
		list = (raw as Record<string, unknown>).products;
	}
	if (!Array.isArray(list)) return null;
	const ids = list.filter((n): n is number => typeof n === 'number');
	return ids.length > 0 ? ids : null;
}

/** Re-validate the client-supplied cart JSON into the POST /orders item shape. */
function parseItems(raw: string): OrderItemInput[] | null {
	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch {
		return null;
	}
	if (!Array.isArray(parsed) || parsed.length === 0 || parsed.length > 50) return null;

	const items: OrderItemInput[] = [];
	for (const entry of parsed) {
		if (!entry || typeof entry !== 'object') return null;
		const e = entry as Record<string, unknown>;
		const itemType = e.item_type as CartItemType;
		const cycle = e.cycle as BillingCycle;
		if (!ITEM_TYPES.includes(itemType) || !CYCLES.includes(cycle)) return null;

		const item: OrderItemInput = { item_type: itemType, cycle };
		if (typeof e.product_id === 'number') item.product_id = e.product_id;
		if (typeof e.domain === 'string' && e.domain) item.domain = e.domain.toLowerCase();
		if (typeof e.epp_code === 'string' && e.epp_code) item.epp_code = e.epp_code;
		if (typeof e.domain_years === 'number' && e.domain_years > 0)
			item.domain_years = e.domain_years;
		if (Array.isArray(e.domain_addons)) {
			const addons = e.domain_addons.filter((k): k is string => typeof k === 'string');
			if (addons.length > 0) item.domain_addons = addons;
		}
		// Configurable-option selections: array of {option_id, value_id} (the Go
		// DTO shape emitted by cart.toOrderPayloadItems).
		if (Array.isArray(e.options)) {
			const options: Array<{ option_id: number; value_id: number }> = [];
			for (const o of e.options as unknown[]) {
				if (o && typeof o === 'object') {
					const oo = o as Record<string, unknown>;
					if (typeof oo.option_id === 'number' && typeof oo.value_id === 'number') {
						options.push({ option_id: oo.option_id, value_id: oo.value_id });
					}
				}
			}
			if (options.length > 0) item.options = options;
		}

		// Dynamic-product spec selections: array of {key, qty, unlimited?}.
		if (Array.isArray(e.specs)) {
			const specs: Array<{ key: string; qty: number; unlimited?: boolean }> = [];
			for (const s of e.specs as unknown[]) {
				if (s && typeof s === 'object') {
					const ss = s as Record<string, unknown>;
					if (typeof ss.key === 'string' && typeof ss.qty === 'number') {
						specs.push({
							key: ss.key,
							qty: ss.qty,
							unlimited: ss.unlimited === true || undefined
						});
					}
				}
			}
			if (specs.length > 0) item.specs = specs;
		}

		// Structural sanity: products need product_id, domain items need a domain.
		if (itemType === 'product' && item.product_id === undefined) return null;
		if (itemType !== 'product' && !item.domain) return null;
		items.push(item);
	}
	return items;
}

export const load: PageServerLoad = async (event) => {
	const captcha = await loadCaptchaConfig(event);
	const tax = await loadTaxConfig(event);

	if (!event.locals.user) {
		return { user: null, emailVerified: false, captcha, tax };
	}

	// locals.user.email_verified comes from hooks.server.ts (/auth/me user DTO).
	// NOTE: a previous version re-fetched /auth/me here and read a flat
	// `email_verified_at` field that the nested MeResponse never had - the
	// check always passed and the verify-required panel never rendered,
	// letting unverified users hit the raw backend checkout error instead.
	// When the admin disables security.require_email_verification the backend
	// gate is off too, so treat everyone as verified and keep checkout open.
	const requireVerify = await loadRequireEmailVerification(event);
	return {
		user: event.locals.user,
		emailVerified: event.locals.user.email_verified || !requireVerify,
		captcha,
		tax
	};
};

export const actions: Actions = {
	/** Inline login panel (mirrors the /login page pattern, but stays on the cart). */
	login: async (event) => {
		const form = await event.request.formData();
		const email = String(form.get('email') ?? '').trim();
		const password = String(form.get('password') ?? '');

		if (!email || !password) {
			return fail(400, { loginErrorKey: 'auth.fillAllFields', loginEmail: email });
		}

		const loginCaptcha = captchaToken(form);
		const res = await apiFetch<LoginResponse>(event, '/api/v1/auth/login', {
			method: 'POST',
			body: { email, password, ...(loginCaptcha ? { captcha_token: loginCaptcha } : {}) },
			token: null
		});

		if (res.error || !res.data) {
			const invalid = res.error?.code === 'UNAUTHORIZED' || res.status === 401;
			return fail(res.status >= 400 ? res.status : 400, {
				loginErrorKey: invalid ? 'orderfe.cart.loginFailed' : undefined,
				loginErrorMessage: invalid ? undefined : (res.error?.message ?? 'Login failed'),
				loginEmail: email
			});
		}

		setSessionCookies(event.cookies, {
			access_token: res.data.access_token,
			refresh_token: res.data.refresh_token
		});
		return { loginSuccess: true };
	},

	/** Inline registration (reuses POST /api/v1/auth/register). */
	register: async (event) => {
		const form = await event.request.formData();
		const value = (name: string) => String(form.get(name) ?? '').trim();

		const email = value('email');
		const password = String(form.get('password') ?? '');
		const confirm = String(form.get('confirm_password') ?? '');
		const body = {
			email,
			password,
			first_name: value('first_name'),
			last_name: value('last_name'),
			company: value('company'),
			address1: value('address1'),
			city: value('city'),
			postcode: value('postcode'),
			country: value('country') || 'ID',
			phone: value('phone')
		};

		const values = { ...body, password: '', confirm_password: '' };

		if (!email || !password || !body.first_name || !body.last_name) {
			return fail(400, {
				registerErrorKey: 'orderfe.register.fillRequired',
				registerValues: values
			});
		}
		if (password !== confirm) {
			return fail(400, {
				registerErrorKey: 'orderfe.register.passwordMismatch',
				registerValues: values
			});
		}

		const registerCaptcha = captchaToken(form);
		const res = await apiFetch<unknown>(event, '/api/v1/auth/register', {
			method: 'POST',
			body: { ...body, ...(registerCaptcha ? { captcha_token: registerCaptcha } : {}) },
			token: null
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, {
				registerErrorKey: res.error.code === 'VALIDATION' ? 'orderfe.register.failed' : undefined,
				registerErrorMessage:
					res.error.code === 'VALIDATION' ? res.error.message : (res.error.message ?? 'error'),
				registerValues: values
			});
		}

		return { registerSuccess: true, registeredEmail: email };
	},

	/** Resend the verification email for the signed-in (unverified) user. */
	resend: async (event) => {
		if (!event.locals.user) {
			return fail(401, { resendError: true });
		}
		const res = await apiFetch<unknown>(event, '/api/v1/auth/resend-verification', {
			method: 'POST',
			body: { email: event.locals.user.email }
		});
		if (res.error) {
			return fail(res.status >= 400 ? res.status : 400, { resendError: true });
		}
		return { resendSuccess: true };
	},

	/** Validate a coupon code; the client stores the result for the totals preview. */
	coupon: async (event) => {
		const form = await event.request.formData();
		const code = String(form.get('code') ?? '')
			.trim()
			.toUpperCase();
		if (!code) {
			return fail(400, { couponErrorKey: 'orderfe.cart.couponRequired' });
		}

		const res = await apiFetch<CouponResponse>(event, '/api/v1/coupons/validate', {
			method: 'POST',
			body: { code }
		});

		if (res.error || !res.data) {
			return fail(res.status >= 400 ? res.status : 400, {
				couponErrorKey: 'orderfe.cart.couponInvalid',
				couponCode: code
			});
		}

		const applied: AppliedCoupon = {
			code: res.data.code || code,
			type: res.data.type,
			value: res.data.value,
			applies_to: normalizeAppliesTo(res.data.applies_to),
			recurring: res.data.recurring
		};
		return { couponApplied: applied };
	},

	/** POST /api/v1/orders -> redirect to the created invoice (CONTRACTS.md §8). */
	checkout: async (event) => {
		if (!event.locals.user) {
			return fail(401, { checkoutErrorKey: 'orderfe.cart.authRequiredDesc' });
		}
		if (event.locals.user.role !== 'client') {
			return fail(403, { checkoutErrorKey: 'orderfe.cart.staffCannotOrder' });
		}

		const form = await event.request.formData();
		const items = parseItems(String(form.get('items') ?? ''));
		if (!items) {
			return fail(400, { checkoutErrorKey: 'orderfe.cart.checkoutEmpty' });
		}

		const coupon = String(form.get('coupon') ?? '').trim();
		const body: Record<string, unknown> = { items };
		if (coupon) body.coupon = coupon;
		const checkoutCaptcha = captchaToken(form);
		if (checkoutCaptcha) body.captcha_token = checkoutCaptcha;

		const res = await apiFetch<CheckoutResponse>(event, '/api/v1/orders', {
			method: 'POST',
			body
		});

		if (res.error || !res.data) {
			return fail(res.status >= 400 ? res.status : 400, {
				checkoutErrorMessage: res.error?.message ?? 'error',
				checkoutErrorCode: res.error?.code
			});
		}

		const invoiceId = res.data.invoice?.id ?? res.data.invoice_id;
		redirect(303, invoiceId ? `/billing/invoices/${invoiceId}` : '/billing');
	}
};
