/** snipe.ClientSnipe JSON tags (what an owner may see of a hunt). */
export interface ClientSnipe {
	id: number;
	domain_name: string;
	years: number;
	status: SnipeStatus;
	phase?: 'patient' | 'burst';
	hunt_from: string | null;
	hunt_until: string | null;
	nameservers: string[];
	invoice_id: number | null;
	plan_id: number | null;
	plan_name: string;
	fee_amount: number;
	domain_amount: number;
	refundable_domain: number;
	refundable_fee: number;
	refund_amount: number;
	refunded_at: string | null;
	attempts: number;
	paid_at: string | null;
	deadline_at: string | null;
	won_at: string | null;
	won_domain_id: number | null;
	created_at: string;
}

export type SnipeStatus = 'pending_payment' | 'active' | 'won' | 'expired' | 'cancelled' | 'failed';

export const SNIPE_STATUSES: SnipeStatus[] = [
	'pending_payment',
	'active',
	'won',
	'expired',
	'cancelled',
	'failed'
];

export interface ClientEvent {
	kind: string;
	created_at: string;
}

/** snipe.PlanView. */
export interface SnipePlanView {
	id: number;
	name: string;
	window_days: number;
	fee: number;
	success_fee: number;
}

/** snipe.Quote. */
export interface SnipeQuote {
	domain: string;
	years: number;
	plan_id: number;
	plan_name: string;
	window_days: number;
	fee: number;
	domain_price: number;
	success_fee: number;
	subtotal: number;
	tax_rate: number;
	tax_inclusive: boolean;
	tax: number;
	total: number;
	refundable_if_lost: number;
	renew_per_year: number;
}

/** StatusBadge colour for a snipe status. */
export function snipeVariant(status: string): 'green' | 'yellow' | 'red' | 'gray' | 'blue' {
	switch (status) {
		case 'pending_payment':
			return 'yellow';
		case 'active':
			return 'blue';
		case 'won':
			return 'green';
		case 'failed':
			return 'red';
		default:
			return 'gray';
	}
}

export function isTerminal(status: string): boolean {
	return status === 'won' || status === 'expired' || status === 'cancelled' || status === 'failed';
}
