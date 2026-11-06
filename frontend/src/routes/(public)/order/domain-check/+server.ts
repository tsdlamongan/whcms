/**
 * FE-ORDER - same-origin proxy for the debounced domain availability check.
 * Browser code must not call the Go API directly (CONTRACTS.md §13), so the
 * product configure page and domain search POST here instead.
 */
import { apiFetch } from '$lib/server/api';
import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';

interface DomainAvailability {
	name: string;
	available: boolean;
	premium: boolean;
	price: number;
	/** Full year-1..10 register price matrix - present only when `price` came
	 *  from an active TLDPricing row (not a flat annual rate; a multi-year
	 *  estimate must look this up instead of assuming price * years). */
	register_prices?: Record<string, number>;
	/** The extension has an ACTIVE Domain Pricing row (is for sale). */
	sellable?: boolean;
}

/** GET /api/v1/domains/tlds response row (domain.TLDPricing JSON tags). */
interface ActiveTLD {
	tld: string;
	min_years: number;
	max_years: number;
}

const DOMAIN_RE = /^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;

/** Everything after the first label, e.g. "example.co.id" -> "co.id". */
function extensionOf(name: string): string {
	const i = name.indexOf('.');
	return i < 0 ? '' : name.slice(i + 1);
}

export const POST: RequestHandler = async (event) => {
	let payload: unknown;
	try {
		payload = await event.request.json();
	} catch {
		return json({ results: null, error: 'invalid JSON body' }, { status: 400 });
	}

	const names = (payload as { names?: unknown })?.names;
	if (!Array.isArray(names) || names.length === 0 || names.length > 20) {
		return json(
			{ results: null, error: 'names must be a non-empty array (max 20)' },
			{ status: 400 }
		);
	}

	const cleaned = [
		...new Set(
			names
				.filter((n): n is string => typeof n === 'string')
				.map((n) => n.trim().toLowerCase())
				.filter((n) => DOMAIN_RE.test(n))
		)
	];
	if (cleaned.length === 0) {
		return json({ results: null, error: 'no valid domain names' }, { status: 400 });
	}

	const [res, tldsRes] = await Promise.all([
		apiFetch<DomainAvailability[]>(event, '/api/v1/domains/check', {
			method: 'POST',
			body: { names: cleaned }
		}),
		apiFetch<ActiveTLD[]>(event, '/api/v1/domains/tlds')
	]);

	if (res.error) {
		return json(
			{ results: null, error: res.error.message },
			{ status: res.status >= 400 ? res.status : 502 }
		);
	}

	const tldByExt = new Map((tldsRes.data ?? []).map((t) => [t.tld, t]));
	const results = (res.data ?? []).map((r) => {
		const t = tldByExt.get(extensionOf(r.name));
		return { ...r, min_years: t?.min_years ?? 1, max_years: t?.max_years ?? 10 };
	});

	return json({ results, error: null });
};
