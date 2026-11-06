import {
	expect,
	request,
	type APIRequestContext,
	type APIResponse,
	type BrowserContext,
	type Page
} from '@playwright/test';

/**
 * Shared E2E helpers. Every spec runs against the already-live stack
 * (frontend :5173, backend api :8080, mockserver :9090). Test data is unique
 * per run so specs are safe under concurrency against the shared backend + DB.
 */

export const APP_BASE = 'http://localhost:5173';
export const API_BASE = 'http://localhost:8080';
export const MOCK_BASE = 'http://localhost:9090';

export const ADMIN_EMAIL = 'admin@e2e.test';
export const ADMIN_PASSWORD = 'AdminE2E!2026';
export const STAFF_EMAIL = 'staff@e2e.test';
export const STAFF_PASSWORD = 'StaffE2E!2026';

/**
 * A dummy CAPTCHA token sent on every API auth/order call. CAPTCHA is disabled
 * by default so this is ignored; if an operator enables it in dev, the dev
 * stack's "always passes" Turnstile test secret + mockserver siteverify accept
 * any non-empty token, so the suite stays green either way.
 */
export const CAPTCHA_TOKEN = 'e2e-dummy-captcha-token';

export function sleep(ms: number): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, ms));
}

let seq = 0;
/** A process-unique suffix (safe under concurrent specs sharing the backend). */
export function unique(prefix = ''): string {
	seq += 1;
	const s = `${Date.now().toString(36)}${Math.floor(Math.random() * 1e6).toString(36)}${seq}`;
	return prefix ? `${prefix}-${s}` : s;
}

/** A domain label whose first 8 alnum chars are unique (cPanel username derive). */
export function uniqueDomainLabel(): string {
	return `e${Math.random().toString(36).slice(2, 9)}`;
}

/** Tuning knobs for withAuthRateLimitRetry. Defaults match the historical
 *  behavior (25 attempts, 3000ms backoff, APIResponse envelope detection),
 *  so plain calls are unchanged; the options exist for specs whose local
 *  retry copies differed (own result shape, pacing, or exhaustion policy). */
export interface RateLimitRetryOptions<T> {
	/** Max attempts before giving up (default 25). */
	attempts?: number;
	/** Backoff between attempts in ms (default 3000). */
	delayMs?: number;
	/** Detects a transient RATE_LIMITED result. Defaults to the APIResponse
	 *  check: non-ok status whose JSON envelope has error.code RATE_LIMITED.
	 *  Provide this when fn returns something other than an APIResponse. */
	isRateLimited?: (res: T) => boolean | Promise<boolean>;
	/** When every attempt was rate-limited: make one final fresh call and
	 *  return its result (true) instead of returning the last rate-limited
	 *  result (false, the default). */
	finalFreshAttempt?: boolean;
}

/**
 * /auth/register|login|verify-email share one IP-keyed fixed-window rate limit
 * (30 req/min). Retry on RATE_LIMITED with backoff spanning more than one
 * window before failing (cross-test interference, not a bug).
 */
export async function withAuthRateLimitRetry<T = APIResponse>(
	fn: () => Promise<T>,
	opts: RateLimitRetryOptions<T> = {}
): Promise<T> {
	const attempts = opts.attempts ?? 25;
	const delayMs = opts.delayMs ?? 3000;
	const isRateLimited =
		opts.isRateLimited ??
		(async (res: T): Promise<boolean> => {
			const r = res as unknown as APIResponse;
			if (r.ok()) return false;
			let code: string | undefined;
			try {
				code = (await r.json()).error?.code;
			} catch {
				/* non-JSON body: a real failure, not a rate limit */
			}
			return code === 'RATE_LIMITED';
		});
	let lastRes: T | undefined;
	for (let attempt = 1; attempt <= attempts; attempt++) {
		const res = await fn();
		if (!(await isRateLimited(res))) return res;
		lastRes = res;
		await sleep(delayMs);
	}
	return opts.finalFreshAttempt ? fn() : (lastRes as T);
}

export function authHeaders(token: string): Record<string, string> {
	return { authorization: `Bearer ${token}` };
}

