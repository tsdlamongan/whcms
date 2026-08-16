import { apiFetch } from '$lib/server/api';
import type { RequestEvent } from '@sveltejs/kit';

/** Browser-facing CAPTCHA config (never carries the secret key). */
export interface CaptchaConfig {
	enabled: boolean;
	provider: string;
	siteKey: string;
}

/** Browser-facing billing tax config, for order/cart price previews. */
export interface TaxConfig {
	enabled: boolean;
	rate: number;
	inclusive: boolean;
}

interface PublicConfigResponse {
	captcha: { enabled: boolean; provider: string; site_key: string };
	billing?: { tax_enabled: boolean; tax_rate: number; tax_inclusive: boolean };
	security?: { require_email_verification: boolean };
}

const CAPTCHA_DISABLED: CaptchaConfig = { enabled: false, provider: 'turnstile', siteKey: '' };
// Fail-closed: if the real setting can't be loaded, show no tax rather than
// guessing a rate the server might not actually charge.
const TAX_DISABLED: TaxConfig = { enabled: false, rate: 11, inclusive: false };

// Both loaders below read the same GET /api/v1/public/config endpoint and are
// frequently called from the same load function - dedupe to one upstream
// request per SSR request.
const publicConfigCache = new WeakMap<
	RequestEvent,
	Promise<{ data: PublicConfigResponse | null; error: unknown }>
>();

function fetchPublicConfig(event: RequestEvent) {
	let p = publicConfigCache.get(event);
	if (!p) {
		p = apiFetch<PublicConfigResponse>(event, '/api/v1/public/config', { token: null });
		publicConfigCache.set(event, p);
	}
	return p;
}

/**
 * Load the CAPTCHA config from the public API (GET /api/v1/public/config).
 * Never throws - on any error it returns a disabled config so the page still
 * renders (the backend remains the source of truth for enforcement).
 */
export async function loadCaptchaConfig(event: RequestEvent): Promise<CaptchaConfig> {
	const res = await fetchPublicConfig(event);
	const c = res.data?.captcha;
	if (res.error || !c) return CAPTCHA_DISABLED;
	return {
		enabled: c.enabled === true,
		provider: c.provider || 'turnstile',
		siteKey: c.site_key ?? ''
	};
}

/**
 * Load the effective billing tax config from the same public API - so order/
 * cart price previews reflect the admin's actual General Settings toggle
 * instead of assuming tax is always on. Never throws - on any error it
 * returns a disabled config (no tax shown), matching the server-side default.
 */
export async function loadTaxConfig(event: RequestEvent): Promise<TaxConfig> {
	const res = await fetchPublicConfig(event);
	const b = res.data?.billing;
	if (res.error || !b) return TAX_DISABLED;
	return {
		enabled: b.tax_enabled === true,
		rate: typeof b.tax_rate === 'number' ? b.tax_rate : 11,
		inclusive: b.tax_inclusive === true
	};
}

/**
 * Whether checkout requires a verified email (security.require_email_verification,
 * from the same public config endpoint). Mirrors the backend gate for UI hints
 * (cart verify panel, dashboard banner); the backend still enforces it at
 * POST /orders. Never throws - on any error it returns true (fail-closed: the
 * UI hints at verification rather than letting the user run into the raw
 * backend error).
 */
export async function loadRequireEmailVerification(event: RequestEvent): Promise<boolean> {
	const res = await fetchPublicConfig(event);
	const s = res.data?.security;
	if (res.error || !s) return true;
	return s.require_email_verification !== false;
}

/** Extract the posted CAPTCHA token from a form body (empty string when absent). */
export function captchaToken(form: FormData): string {
	return String(form.get('captcha_token') ?? '').trim();
}
