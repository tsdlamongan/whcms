import { apiUrl } from '$lib/server/api';
import { error, type RequestEvent } from '@sveltejs/kit';

/**
 * Streams a CSV export from the Go API to the browser with the session's
 * bearer token (a plain `<a download>` link cannot attach it).
 *
 * `+server.ts` routes bypass the (admin) layout guard, so the role is
 * re-checked here. `path` includes the `/api/v1` prefix; blank/undefined query
 * values are skipped. The upstream `content-disposition` is forwarded so the
 * backend picks the filename (e.g. `revenue_2026-01-01_2026-01-31.csv`).
 */
export async function proxyAdminCsv(
	event: RequestEvent,
	path: string,
	query: Record<string, string | undefined> = {},
	fallbackFilename = 'export.csv'
): Promise<Response> {
	const user = event.locals.user;
	if (!user || (user.role !== 'admin' && user.role !== 'staff')) {
		error(403, 'forbidden');
	}

	const url = new URL(apiUrl() + path);
	for (const [k, v] of Object.entries(query)) {
		if (v) url.searchParams.set(k, v);
	}

	let upstream: Response;
	try {
		upstream = await event.fetch(url, {
			headers: {
				authorization: `Bearer ${event.locals.accessToken ?? ''}`,
				accept: 'text/csv',
				'x-forwarded-for': event.getClientAddress()
			}
		});
	} catch {
		error(502, 'export unavailable');
	}

	if (!upstream.ok || !upstream.body) {
		error(upstream.status >= 400 ? upstream.status : 502, 'export failed');
	}

	return new Response(upstream.body, {
		status: 200,
		headers: {
			'content-type': upstream.headers.get('content-type') ?? 'text/csv; charset=utf-8',
			'content-disposition':
				upstream.headers.get('content-disposition') ?? `attachment; filename="${fallbackFilename}"`,
			'cache-control': 'private, no-store'
		}
	});
}
