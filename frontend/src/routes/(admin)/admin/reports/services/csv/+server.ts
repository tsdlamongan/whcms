import { proxyAdminCsv } from '$lib/server/csv';
import type { RequestHandler } from './$types';

/** Services report CSV (GET /admin/reports/services?format=csv - product,status,count rows). */
export const GET: RequestHandler = (event) =>
	proxyAdminCsv(event, '/api/v1/admin/reports/services', { format: 'csv' }, 'services.csv');
