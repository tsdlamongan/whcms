import { apiFetch } from '$lib/server/api';
import type { PageServerLoad } from './$types';
import { parseRange } from '../range';

/** One day of the orders-over-time series (adminops.OrdersReportData.points). */
interface OrdersPoint {
	period: string;
	amount: number;
	count: number;
}

/** One row of the by-status breakdown (adminops.OrderStatusCount). */
interface OrdersStatusRow {
	status: string;
	count: number;
	amount: number;
}

/** GET /api/v1/admin/reports/orders payload (adminops.OrdersReportData). */
interface OrdersReport {
	total_amount?: number;
	total_count?: number;
	points?: OrdersPoint[];
	by_status?: OrdersStatusRow[];
}

export const load: PageServerLoad = async (event) => {
	const { from, to } = parseRange(event.url);

	const res = await apiFetch<OrdersReport>(event, '/api/v1/admin/reports/orders', {
		query: { from, to }
	});

	const report = res.data ?? {};
	const byStatus = report.by_status ?? [];
	const series = report.points ?? [];

	return {
		from,
		to,
		totalOrders: report.total_count ?? series.reduce((sum, p) => sum + (p.count || 0), 0),
		totalValue: report.total_amount ?? series.reduce((sum, p) => sum + (p.amount || 0), 0),
		byStatus,
		series,
		errorMessage: res.error?.message ?? null
	};
};
