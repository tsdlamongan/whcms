import { execFile, execFileSync, spawn, type ChildProcess } from 'node:child_process';
import os from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import { expect, test } from './fixtures';

/**
 * Installation wizard (docs/CONTRACTS.md §15) - app-level phase. A normal spec
 * can't cover it: the shared `make up` stack is always already "installed"
 * once cmd/seed runs, so the wizard needs a FRESH, not-yet-installed instance.
 *
 * This drives that instance at the HTTP level (NO browser, NO second frontend),
 * standing up only a throwaway Postgres DB + a second `cmd/api` process
 * (migrated, NOT seeded -> no admin -> not installed) and asserting the whole
 * install lifecycle end-to-end against the real, fully-wired binary:
 * fresh -> create first admin -> installed -> second-admin CONFLICT guard ->
 * optional settings -> the created admin can actually log in.
 *
 * Two hard-won environment notes (see e2e-stack-gotchas memory #8):
 *  - HTTP is done with `curl` via subprocess, NOT Node's global `fetch`.
 *    Calling `fetch` (undici) from inside a Playwright worker on this machine
 *    deterministically SIGKILLs the worker (bisected: sleeping/spawning/psql/
 *    go-build are all fine, only `fetch` triggers it - no OS jetsam log). curl
 *    is a plain subprocess and works, same as the psql calls here.
 *  - It runs LAST and alone (playwright.config.ts `isolated-install` project,
 *    `dependencies: ['shared-stack']`, and `workers: 1`).
 */

const REPO_ROOT = path.resolve(import.meta.dirname, '../../..');
const BACKEND_DIR = path.join(REPO_ROOT, 'backend');
const apiBinPath = path.join(os.tmpdir(), `whcms-install-spec-api-${process.pid}`);

const API_PORT = 8093;
const API_BASE = `http://localhost:${API_PORT}`;

const MAINT_DATABASE_URL = 'postgres://root:postgres@localhost:5432/postgres?sslmode=disable';
const dbName = `whmcs_e2e_install_${Date.now()}_${Math.floor(Math.random() * 100000)}`;
const DATABASE_URL = `postgres://root:postgres@localhost:5432/${dbName}?sslmode=disable`;

const adminEmail = `install-admin-${Date.now()}@example.test`;
const adminPassword = 'InstallWizard!2026';

const run = promisify(execFile);

let apiProc: ChildProcess | undefined;

interface Envelope<T> {
	data: T | null;
	error: { code: string; message: string } | null;
}

const STATUS_MARKER = '\n__HTTP_STATUS__:';

/**
 * The auth endpoints rate-limit per client IP (30 req/min, a Redis counter
 * shared with the shared-stack API, which has just served the whole suite from
 * 127.0.0.1). This spec runs last and would inherit a saturated window, so it
 * presents its own address (the API trusts X-Forwarded-For from loopback);
 * TEST-NET-2, never routable.
 */
const CLIENT_IP = '198.51.100.77';

/**
 * One HTTP call via curl (see file header for why not `fetch`). Returns the
 * parsed envelope + HTTP status. curl runs without -f, so 4xx/5xx still
 * resolve (we assert on the CONFLICT 409 below).
 */
async function api<T>(
	method: 'GET' | 'POST',
	pathname: string,
	body?: unknown
): Promise<{ status: number; env: Envelope<T> }> {
	const args = [
		'-sS',
		'-X',
		method,
		`${API_BASE}${pathname}`,
		'-H',
		`x-forwarded-for: ${CLIENT_IP}`,
		'-w',
		`${STATUS_MARKER}%{http_code}`
	];
	if (body !== undefined) {
		args.push('-H', 'content-type: application/json', '-d', JSON.stringify(body));
	}
	const { stdout } = await run('curl', args);
	const idx = stdout.lastIndexOf(STATUS_MARKER);
	const rawBody = stdout.slice(0, idx);
	const status = Number(stdout.slice(idx + STATUS_MARKER.length).trim());
	return { status, env: JSON.parse(rawBody) as Envelope<T> };
}

async function waitForHealth(pathname: string, tries = 90): Promise<void> {
	for (let i = 0; i < tries; i++) {
		try {
			await run('curl', ['-fsS', '-o', '/dev/null', `${API_BASE}${pathname}`]);
			return;
		} catch {
			// not up yet
		}
		await new Promise((r) => setTimeout(r, 1000));
	}
	throw new Error(`${API_BASE}${pathname} did not become healthy within ${tries}s`);
}

function killByPort(port: number) {
	try {
		const pids = execFileSync('lsof', ['-ti', `:${port}`], { encoding: 'utf-8' }).trim();
		for (const pid of pids.split('\n').filter(Boolean)) {
			try {
				process.kill(Number(pid), 'SIGKILL');
			} catch {
				// already dead
			}
		}
	} catch {
		// nothing listening
	}
}

