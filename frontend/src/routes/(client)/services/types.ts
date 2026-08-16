/**
 * Shared types + helpers for the client Services pages (FE-CLIENT-SERVICES).
 * Field names mirror the JSON tags in backend/internal/domain/entities.go.
 */

import type { SpecRow } from '../../(public)/order/product/[slug]/+page.server';

export type { SpecRow };

export type ServiceStatus = 'pending' | 'active' | 'suspended' | 'terminated' | 'cancelled';

export type BillingCycle =
	'one_time' | 'monthly' | 'quarterly' | 'semiannually' | 'annually' | 'biennially';

export const SERVICE_STATUSES: readonly ServiceStatus[] = [
	'pending',
	'active',
	'suspended',
	'terminated',
	'cancelled'
] as const;

export const BILLING_CYCLES: readonly BillingCycle[] = [
	'one_time',
	'monthly',
	'quarterly',
	'semiannually',
	'annually',
	'biennially'
] as const;

/** One chosen dynamic-spec knob inside a pending upgrade (domain.UpgradeSpec). */
export interface PendingUpgradeSpec {
	key: string;
	provision_key: string;
	unit: string;
	/** -1 (UnlimitedQty) when unlimited. */
	qty: number;
	unlimited?: boolean;
	amount: number;
}

/** services.pending_upgrade JSON payload (domain.ServiceUpgrade). */
export interface PendingUpgrade {
	product_id: number;
	cycle: BillingCycle;
	recurring_amount: number;
	invoice_id: number;
	/** Present when the upgrade targets a custom-spec (configurable) product. */
	specs?: PendingUpgradeSpec[];
}

/** domain.Service (JSON tags) + optional enrichment fields the API may include. */
export interface Service {
	id: number;
	client_id: number;
	order_item_id: number | null;
	product_id: number;
	server_id: number | null;
	domain: string;
	username: string;
	status: ServiceStatus;
	billing_cycle: BillingCycle;
	recurring_amount: number;
	setup_fee: number;
	next_due_date: string | null;
	registration_date: string | null;
	terminated_at: string | null;
	suspend_reason: string;
	panel_meta: unknown;
	coupon_id: number | null;
	pending_upgrade: PendingUpgrade | string | null;
	notes: string;
	created_at: string;
	updated_at: string;
	/**
	 * Display-name enrichment (provisioning.ServiceView): attached by
	 * GET /services (+detail). Empty/absent when the referenced row is gone -
	 * render with a "#<id>" fallback.
	 */
	product_name?: string;
	server_name?: string;
	server_hostname?: string;
	/** Enrichment (guessed - not in entities.go); rendered when present. */
	renewal_invoice_id?: number | null;
}

/** Public catalog product as returned by GET /api/v1/products (pricing shape guessed). */
export interface CatalogPricing {
	cycle: BillingCycle;
	price: number;
	setup_fee: number;
}

export interface CatalogProduct {
	id: number;
	name: string;
	slug: string;
	type?: string;
	hidden?: boolean;
	configurable?: boolean;
	pricing?: CatalogPricing[];
	/**
	 * Dynamic spec knobs with per-cycle pricing. Not part of the grouped
	 * GET /products payload - the service-detail load attaches them (from
	 * GET /products/:slug) for configurable products so the upgrade modal
	 * can render the spec configurator.
	 */
	specs?: SpecRow[];
}

/**
 * GET /api/v1/products actually returns product *groups*, each with a nested
 * `products: [...]` array (`{data:[{id,name,slug,products:[...]}]}`) - not a
 * flat product array. Flatten via `groups.flatMap((g) => g.products ?? [])`
 * before use (see frontend/tests/e2e/client-manage.spec.ts's own note on
 * this, and helpers.ts's seededHostingProductId).
 */
export interface CatalogGroup {
	id: number;
	name: string;
	products?: CatalogProduct[];
}

/** IDR text for places where a component cannot be used (e.g. <option> labels). */
export { formatIDR } from '$lib/money';

/** Normalize pending_upgrade which may arrive as an embedded object or a JSON string. */
export function parsePendingUpgrade(
	value: PendingUpgrade | string | null | undefined
): PendingUpgrade | null {
	if (!value) return null;
	if (typeof value === 'string') {
		try {
			return JSON.parse(value) as PendingUpgrade;
		} catch {
			return null;
		}
	}
	return value;
}
