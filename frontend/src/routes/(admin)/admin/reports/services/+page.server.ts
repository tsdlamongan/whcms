import { apiFetch } from '$lib/server/api';
import type { PageServerLoad } from './$types';

/** One product/status cell of the report (adminops.ProductStatusCount). */
interface ServicesProductRow {
	product: string;
	status: string;
	count: number;
}

/**
 * GET /api/v1/admin/reports/services payload (adminops.ServicesReportData):
 * `by_status` is a {status: count} MAP and `by_product` one row per
 * product+status pair - not the pre-aggregated arrays a chart wants.
 */
interface ServicesReport {
	by_status?: Record<string, number> | null;
	by_product?: ServicesProductRow[] | null;
}

export interface ProductSummary {
	name: string;
	count: number;
	/** Per-status split, biggest first - shown under the product name. */
	breakdown: { status: string; count: number }[];
}

export const load: PageServerLoad = async (event) => {
	const res = await apiFetch<ServicesReport>(event, '/api/v1/admin/reports/services');
	const report = res.data ?? {};

	const byStatus = Object.entries(report.by_status ?? {})
		.map(([status, count]) => ({ status, count: count || 0 }))
		.sort((a, b) => b.count - a.count || a.status.localeCompare(b.status));
	const total = byStatus.reduce((sum, row) => sum + row.count, 0);

	const products = new Map<string, ProductSummary>();
	for (const row of report.by_product ?? []) {
		const name = row.product || '(unknown product)';
		const entry = products.get(name) ?? { name, count: 0, breakdown: [] };
		entry.count += row.count || 0;
		entry.breakdown.push({ status: row.status, count: row.count || 0 });
		products.set(name, entry);
	}
	const byProduct = [...products.values()]
		.map((p) => ({
			...p,
			breakdown: p.breakdown.sort((a, b) => b.count - a.count || a.status.localeCompare(b.status))
		}))
		.sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));

	return {
		total,
		byStatus,
		byProduct,
		errorMessage: res.error?.message ?? null
	};
};
