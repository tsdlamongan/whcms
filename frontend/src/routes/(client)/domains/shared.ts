/** Shared types/constants for the client domains routes (FE-CLIENT-DOMAINS). */
import { requiresEmailVerification } from '$lib/domainDocuments';

export const DOMAIN_TABS = ['overview', 'nameservers', 'dns', 'epp', 'contact', 'addons'] as const;
export type DomainTab = (typeof DOMAIN_TABS)[number];

/** CONTRACTS.md §4 domain status enum. */
export const DOMAIN_STATUSES = [
	'pending',
	'active',
	'pending_transfer',
	'expired',
	'cancelled',
	'suspended',
	'rejected'
] as const;

/** Registrar-reported verification state (`domain.DomainVerification`). */
export type VerificationStatus = '' | 'waiting' | 'verifying' | 'document_validating' | 'active';

/** The documented upload/suspend/delete timeline while verification is open
 *  (`domain.VerificationDeadlines`). Dates are YYYY-MM-DD. */
export interface VerificationDeadlines {
	kind: 'documents' | 'email';
	upload_by?: string;
	suspend_on: string;
	delete_on?: string;
}

/** Matches what the registrar actually accepts (no TXT/NS/SRV/CAA; SPF is its
 *  own legacy record type there, not folded into TXT). */
export const DNS_TYPES = ['A', 'AAAA', 'CNAME', 'MX', 'SPF'] as const;

/** Mirrors backend `domain.Domain` JSON tags (entities.go). */
export interface DomainRow {
	id: number;
	client_id: number;
	registrar_id: number;
	name: string;
	status: string;
	registration_date: string | null;
	expiry_date: string | null;
	next_due_date: string | null;
	recurring_amount: number;
	billing_cycle: string;
	auto_renew: boolean;
	nameservers: unknown;
	id_protection: boolean;
	dns_management_enabled: boolean;
	email_forwarding_enabled: boolean;
	registrar_meta?: unknown;
	created_at?: string;
	updated_at?: string;
	/** Document / verification lifecycle (RDash Auto Activation). */
	document_required: boolean;
	verification_status: VerificationStatus;
	status_reason: string;
	document_rejected_at: string | null;
	registrar_refundable: boolean;
	refund_amount: number;
	refunded_at: string | null;
	verification_deadlines: VerificationDeadlines | null;
}

/** Statuses in which a domain can still owe the registrar documents/verification. */
const OPEN_STATUSES = ['pending', 'active', 'pending_transfer', 'suspended'];

/** The domain still owes the registrar its documents. */
export function needsDocuments(d: DomainRow): boolean {
	return (
		d.document_required && d.verification_status !== 'active' && OPEN_STATUSES.includes(d.status)
	);
}

/** The domain still owes the registrant's email verification (.id, .my.id, ...). */
export function needsEmailVerification(d: DomainRow): boolean {
	return (
		!d.document_required &&
		requiresEmailVerification(d.name) &&
		(d.verification_status === 'waiting' || d.verification_status === 'verifying') &&
		(d.status === 'active' || d.status === 'suspended')
	);
}

/** GET /api/v1/domains/addons response row (domain.DomainAddon JSON tags). */
export interface DomainAddonOption {
	key: string;
	name: string;
	price: number;
}

/** Mirrors `ports.DNSRecord` JSON tags. */
export interface DnsRecord {
	type: string;
	host: string;
	value: string;
	ttl: number;
	prio: number;
}

/** Mirrors `ports.RegistrantContact` JSON tags. */
export interface RegistrantContact {
	first_name: string;
	last_name: string;
	company: string;
	email: string;
	phone: string;
	address1: string;
	city: string;
	state: string;
	postcode: string;
	country: string;
}

export const CONTACT_FIELDS = [
	'first_name',
	'last_name',
	'company',
	'email',
	'phone',
	'address1',
	'city',
	'state',
	'postcode',
	'country'
] as const;
