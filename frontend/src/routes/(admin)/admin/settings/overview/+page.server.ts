import { apiFetch } from '$lib/server/api';
import type { PageServerLoad } from './$types';
import type { RawSettingsGroups } from '../+page.server';

/**
 * System Settings landing page (docs/DESIGN.md §7): navigation cards plus a
 * "setup tasks" checklist.
 *
 * Every settings group already exists after the first migration (000002 seeds
 * company/billing/automation/mail/tickets/security with placeholder defaults),
 * so "does the group exist" can never say whether an operator has set the
 * system up. Each task below therefore checks something an operator must
 * actually have done:
 *   - company / mail: the seeded placeholder identity was replaced;
 *   - gateway: Duitku has credentials or the manual bank-transfer gateway is
 *     enabled with an account;
 *   - products / servers: at least one exists.
 * A task whose data the signed-in staff member cannot read (403) or that failed
 * to load is reported as `done: null` (unknown) and left out of the progress
 * ratio rather than counted either way.
 */

// Seed placeholders from backend/migrations/000002_seed_core.up.sql.
const PLACEHOLDER_COMPANY_NAME = 'WHCMS';
const PLACEHOLDER_COMPANY_EMAIL = 'billing@example.com';
const PLACEHOLDER_MAIL_FROM = 'no-reply@example.com';

interface GatewaysConfig {
	duitku?: { merchant_code?: string; api_key_set?: boolean };
	manual?: { enabled?: boolean; accounts?: unknown[] | null };
}

export interface SetupTask {
	key: 'company' | 'mail' | 'gateway' | 'products' | 'servers';
	label: string;
	href: string;
	/** true = done, false = still to do, null = could not be determined. */
	done: boolean | null;
}

export const load: PageServerLoad = async (event) => {
	const [settingsRes, gatewaysRes, productsRes, serversRes] = await Promise.all([
		apiFetch<RawSettingsGroups>(event, '/api/v1/admin/settings'),
		apiFetch<GatewaysConfig>(event, '/api/v1/admin/gateways'),
		apiFetch<unknown[]>(event, '/api/v1/admin/products', { query: { per_page: 1 } }),
		apiFetch<unknown[]>(event, '/api/v1/admin/servers', { query: { per_page: 1 } })
	]);

	const company = settingsRes.data?.company;
	const mail = settingsRes.data?.mail;
	const duitku = gatewaysRes.data?.duitku;
	const manual = gatewaysRes.data?.manual;

	const tasks: SetupTask[] = [
		{
			key: 'company',
			label: 'Set your company name and billing email',
			href: '/admin/settings',
			done: settingsRes.error
				? null
				: !!company?.name &&
					company.name !== PLACEHOLDER_COMPANY_NAME &&
					!!company.email &&
					company.email !== PLACEHOLDER_COMPANY_EMAIL
		},
		{
			key: 'mail',
			label: 'Set the sender name and address for outbound email',
			href: '/admin/settings?tab=mail',
			done: settingsRes.error
				? null
				: !!mail?.from_name && !!mail.from_email && mail.from_email !== PLACEHOLDER_MAIL_FROM
		},
		{
			key: 'gateway',
			label: 'Configure a payment gateway',
			href: '/admin/gateways',
			done: gatewaysRes.error
				? null
				: (!!duitku?.merchant_code && !!duitku.api_key_set) ||
					(!!manual?.enabled && (manual.accounts?.length ?? 0) > 0)
		},
		{
			key: 'products',
			label: 'Create at least one product',
			href: '/admin/products',
			done: productsRes.error ? null : (productsRes.meta?.total ?? 0) > 0
		},
		{
			key: 'servers',
			label: 'Add a provisioning server',
			href: '/admin/servers',
			done: serversRes.error ? null : (serversRes.meta?.total ?? 0) > 0
		}
	];

	const known = tasks.filter((t) => t.done !== null);
	const doneCount = known.filter((t) => t.done).length;

	return {
		tasks,
		doneCount,
		knownCount: known.length,
		setupPercent: known.length > 0 ? Math.round((doneCount / known.length) * 100) : 0,
		// Only surfaced when the settings call itself failed for a reason other than
		// "this staff member lacks the settings permission" (the page is still usable).
		listError: settingsRes.error && settingsRes.status !== 403 ? settingsRes.error.message : null
	};
};