/** Poll the mock mail sink for the newest message to `to` and return html+text. */
export async function waitForMail(api: APIRequestContext, to: string): Promise<string> {
	let body = '';
	await expect
		.poll(
			async () => {
				const res = await api.get(`${MOCK_BASE}/mail/messages`, { params: { to } });
				if (!res.ok()) return '';
				const msgs = (await res.json()) as Array<{ html?: string; text?: string }>;
				if (msgs.length === 0) return '';
				body = `${msgs[0].html ?? ''} ${msgs[0].text ?? ''}`;
				return body;
			},
			{ timeout: 20_000, message: `waiting for mail to ${to}` }
		)
		.not.toBe('');
	return body;
}

/**
 * Poll the mail sink until a message to `to` matches `pattern` and return its
 * first capture group (decoded). Matching a specific link (e.g. the reset link,
 * not any `token=`) avoids grabbing the wrong email when several are queued.
 */
export async function waitForMailMatch(
	api: APIRequestContext,
	to: string,
	pattern: RegExp
): Promise<string> {
	let captured = '';
	await expect
		.poll(
			async () => {
				const res = await api.get(`${MOCK_BASE}/mail/messages`, { params: { to } });
				if (!res.ok()) return '';
				const msgs = (await res.json()) as Array<{ html?: string; text?: string }>;
				for (const m of msgs) {
					const body = `${m.html ?? ''} ${m.text ?? ''}`;
					const match = body.match(pattern);
					if (match) {
						captured = decodeURIComponent(match[1]);
						return captured;
					}
				}
				return '';
			},
			{ timeout: 20_000, message: `waiting for mail matching ${pattern} to ${to}` }
		)
		.not.toBe('');
	return captured;
}

