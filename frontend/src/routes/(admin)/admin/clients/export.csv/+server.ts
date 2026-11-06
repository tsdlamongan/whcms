import { proxyAdminCsv } from '$lib/server/csv';
import type { RequestHandler } from './$types';

/** Streams GET /api/v1/admin/clients/export (registered before /:id - see clients/handler.go). */
export const GET: RequestHandler = (event) =>
	proxyAdminCsv(event, '/api/v1/admin/clients/export', {}, 'clients.csv');
