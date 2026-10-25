import { apiFetch } from '$lib/server/api';
import type { PageServerLoad } from './$types';

export interface DomainCheckResult {
	name: string;
	available: boolean;
	premium: boolean;
	price: number;
	/** Full year-1..10 register price matrix - present only when `price` came
	 *  from an active TLDPricing row (not a flat annual rate; a multi-year
	 *  estimate must look this up instead of assuming price * years). */
	register_prices?: Record<string, number>;
	/** The extension needs identity/legal documents after registration. */
	document_required?: boolean;
	/** The extension has an ACTIVE Domain Pricing row: only then is it for sale
	 *  (no price is shown and checkout refuses it otherwise). */
	sellable?: boolean;
	min_years: number;
	max_years: number;
}

export interface DomainAddonOption {
	key: string;
	name: string;
	price: number;
}

/** GET /api/v1/domains/tlds response row (domain.TLDPricing JSON tags). */
interface ActiveTLD {
	tld: string;
	min_years: number;
	max_years: number;
}

const LABEL_RE = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
const DOMAIN_RE = /^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;

/** Everything after the first label, e.g. "example.co.id" -> "co.id". */
function extensionOf(name: string): string {
	const i = name.indexOf('.');
	return i < 0 ? '' : name.slice(i + 1);
}

/** Build candidate names for a raw query ("mysite" or "mysite.com"). */
function candidates(q: string, tlds: string[]): string[] | null {
	const cleaned = q
		.trim()
		.toLowerCase()
		.replace(/^https?:\/\//, '')
		.replace(/^www\./, '')
		.replace(/\/.*$/, '');
	const base = cleaned.split('.')[0] ?? '';
	if (!LABEL_RE.test(base)) return null;

	const names = new Set<string>();
	if (cleaned.includes('.') && DOMAIN_RE.test(cleaned)) names.add(cleaned);
	for (const tld of tlds) names.add(`${base}.${tld}`);
	return [...names].slice(0, 10);
}

export const load: PageServerLoad = async (event) => {
	const [tldsRes, addonsRes] = await Promise.all([
		apiFetch<ActiveTLD[]>(event, '/api/v1/domains/tlds'),
		apiFetch<DomainAddonOption[]>(event, '/api/v1/domains/addons')
	]);
	// A hardcoded TLD list would silently drift from the admin-managed catalog,
	// so on fetch failure we degrade to a smaller candidate set (exact-query-only
	// matching) rather than a broken page.
	const tlds = tldsRes.data ?? [];
	const addons = addonsRes.data ?? [];
	const tldByExt = new Map(tlds.map((t) => [t.tld, t]));
	// The extensions on sale; null when the catalog could not be loaded (then
	// nothing is blocked client-side - the server still refuses at checkout).
	const sellableTlds: string[] | null = tldsRes.data ? tlds.map((t) => t.tld) : null;

	const q = event.url.searchParams.get('q')?.trim() ?? '';
	if (!q) {
		return {
			q: '',
			results: null,
			invalidQuery: false,
			searchError: null,
			addons,
			sellableTlds
		};
	}

	const names = candidates(
		q,
		tlds.map((t) => t.tld)
	);
	if (!names) {
		return { q, results: null, invalidQuery: true, searchError: null, addons, sellableTlds };
	}

	const res = await apiFetch<Omit<DomainCheckResult, 'min_years' | 'max_years'>[]>(
		event,
		'/api/v1/domains/check',
		{ method: 'POST', body: { names } }
	);

	if (res.error) {
		return {
			q,
			results: null,
			invalidQuery: false,
			searchError: res.error.message,
			addons,
			sellableTlds
		};
	}

	// Preserve the candidate order (exact query first, then TLD alternates).
	const byName = new Map((res.data ?? []).map((r) => [r.name, r]));
	const results: DomainCheckResult[] = names
		.map((n) => byName.get(n))
		.filter((r): r is Omit<DomainCheckResult, 'min_years' | 'max_years'> => r !== undefined)
		.map((r) => {
			const t = tldByExt.get(extensionOf(r.name));
			return { ...r, min_years: t?.min_years ?? 1, max_years: t?.max_years ?? 10 };
		});

	return { q, results, invalidQuery: false, searchError: null, addons, sellableTlds };
};