test.beforeAll(async () => {
	test.setTimeout(180_000);

	await run('psql', [MAINT_DATABASE_URL, '-v', 'ON_ERROR_STOP=1', '-c', `CREATE DATABASE ${dbName};`]);

	// Build once and exec the binary directly rather than `go run` (which would
	// keep the Go toolchain resident as an extra parent process). Awaited (not
	// execFileSync) so the compile never blocks the worker's event loop.
	await run('go', ['build', '-o', apiBinPath, './cmd/api'], { cwd: BACKEND_DIR });

	// detached + stdio:'ignore' fully decouples the backend from the Playwright
	// worker (own process group; no output piped into the worker's stdout).
	apiProc = spawn(apiBinPath, [], {
		detached: true,
		stdio: 'ignore',
		env: {
			...process.env,
			APP_ENV: 'development',
			APP_PORT: String(API_PORT),
			APP_BASE_URL: API_BASE,
			FRONTEND_URL: API_BASE,
			// Well-known dev-only values (deny-listed for production in
			// config.wellKnownDevSecrets) that satisfy config.LoadFrom's
			// required-secret checks, so config.Load() succeeds and the
			// pre-boot bootstrap phase never triggers here.
			JWT_SECRET: 'wXRw37nkVMhtvXKYd0msDNfPxAUJTEWb4a/4NtUssF8=',
			APP_ENCRYPTION_KEY: '2lhQfX4SZlR+sa1rt6gStNc8wIg1nDu27sngf0KcgW8=',
			DATABASE_URL,
			// Shares the local Redis/RustFS with the make-up stack (harmless - the
			// throwaway DB is what makes this a distinct, uninstalled instance).
			REDIS_ADDR: 'localhost:6379',
			RUSTFS_ENDPOINT: 'http://localhost:9000',
			RUSTFS_ACCESS_KEY: 'rustfsadmin',
			RUSTFS_SECRET_KEY: 'rustfsadmin',
			RUSTFS_BUCKET: 'whmcs',
			RUSTFS_USE_SSL: 'false',
			MAIL_DRIVER: 'log',
			WORKER_CONCURRENCY: '1',
			ADMIN_ALERT_EMAIL: 'admin-alerts@example.test'
		}
	});
	apiProc.unref();
	await waitForHealth('/healthz');
	await waitForHealth('/readyz');
});

test.afterAll(async () => {
	apiProc?.kill('SIGKILL');
	killByPort(API_PORT);
	try {
		await run('rm', ['-f', apiBinPath]);
	} catch {
		// best-effort cleanup
	}
	try {
		await run('psql', [
			MAINT_DATABASE_URL,
			'-v',
			'ON_ERROR_STOP=1',
			'-c',
			`DROP DATABASE IF EXISTS ${dbName} WITH (FORCE);`
		]);
	} catch {
		// best-effort cleanup
	}
});

test('installation wizard API: fresh instance installs a first admin, guards against a second, and the admin can log in', async () => {
	await test.step('a fresh, migrated-but-unseeded instance reports config-ready but not installed', async () => {
		const { status, env } = await api<{ config_ready: boolean; installed: boolean }>(
			'GET',
			'/api/v1/install/status'
		);
		expect(status).toBe(200);
		expect(env.data?.config_ready).toBe(true);
		expect(env.data?.installed).toBe(false);
	});

	await test.step('creating the first admin succeeds', async () => {
		const { status, env } = await api<{ id: number; email: string }>('POST', '/api/v1/install/admin', {
			email: adminEmail,
			password: adminPassword
		});
		expect(status, JSON.stringify(env)).toBe(201);
		expect(env.data?.email).toBe(adminEmail);
		expect(env.data?.id).toBeGreaterThan(0);
	});

	await test.step('status now reports installed', async () => {
		const { env } = await api<{ installed: boolean }>('GET', '/api/v1/install/status');
		expect(env.data?.installed).toBe(true);
	});

	await test.step('a second admin-creation attempt is rejected with CONFLICT (the install guard)', async () => {
		const { status, env } = await api('POST', '/api/v1/install/admin', {
			email: `second-${Date.now()}@example.test`,
			password: 'AnotherPass!2026'
		});
		expect(status).toBe(409);
		expect(env.error?.code).toBe('CONFLICT');
	});

	await test.step('optional site settings save succeeds', async () => {
		const { status } = await api('POST', '/api/v1/install/settings', {
			company_name: 'Acme Hosting E2E'
		});
		expect(status).toBe(200);
	});

	await test.step('the freshly created admin can actually log in with the admin role', async () => {
		const { status, env } = await api<{ access_token: string; user: { role: string } }>(
			'POST',
			'/api/v1/auth/login',
			{ email: adminEmail, password: adminPassword }
		);
		expect(status, JSON.stringify(env)).toBe(200);
		expect(env.data?.access_token).toBeTruthy();
		expect(env.data?.user.role).toBe('admin');
	});
});
