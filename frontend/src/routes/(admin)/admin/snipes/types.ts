/** domain.DomainSnipe JSON tags (+ the client name joined in by the BFF). */
export interface AdminSnipe {
	id: number;
	client_id: number;
	plan_id: number | null;
	source: 'client' | 'admin';
	domain_name: string;
	years: number;
	status: SnipeStatus;
	phase: 'patient' | 'burst';
	hunt_from: string | null;
	hunt_until: string | null;
	nameservers: string[] | null;
	invoice_id: number | null;
	fee_amount: number;
	domain_amount: number;
	renew_per_year: number;
	refundable_domain: number;
	refundable_fee: number;
	refund_amount: number;
	refunded_at: string | null;
	attempts: number;
	last_attempt_at: string | null;
	last_error: string;
	paid_at: string | null;
	deadline_at: string | null;
	won_at: string | null;
	won_domain_id: number | null;
	created_at: string;
	updated_at: string;
	client_name?: string;
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

/** domain.SnipeEvent JSON tags. */
export interface SnipeEvent {
	id: number;
	snipe_id: number;
	kind: string;
	message: string;
	created_at: string;
}

/** snipe.StatusResponse (GET /admin/snipes/status). */
export interface SnipeOverview {
	enabled: boolean;
	pause_reason: string;
	paused_at?: string;
	counts: Record<string, number>;
	hunting: number;
	reseller_balance: number | null;
	reseller_error?: string;
}

/** hp-badge colour class for a snipe status. */
export function snipeStatusClass(s: string): string {
	switch (s) {
		case 'active':
		case 'won':
			return 'active';
		case 'pending_payment':
			return 'pending';
		case 'failed':
			return 'terminated';
		default:
			return 'cancelled';
	}
}

export function isTerminal(s: string): boolean {
	return s === 'won' || s === 'expired' || s === 'cancelled' || s === 'failed';
}

export { jakartaToISO } from '$lib/snipe';

/** One admin-editable hunt setting (the generic settings endpoint's snipe.* keys). */
export interface SnipeSettingDef {
	key: string;
	label: string;
	kind: 'bool' | 'int';
	unit?: string;
	help: string;
	def: boolean | number;
}

export const SNIPE_SETTINGS: SnipeSettingDef[] = [
	{
		key: 'snipe.enabled',
		label: 'Sniper enabled',
		kind: 'bool',
		help: 'Master switch. Off stops every running hunt (they stay active and resume when it is on again).',
		def: true
	},
	{
		key: 'snipe.max_active_global',
		label: 'Max hunts at once',
		kind: 'int',
		help: 'Hunts running at the same time across the whole system. Clients cannot buy a new hunt once active + unpaid ones reach this number.',
		def: 50
	},
	{
		key: 'snipe.max_in_flight',
		label: 'Max concurrent registrar requests',
		kind: 'int',
		help: 'Shared by every hunt. Keep low: too many parallel calls can get the reseller key blocked.',
		def: 2
	},
	{
		key: 'snipe.burst_interval_ms',
		label: 'Burst interval',
		kind: 'int',
		unit: 'ms',
		help: 'Pause between register attempts of one hunt while bursting.',
		def: 200
	},
	{
		key: 'snipe.burst_jitter_ms',
		label: 'Burst jitter',
		kind: 'int',
		unit: 'ms',
		help: 'Random extra added to every burst pause.',
		def: 100
	},
	{
		key: 'snipe.patient_interval_seconds',
		label: 'Availability probe interval',
		kind: 'int',
		unit: 's',
		help: 'How often a patient hunt checks whether the name dropped.',
		def: 10
	},
	{
		key: 'snipe.burst_lead_seconds',
		label: 'Burst lead time',
		kind: 'int',
		unit: 's',
		help: 'Start bursting this long before the owner-supplied drop window opens.',
		def: 60
	},
	{
		key: 'snipe.rate_limit_backoff_ms',
		label: 'Rate-limit backoff',
		kind: 'int',
		unit: 'ms',
		help: 'Minimum wait after the registrar answers 429 (a longer Retry-After wins).',
		def: 2000
	},
	{
		key: 'snipe.request_timeout_ms',
		label: 'Request timeout',
		kind: 'int',
		unit: 'ms',
		help: 'Per registrar request. A timed-out register call is verified before retrying.',
		def: 3000
	},
	{
		key: 'snipe.max_active_per_client',
		label: 'Max open snipes per client',
		kind: 'int',
		help: 'Pending-payment plus active snipes one client may have.',
		def: 5
	},
	{
		key: 'snipe.invoice_due_hours',
		label: 'Snipe invoice due',
		kind: 'int',
		unit: 'h',
		help: 'How long a client has to pay before the pending snipe is released.',
		def: 24
	},
	{
		key: 'snipe.pause_probe_seconds',
		label: 'Pause re-check interval',
		kind: 'int',
		unit: 's',
		help: 'While paused, how often the reseller account is re-checked (an empty balance only resumes after a top-up).',
		def: 60
	},
	{
		key: 'snipe.min_reseller_balance',
		label: 'Reseller balance floor',
		kind: 'int',
		unit: 'IDR',
		help: 'Hunts pause while the reseller balance is below this, protecting paid registrations.',
		def: 0
	}
];
