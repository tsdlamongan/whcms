/**
 * Per-module staff permissions (CONTRACTS §3 RBAC - JSONB map on the users row).
 *
 * Kept in a plain module (not `+page.server.ts`) because SvelteKit only allows a
 * fixed set of special exports (`load`, `actions`, page options) from `+page.server.ts` -
 * exporting anything else there breaks the build. Both `+page.server.ts` (form parsing)
 * and `+page.svelte` (checkbox list) import from here instead.
 */
export const PERMISSION_MODULES = [
	'clients',
	'orders',
	'billing',
	'payments',
	'services',
	'domains',
	'support',
	'products',
	'servers',
	'reports',
	'settings',
	'logs',
	'announcements',
	'knowledgebase',
	'network'
] as const;

/**
 * These are exactly the modules the API enforces with RequirePermission(...)
 * (backend: adminops.KnownPermissionModules minus the admin-only
 * staff/gateways/registrars, which `RequireRole("admin")` guards instead).
 * Keys the API stores for a user that are NOT listed here must survive an edit
 * - see `mergePermissions`.
 */
export type PermissionModule = (typeof PERMISSION_MODULES)[number];

/**
 * Overlays the checkbox state of the modules this UI shows onto the permission
 * map already stored for the user. PATCH /admin/staff/:id REPLACES the whole
 * map, so sending only the displayed modules would silently drop every other
 * stored key (e.g. the admin-only `staff`/`gateways`/`registrars` flags a
 * seeded account carries).
 */
export function mergePermissions(
	existing: Record<string, boolean>,
	shown: Record<string, boolean>
): Record<string, boolean> {
	return { ...existing, ...shown };
}

/** permissions is json.RawMessage on the wire - normalize object | JSON string | null. */
export function parsePermissions(raw: unknown): Record<string, boolean> {
	let obj: unknown = raw;
	if (typeof raw === 'string' && raw.trim()) {
		try {
			obj = JSON.parse(raw);
		} catch {
			return {};
		}
	}
	if (typeof obj !== 'object' || obj === null || Array.isArray(obj)) return {};
	const out: Record<string, boolean> = {};
	for (const [k, v] of Object.entries(obj)) out[k] = v === true;
	return out;
}
