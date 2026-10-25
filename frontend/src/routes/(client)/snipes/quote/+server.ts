import { apiFetch } from '$lib/server/api';
import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import type { SnipeQuote } from '../shared';

/**
 * BFF proxy for the live price preview on the "start a hunt" form: the browser
 * never talks to the Go API directly, so it posts here and this forwards the
 * session's token server-side.
 */
export const POST: RequestHandler = async (event) => {
	const body = await event.request.json().catch(() => null);
	if (!body || typeof body !== 'object') {
		return json({ error: 'invalid request' }, { status: 400 });
	}
	const res = await apiFetch<SnipeQuote>(event, '/api/v1/snipe/quote', { method: 'POST', body });
	if (res.error || !res.data) {
		return json(
			{ error: res.error?.message ?? 'quote failed', code: res.error?.code },
			{ status: res.status >= 400 ? res.status : 400 }
		);
	}
	return json({ quote: res.data });
};
