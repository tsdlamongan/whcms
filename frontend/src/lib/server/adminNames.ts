import { apiFetch } from '$lib/server/api';
import type { RequestEvent } from '@sveltejs/kit';

/**
 * Admin list endpoints (orders, invoices, services, domains, tickets,
 * transactions) return rows that only carry foreign keys (`client_id`,
 * `invoice_id`). The tables want a readable client name / invoice number, so
 * these helpers resolve them for ONE page of rows with a few batched calls
 * (`GET /admin/clients?ids=`, `GET /admin/invoices?ids=`, at most `ID_BATCH`
 * ids each) instead of one request per row.
 *
 * Every lookup is best-effort: staff without the `clients` / `billing`
 * permission get a 403 and the page simply falls back to `#<id>` (the same
 * fallback the templates already render), never an error.
 */

interface ClientRow {
	id: number;
	first_name?: string;
	last_name?: string;
	company?: string;
	email?: string;
}

interface InvoiceRow {
	id: number;
	invoice_number: string;
	client_id: number;
}

/**
 * Ids per lookup request. A 1000-row page can reference ~900 distinct clients;
 * as one `?ids=` list that is a ~4.4 KB query string, which the API (and any
 * proxy) rejects with 431 - and the best-effort lookup then silently degrades
 * every row to `#id`. Batches this size stay far below that limit.
 */
const ID_BATCH = 100;

/** "First Last", else company, else email, else `#id`. */
export function clientDisplayName(c: ClientRow): string {
	const name = [c.first_name, c.last_name].filter(Boolean).join(' ').trim();
	return name || c.company || c.email || `#${c.id}`;
}

function uniqueIds(ids: Iterable<number | null | undefined>): number[] {
	return [...new Set([...ids].filter((n): n is number => typeof n === 'number' && n > 0))];
}

/** Rows of `path` (an admin list endpoint) matching `ids`, fetched in `ID_BATCH`-sized requests. */
async function fetchByIds<T>(event: RequestEvent, path: string, ids: number[]): Promise<T[]> {
	const batches: number[][] = [];
	for (let i = 0; i < ids.length; i += ID_BATCH) batches.push(ids.slice(i, i + ID_BATCH));
	const pages = await Promise.all(
		batches.map((batch) =>
			apiFetch<T[]>(event, path, { query: { ids: batch.join(','), per_page: batch.length } })
		)
	);
	return pages.flatMap((page) => page.data ?? []);
}

/** id -> display name for the given client ids (empty map on any failure). */
export async function clientNamesFor(
	event: RequestEvent,
	ids: Iterable<number | null | undefined>
): Promise<Map<number, string>> {
	const wanted = uniqueIds(ids);
	const names = new Map<number, string>();
	if (wanted.length === 0) return names;

	for (const c of await fetchByIds<ClientRow>(event, '/api/v1/admin/clients', wanted)) {
		names.set(c.id, clientDisplayName(c));
	}
	return names;
}

/** Adds `client_name` to every row that has a resolvable `client_id`. */
export async function withClientNames<T extends { client_id?: number | null }>(
	event: RequestEvent,
	rows: T[]
): Promise<(T & { client_name?: string })[]> {
	const names = await clientNamesFor(
		event,
		rows.map((r) => r.client_id)
	);
	return rows.map((r) => {
		const name = r.client_id ? names.get(r.client_id) : undefined;
		return name ? { ...r, client_name: name } : r;
	});
}

/**
 * Adds `invoice_number` and `client_name` to transaction rows (a transaction
 * only knows its `invoice_id`; the client comes from that invoice).
 */
export async function withInvoiceInfo<T extends { invoice_id: number }>(
	event: RequestEvent,
	rows: T[]
): Promise<(T & { invoice_number?: string; client_name?: string })[]> {
	const wanted = uniqueIds(rows.map((r) => r.invoice_id));
	if (wanted.length === 0) return rows;

	const invoices = new Map(
		(await fetchByIds<InvoiceRow>(event, '/api/v1/admin/invoices', wanted)).map(
			(i) => [i.id, i] as const
		)
	);
	const names = await clientNamesFor(
		event,
		[...invoices.values()].map((i) => i.client_id)
	);

	return rows.map((r) => {
		const inv = invoices.get(r.invoice_id);
		if (!inv) return r;
		return { ...r, invoice_number: inv.invoice_number, client_name: names.get(inv.client_id) };
	});
}
