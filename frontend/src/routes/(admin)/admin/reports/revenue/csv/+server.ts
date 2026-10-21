import { proxyAdminCsv } from '$lib/server/csv';
import type { RequestHandler } from './$types';
import { parseGroupBy, parseRange } from '../../range';

/**
 * Revenue report CSV. The backend renders it itself (GET
 * /admin/reports/revenue?format=csv - period,amount,count rows plus a TOTAL
 * row), so this only forwards the same range/grouping the page is showing.
 */
export const GET: RequestHandler = (event) => {
	const { from, to } = parseRange(event.url);
	return proxyAdminCsv(
		event,
		'/api/v1/admin/reports/revenue',
		{ format: 'csv', from, to, group_by: parseGroupBy(event.url) },
		`revenue-${from}-${to}.csv`
	);
};
