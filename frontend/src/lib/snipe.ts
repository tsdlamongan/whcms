/**
 * Small helpers shared by the admin and client domain-snipe pages.
 */

/**
 * A `datetime-local` value ("2026-10-24T19:30") is wall-clock time with no
 * zone; the app is Asia/Jakarta everywhere (UTC+7, no DST), so pin it there
 * and send RFC 3339. Returns undefined for an empty or malformed value.
 */
export function jakartaToISO(local: string): string | undefined {
	const v = local.trim();
	if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(v)) return undefined;
	return `${v}:00+07:00`;
}
