import { apiUrl } from '$lib/server/api';
import {
	ACCESS_COOKIE,
	clearSessionCookies,
	REFRESH_COOKIE,
	setSessionCookies,
	type SessionTokens
} from '$lib/server/session';
import { redirect, type Handle, type RequestEvent } from '@sveltejs/kit';

/**
 * GET /api/v1/auth/me's real shape (internal/modules/auth.MeResponse): the user
 * DTO and the client profile are each their OWN nested object under `data` -
 * `data.user.role`, NOT `data.role` - with `data.client` null for staff/admin.
 */
interface MeResponse {
	user: {
		id: number;
		email: string;
		role: 'admin' | 'staff' | 'client';
		client_id: number;
		name?: string;
		email_verified?: boolean;
	};
	client: {
		first_name?: string;
		last_name?: string;
	} | null;
}

interface Envelope<T> {
	data: T | null;
	error: { code: string; message: string } | null;
}

function toSessionUser(me: MeResponse): SessionUser {
	const u = me.user;
	const name =
		u.name?.trim() ||
		[me.client?.first_name, me.client?.last_name].filter(Boolean).join(' ').trim() ||
		u.email;
	return {
		id: u.id,
		email: u.email,
		role: u.role,
		client_id: u.client_id ?? 0,
		name,
		// Absent field (older API) degrades to "verified" - the backend still
		// enforces verification at POST /orders; this flag only drives UI hints.
		email_verified: u.email_verified ?? true
	};
}

/** GET /api/v1/auth/me with the given access token. Returns {user} or {unauthorized} flag. */
async function fetchMe(
	event: RequestEvent,
	accessToken: string
): Promise<{ user: SessionUser | null; unauthorized: boolean }> {
	try {
		const res = await event.fetch(`${apiUrl()}/api/v1/auth/me`, {
			headers: {
				authorization: `Bearer ${accessToken}`,
				accept: 'application/json',
				// See $lib/server/api.ts apiFetch: without this the API's
				// per-IP rate limiter/login-lockout sees every user as this
				// one server (internal frontend->api hop).
				'x-forwarded-for': event.getClientAddress()
			}
		});
		if (res.status === 401) return { user: null, unauthorized: true };
		if (!res.ok) return { user: null, unauthorized: false };
		const envelope = (await res.json()) as Envelope<MeResponse>;
		if (!envelope.data) return { user: null, unauthorized: false };
		return { user: toSessionUser(envelope.data), unauthorized: false };
	} catch {
		// API unreachable - render as anonymous rather than crashing SSR.
		return { user: null, unauthorized: false };
	}
}

/** POST /api/v1/auth/refresh - rotates the token pair. Returns null when the refresh is rejected. */
async function refreshSession(
	event: RequestEvent,
	refreshToken: string
): Promise<SessionTokens | null> {
	try {
		const res = await event.fetch(`${apiUrl()}/api/v1/auth/refresh`, {
			method: 'POST',
			headers: {
				'content-type': 'application/json',
				accept: 'application/json',
				'x-forwarded-for': event.getClientAddress()
			},
			body: JSON.stringify({ refresh_token: refreshToken })
		});
		if (!res.ok) return null;
		const envelope = (await res.json()) as Envelope<SessionTokens>;
		if (!envelope.data?.access_token || !envelope.data?.refresh_token) return null;
		return envelope.data;
	} catch {
		return null;
	}
}

// Flips permanently true the first time GET /api/v1/install/status reports
// `installed` - once true it never becomes false again, so every request
// after the first one post-install skips the check entirely. Only a
// not-yet-installed server pays this cost, and only until it is installed.
let installedCache = false;

/** GET /api/v1/install/status - fails OPEN (treated as installed) on any
 *  API/network error, so a transient outage never traps every visitor on
 *  the install wizard (matches fetchMe's own "render anonymous" fallback). */
async function isInstalled(event: RequestEvent): Promise<boolean> {
	if (installedCache) return true;
	try {
		const res = await event.fetch(`${apiUrl()}/api/v1/install/status`, {
			headers: {
				accept: 'application/json',
				'x-forwarded-for': event.getClientAddress()
			}
		});
		if (!res.ok) return true;
		const envelope = (await res.json()) as Envelope<{ installed?: boolean }>;
		if (!envelope.data?.installed) return false;
		installedCache = true;
		return true;
	} catch {
		return true;
	}
}

export const handle: Handle = async ({ event, resolve }) => {
	const { cookies, locals } = event;

	locals.locale = cookies.get('locale') === 'en' ? 'en' : 'id';
	locals.user = null;
	locals.accessToken = null;

	const path = event.url.pathname;
	const skipInstallGate =
		path === '/install' ||
		path.startsWith('/install/') ||
		path.startsWith('/_app/') ||
		path === '/favicon.ico';
	if (!skipInstallGate && !(await isInstalled(event))) {
		redirect(303, '/install');
	}

	locals.accessToken = cookies.get(ACCESS_COOKIE) ?? null;

	const refreshToken = cookies.get(REFRESH_COOKIE) ?? null;

	if (locals.accessToken) {
		const { user, unauthorized } = await fetchMe(event, locals.accessToken);
		locals.user = user;

		// Auto-refresh once on 401, with rotated cookies.
		if (!user && unauthorized && refreshToken) {
			const rotated = await refreshSession(event, refreshToken);
			if (rotated) {
				setSessionCookies(cookies, rotated);
				locals.accessToken = rotated.access_token;
				const retry = await fetchMe(event, rotated.access_token);
				locals.user = retry.user;
			} else {
				clearSessionCookies(cookies);
				locals.accessToken = null;
			}
		}
	} else if (refreshToken) {
		// Access cookie expired (15m) but the refresh token (30d) is still alive.
		const rotated = await refreshSession(event, refreshToken);
		if (rotated) {
			setSessionCookies(cookies, rotated);
			locals.accessToken = rotated.access_token;
			const { user } = await fetchMe(event, rotated.access_token);
			locals.user = user;
		} else {
			clearSessionCookies(cookies);
		}
	}

	const response = await resolve(event, {
		transformPageChunk: ({ html }) => html.replace('%lang%', locals.locale)
	});

	// Baseline security headers for every BFF response. CSP is limited to
	// frame-ancestors (clickjacking) so the CDN fonts/styles and SvelteKit's
	// inline hydration script keep working; tighten further behind a proxy if
	// your deployment allows it.
	response.headers.set('X-Frame-Options', 'DENY');
	response.headers.set('X-Content-Type-Options', 'nosniff');
	response.headers.set('Referrer-Policy', 'strict-origin-when-cross-origin');
	response.headers.set('Permissions-Policy', 'camera=(), microphone=(), geolocation=()');
	response.headers.set('Content-Security-Policy', "frame-ancestors 'none'");
	if (event.url.protocol === 'https:') {
		response.headers.set('Strict-Transport-Security', 'max-age=31536000; includeSubDomains');
	}
	return response;
};