export const VERIFY_LINK = /verify-email\?token=([^"'&\s<]+)/;
export const RESET_LINK = /reset-password\?token=([^"'&\s<]+)/;

/** Log in via the API and return the access token. */
export async function loginApi(
	api: APIRequestContext,
	email: string,
	password: string
): Promise<{ accessToken: string; refreshToken: string }> {
	const res = await withAuthRateLimitRetry(() =>
		api.post(`${API_BASE}/api/v1/auth/login`, {
			data: { email, password, captcha_token: CAPTCHA_TOKEN }
		})
	);
	expect(res.ok(), `login ${email}: ${await res.text()}`).toBeTruthy();
	const d = (await res.json()).data as { access_token: string; refresh_token: string };
	return { accessToken: d.access_token, refreshToken: d.refresh_token };
}

export async function adminToken(api: APIRequestContext): Promise<string> {
	return (await loginApi(api, ADMIN_EMAIL, ADMIN_PASSWORD)).accessToken;
}

export interface Client {
	email: string;
	password: string;
	accessToken: string;
	refreshToken: string;
}

/** Register a fresh client via API, verify via the mail sink, and log in. */
export async function registerVerifyLogin(
	api: APIRequestContext,
	prefix = 'client'
): Promise<Client> {
	const email = `${unique(prefix)}@e2e.test`;
	const password = 'Cl1entPass!2026';
	const reg = await withAuthRateLimitRetry(() =>
		api.post(`${API_BASE}/api/v1/auth/register`, {
			data: {
				email,
				password,
				first_name: 'E2E',
				last_name: 'Client',
				address1: 'Jl. Testing No. 1',
				city: 'Jakarta',
				state: 'DKI Jakarta',
				postcode: '12110',
				country: 'ID',
				phone: '081200000000',
				captcha_token: CAPTCHA_TOKEN
			}
		})
	);
	expect(reg.ok(), `register: ${await reg.text()}`).toBeTruthy();

	const token = await waitForMailMatch(api, email, VERIFY_LINK);
	const verify = await withAuthRateLimitRetry(() =>
		api.post(`${API_BASE}/api/v1/auth/verify-email`, { data: { token } })
	);
	expect(verify.ok(), `verify-email: ${await verify.text()}`).toBeTruthy();

	const { accessToken, refreshToken } = await loginApi(api, email, password);
	return { email, password, accessToken, refreshToken };
}

/** Register a fresh client via API and log in WITHOUT verifying the email -
 *  for exercising email-verification-gated behavior (e.g. checkout). */
export async function registerLogin(
	api: APIRequestContext,
	prefix = 'unverified'
): Promise<Client> {
	const email = `${unique(prefix)}@e2e.test`;
	const password = 'Cl1entPass!2026';
	const reg = await withAuthRateLimitRetry(() =>
		api.post(`${API_BASE}/api/v1/auth/register`, {
			data: {
				email,
				password,
				first_name: 'E2E',
				last_name: 'Client',
				address1: 'Jl. Testing No. 1',
				city: 'Jakarta',
				state: 'DKI Jakarta',
				postcode: '12110',
				country: 'ID',
				phone: '081200000000',
				captcha_token: CAPTCHA_TOKEN
			}
		})
	);
	expect(reg.ok(), `register: ${await reg.text()}`).toBeTruthy();

	const { accessToken, refreshToken } = await loginApi(api, email, password);
	return { email, password, accessToken, refreshToken };
}

/** Authenticate a browser context the way the BFF does (httpOnly cookies). */
export async function setSessionCookies(
	context: BrowserContext,
	accessToken: string,
	refreshToken = '',
	locale = 'en'
): Promise<void> {
	await context.addCookies([
		{ name: 'access_token', value: accessToken, url: APP_BASE, httpOnly: true, sameSite: 'Lax' },
		...(refreshToken
			? [
					{
						name: 'refresh_token',
						value: refreshToken,
						url: APP_BASE,
						httpOnly: true,
						sameSite: 'Lax' as const
					}
				]
			: []),
		{ name: 'locale', value: locale, url: APP_BASE }
	]);
}

/** Click a testid whether it is the button itself or a wrapper around one. */
export async function clickBtn(page: Page, testid: string): Promise<void> {
	const el = page.getByTestId(testid);
	const inner = el.locator('button');
	if (await inner.count()) await inner.first().click();
	else await el.click();
}

/** Find the seeded shared-hosting product id (GET /products returns groups). */
export async function seededHostingProductId(api: APIRequestContext): Promise<number> {
	const res = await api.get(`${API_BASE}/api/v1/products`);
	expect(res.ok()).toBeTruthy();
	const groups = ((await res.json()).data ?? []) as Array<{
		products?: Array<{
			id: number;
			slug: string;
			hidden?: boolean;
			pricing?: Array<{ cycle: string }>;
		}>;
	}>;
	const products = groups.flatMap((g) => g.products ?? []);
	const hosting =
		products.find((p) => p.slug === 'e2e-shared-hosting') ??
		products.find((p) => !p.hidden && p.pricing?.some((pr) => pr.cycle === 'monthly'));
	expect(hosting, 'seeded hosting product should exist').toBeTruthy();
	return hosting!.id;
}

/** Find a client's numeric id via the admin clients search (by email). */
export async function adminFindClientId(
	api: APIRequestContext,
	token: string,
	email: string
): Promise<number> {
	const res = await api.get(`${API_BASE}/api/v1/admin/clients`, {
		headers: authHeaders(token),
		params: { search: email }
	});
	expect(res.ok(), `admin search clients: ${await res.text()}`).toBeTruthy();
	const list = ((await res.json()).data ?? []) as Array<{ id: number; email?: string }>;
	const found = list.find((c) => c.email === email) ?? list[0];
	expect(found, `client with email ${email} should be found via admin search`).toBeTruthy();
	return found!.id;
}

/** Create an order for the given items and return the invoice id. */
export async function createOrder(
	api: APIRequestContext,
	token: string,
	items: unknown[]
): Promise<number> {
	const res = await api.post(`${API_BASE}/api/v1/orders`, {
		headers: authHeaders(token),
		data: { items, captcha_token: CAPTCHA_TOKEN }
	});
	expect(res.ok(), `create order: ${await res.text()}`).toBeTruthy();
	const body = (await res.json()).data as { invoice: { id: number } };
	return body.invoice.id;
}

/** Pay an invoice end-to-end via the mock Duitku gateway; polls until paid. */
export async function payInvoiceViaMock(
	api: APIRequestContext,
	token: string,
	invoiceId: number,
	method = 'BC'
): Promise<void> {
	const payRes = await api.post(`${API_BASE}/api/v1/invoices/${invoiceId}/pay`, {
		headers: authHeaders(token),
		data: { method }
	});
	expect(payRes.ok(), `pay invoice ${invoiceId}: ${await payRes.text()}`).toBeTruthy();
	const payBody = (await payRes.json()).data as { reference?: string; status?: string };
	if (payBody.status !== 'paid') {
		const confirm = await api.post(`${MOCK_BASE}/mock/duitku/pay/${payBody.reference}`);
		expect(confirm.ok(), `mock duitku pay: ${await confirm.text()}`).toBeTruthy();
	}
	await expect
		.poll(
			async () => {
				const res = await api.get(`${API_BASE}/api/v1/invoices/${invoiceId}`, {
					headers: authHeaders(token)
				});
				const body = (await res.json()).data as { invoice?: { status?: string }; status?: string };
				return body.invoice?.status ?? body.status ?? null;
			},
			{ timeout: 30_000, message: `waiting for invoice ${invoiceId} paid` }
		)
		.toBe('paid');
}

/** Poll a client list endpoint until a matching row reaches `status`. */
export async function pollListStatus(
	api: APIRequestContext,
	token: string,
	path: string,
	match: (r: Record<string, unknown>) => boolean,
	status = 'active',
	timeout = 45_000
): Promise<Record<string, unknown>> {
	let found: Record<string, unknown> | undefined;
	await expect
		.poll(
			async () => {
				const res = await api.get(`${API_BASE}${path}`, { headers: authHeaders(token) });
				const rows = ((await res.json()).data ?? []) as Array<Record<string, unknown>>;
				const row = rows.find(match);
				if (row) found = row;
				return (row?.status as string) ?? null;
			},
			{ timeout, message: `waiting for ${path} row → ${status}` }
		)
		.toBe(status);
	return found!;
}

/** Full setup: fresh verified client with one active hosting service. */
export async function clientWithActiveService(
	api: APIRequestContext,
	prefix = 'svc'
): Promise<{ client: Client; serviceId: number; domain: string }> {
	const client = await registerVerifyLogin(api, prefix);
	const productId = await seededHostingProductId(api);
	const domain = `${uniqueDomainLabel()}.hosting.e2e.test`;
	const invoiceId = await createOrder(api, client.accessToken, [
		{ item_type: 'product', product_id: productId, cycle: 'monthly', domain }
	]);
	await payInvoiceViaMock(api, client.accessToken, invoiceId);
	const svc = await pollListStatus(
		api,
		client.accessToken,
		'/api/v1/services',
		(r) => r.domain === domain,
		'active'
	);
	return { client, serviceId: svc.id as number, domain };
}

/** A disposable API request context (call .dispose() in afterAll). */
export async function newApi(): Promise<APIRequestContext> {
	return request.newContext();
}

/**
 * Make sure `tld` (no leading dot, e.g. "my.id") is ON SALE: a domain can only
 * be ordered when its extension has an ACTIVE Domain Pricing row. Creates a
 * 150.000/year row (1-10 years) when none exists and returns its id so the
 * caller can delete it afterwards; returns null when the extension was already
 * priced (leave it alone) - an inactive row is switched on and also returns
 * null. Specs that own a fixed extension (admin-domain-pricing deletes "my.id")
 * must not rely on the seed for it.
 */
export async function ensureSellableTld(
	api: APIRequestContext,
	admin: string,
	tld: string,
	perYear = 150000
): Promise<number | null> {
	const list = await api.get(`${API_BASE}/api/v1/admin/tld-pricing`, {
		headers: authHeaders(admin)
	});
	expect(list.ok(), `list tld pricing: ${await list.text()}`).toBeTruthy();
	const rows = ((await list.json()).data ?? []) as Array<
		{ id: number; tld: string; active: boolean } & Record<string, unknown>
	>;
	const existing = rows.find((r) => r.tld === tld);

	const regs = await api.get(`${API_BASE}/api/v1/admin/registrars`, {
		headers: authHeaders(admin)
	});
	const registrarId = ((await regs.json()).data as Array<{ id: number }>)[0].id;
	const register: Record<string, number> = {};
	for (let y = 1; y <= 10; y++) register[String(y)] = perYear * y;
	const body = {
		tld,
		registrar_id: registrarId,
		active: true,
		min_years: 1,
		max_years: 10,
		register_prices: register,
		renew_prices: { '1': perYear },
		transfer_price: perYear
	};

	if (existing) {
		if (!existing.active) {
			const res = await api.put(`${API_BASE}/api/v1/admin/tld-pricing/${existing.id}`, {
				headers: authHeaders(admin),
				data: { ...existing, ...body }
			});
			expect(res.ok(), `activate tld pricing: ${await res.text()}`).toBeTruthy();
		}
		return null;
	}
	const res = await api.post(`${API_BASE}/api/v1/admin/tld-pricing`, {
		headers: authHeaders(admin),
		data: body
	});
	expect(res.ok(), `create tld pricing: ${await res.text()}`).toBeTruthy();
	return ((await res.json()).data as { id: number }).id;
}
