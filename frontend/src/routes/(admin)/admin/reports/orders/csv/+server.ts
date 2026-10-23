import { proxyAdminCsv } from '$lib/server/csv';
import type { RequestHandler } from './$types';
import { parseRange } from '../../range';

/** Orders report CSV (GET /admin/reports/orders?format=csv - per-day rows, TOTAL, then the by-status block). */
export const GET: RequestHandler = (event) => {
	const { from, to } = parseRange(event.url);
	return proxyAdminCsv(
		event,
		'/api/v1/admin/reports/orders',
		{ format: 'csv', from, to },
		`orders-${from}-${to}.csv`
	);
};
