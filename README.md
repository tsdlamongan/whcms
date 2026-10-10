# WHCMS

[![CI](https://github.com/tsdlamongan/whcms/actions/workflows/ci.yml/badge.svg)](https://github.com/tsdlamongan/whcms/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](backend/go.mod)
[![SvelteKit 2](https://img.shields.io/badge/SvelteKit-2-FF3E00?logo=svelte)](frontend/package.json)

**WHCMS** (Web Hosting Central Management System) is an **open-source,
WHMCS-style billing & automation platform** for hosting providers:
client management, product catalog, orders, invoicing (IDR), Duitku payments,
automatic cPanel/DirectAdmin provisioning, RDash domain management, support
tickets, and familiar admin + client areas.

> Architecture contract: [`docs/CONTRACTS.md`](docs/CONTRACTS.md) · Product spec: [`docs/PRD.md`](docs/PRD.md) · Contributing: [`CONTRIBUTING.md`](CONTRIBUTING.md)

| Public storefront (Twenty-One theme) | Admin area (HostPanel theme) |
|---|---|
| ![Public storefront](docs/screenshots/storefront.png) | ![Admin dashboard](docs/screenshots/admin-dashboard.png) |

## Features

- **Clients** — registration with email verification, profiles, sub-account contacts with per-area permissions, credit balance + ledger, admin impersonation.
- **Catalog** — product groups, per-cycle pricing with setup fees, configurable options with price deltas, panel resource specs, coupons, product duplication.
- **Orders** — storefront cart for hosting + domains, coupon validation, fraud limits, `on_payment` / `on_order` / `manual` activation.
- **Billing** — invoices with PDF and proforma mode, taxes, late fees, due reminders, prorated upgrades/downgrades, renewals, credit payments; IDR-only `int64` money.
- **Payments** — modular gateway registry: Duitku (inline VA/QRIS instructions, signature-verified webhook confirmed via Check Transaction, idempotent activation, resumable pending payments) and manual bank transfer with admin confirmation.
- **Provisioning** — automatic cPanel/WHM and DirectAdmin accounts: create/suspend/unsuspend/terminate, package upgrades, panel SSO, username-collision handling, reseller package prefixes.
- **Domains** — search, register, transfer, and renew through the RDash registrar module; nameservers, EPP codes, registrant contacts, premium TLD pricing tiers.
- **Support** — departments, tickets with S3-stored attachments, internal notes, email notifications, per-client email delivery history.
- **Portal** — public knowledgebase with search, announcements, network status, and contact pages.
- **Admin area** — WHMCS-style dashboard and KPI cards, revenue/orders/services reports with CSV export, audit + email + integration logs, stuck-job module queue, staff RBAC per module, live gateway/registrar configuration (AES-256-GCM at rest).
- **Ops** — asynq job queue with cron schedules, health probes, structured JSON logs, first-run `/install` wizard, a `whcms` CLI for one-command bare-metal install/backup/restore/update/uninstall, >90% enforced backend coverage, full Playwright E2E suite.

## Architecture

```mermaid
flowchart LR
    B[Browser] -->|SSR / form actions| FE["SvelteKit frontend (BFF)\n:3000 / dev :5173"]
    FE -->|Bearer JWT, server-side only| API["Go API (Fiber v3)\n:8080"]
    API --> PG[(PostgreSQL 18)]
    API --> RD[(Redis 7)]
    API --> S3[(RustFS / S3)]
    W["worker (asynq jobs + cron)"] --> PG
    W --> RD
    API -.->|integrations| EXT["Duitku · WHM/cPanel · DirectAdmin · RDash · SMTP"]
    W -.-> EXT
    MOCK["mockserver :9090 (dev/E2E)"] -. mocks .-> EXT
```

## Stack

| Layer | Technology |
|---|---|
| API | Go ≥1.26, Fiber v3, pgx/v5 (raw SQL, no ORM) |
| Jobs / cron | asynq (Redis-backed queue + periodic scheduler), separate `worker` binary |
| Database | PostgreSQL 18, golang-migrate (auto-run on API startup) |
| Cache / queue / tokens | Redis 7 (key prefix `whmcs:`) |
| Object storage | RustFS (S3-compatible) via minio-go — invoice PDFs, ticket attachments |
| Frontend | SvelteKit 2 + Svelte 5 (runes), TypeScript, Tailwind CSS v4, adapter-node SSR |
| Auth | JWT (HS256) access 15m + rotating opaque refresh tokens in Redis, Argon2id, TOTP 2FA |
| Payments | Duitku (mock-first; real MD5 signature verification) |
| Provisioning | cPanel/WHM + DirectAdmin modules |
| Domains | RDash registrar module |
| Testing | testify (>90% coverage gate), httptest, Playwright E2E, full mockserver |

Money is **IDR-only**, stored as `int64` whole rupiah.

## Repository layout

```
/backend        Go module "github.com/tsdlamongan/whcms/backend"
  /cmd/api        HTTP API server (runs migrations on startup)
  /cmd/worker     asynq worker + cron scheduler
  /cmd/seed       dev/E2E seeder
  /internal/...   domain, ports, services, repositories, integrations, transport, platform
  /migrations     golang-migrate SQL files
/frontend       SvelteKit 2 app (client area + admin area, BFF pattern)
/mockserver     Go module "github.com/tsdlamongan/whcms/mockserver" — mock Duitku/WHM/DirectAdmin/RDash + mail capture (:9090)
/deploy         Dockerfiles, docker-compose.yml (dev), docker-compose.prod.yml, env-examples/
/docs           CONTRACTS.md (binding architecture contract), STACK.md, MODULES.md,
                WIRING.md, FRONTEND.md, E2E.md, RECONCILE.md, PRD.md (product spec),
                DESIGN.md + DESIGN-FRONT.md (WHMCS theme clone specs)
```

## Quickstart — local development

Prerequisites: Go ≥1.26, Node 22, and locally running PostgreSQL 18, Redis
(`:6379`), and an S3-compatible store on `:9000` (key/secret `rustfsadmin`,
bucket `whmcs`). If you don't run these natively yet:

```bash
# PostgreSQL role/database the tooling expects (default installs have no "root" role):
psql -d postgres -c "CREATE ROLE root LOGIN SUPERUSER PASSWORD 'postgres'"

# S3 store — RustFS (or swap in MinIO, same API):
docker run -d --name rustfs -p 9000:9000 \
  -e RUSTFS_ACCESS_KEY=rustfsadmin -e RUSTFS_SECRET_KEY=rustfsadmin \
  -v rustfsdata:/data rustfs/rustfs:latest
```

The `whmcs` Postgres database is your own **local** database
(`postgres://root:postgres@localhost:5432/whmcs`) — separate from the
disposable `whmcs_e2e` database `make up`/`make test-backend` use for
automated test runs, so running tests never resets or clobbers your local
data (see `docs/E2E.md` §3).

**New install?** Skip straight to step 2 without creating `backend/.env` —
`cmd/api` serves an installation wizard at `http://localhost:8080/install`
(proxied by the frontend at `/install`) whenever required config is missing.
It walks you through the database/Redis/RustFS/mail connection, writes
`backend/.env` for you, and creates your first admin account.

```bash
# 1. Backend env (optional — skip this and use the /install wizard instead)
cp deploy/env-examples/api.env.example backend/.env
#    → set APP_ENCRYPTION_KEY (openssl rand -base64 32) and JWT_SECRET.
#    → for mock integrations: DUITKU_BASE_URL=http://localhost:9090
#      DUITKU_MERCHANT_CODE=DEMO DUITKU_API_KEY=secretkey
#      RDASH_BASE_URL=http://localhost:9090/v1
#      MAIL_DRIVER=http MAIL_HTTP_URL=http://localhost:9090/mail/send

# 2. Mockserver (Duitku/WHM/DA/RDash mocks + mail capture on :9090)
cd mockserver && go run .

# 3. API (auto-migrates the DB) and worker — separate terminals, from backend/
cd backend && go run ./cmd/api
cd backend && go run ./cmd/worker

# 4. Frontend (http://localhost:5173)
cd frontend
cp ../deploy/env-examples/frontend.env.example .env
npm install && npm run dev

# 5. Seed dev/demo data (admin+staff users, a cPanel/DirectAdmin server pair
#    pointed at the mockserver, a shared_hosting product) — idempotent, safe
#    to re-run. Only needed if you skipped the /install wizard's admin-account
#    step, or want the extra E2E fixtures on your local DB too:
cd backend && go run ./cmd/seed
```

Health probes: `GET :8080/healthz` (liveness), `GET :8080/readyz` (PG+Redis+S3).

**`make up` / `make down`** (root `Makefile`, wrapping `scripts/e2e-up.sh` /
`scripts/e2e-down.sh`) automate steps 2–5 (mockserver + api + worker + frontend,
with the seeder run in between) for local E2E runs. Run `make help` to list every
target. The seeded admin login is **`admin@e2e.test` / `AdminE2E!2026`**
(dev/E2E only — never reachable in a production deployment you seed yourself).

## Quickstart — Docker Compose

Runs the entire stack in containers (own Postgres/Redis/RustFS — independent
of any services already on your host). From the repo root:

```bash
# Core stack: postgres, redis, rustfs, api, worker, frontend
docker compose -f deploy/docker-compose.yml up -d --build

# Dev stack incl. mockserver (api/worker default to mock integrations)
docker compose -f deploy/docker-compose.yml --profile dev up -d --build
```

- Frontend: http://localhost:3000 · API: http://localhost:8080 · Mockserver: http://localhost:9090
- RustFS publishes on host port 9000; override with `RUSTFS_HOST_PORT` if a
  local instance already uses it.
- Dev-only secret defaults are baked in; override via environment or an
  `.env` file next to the compose file.

## Production install — one-command CLI installer

For a bare Linux VPS (Ubuntu, Debian, CentOS, RHEL, Rocky, Alma, or Fedora),
skip Docker Compose entirely and let the `whcms` CLI provision
Postgres/Redis/RustFS, fetch (or build) binaries, generate secrets, run
migrations, and register systemd services in one shot:

```bash
curl -fsSL https://raw.githubusercontent.com/tsdlamongan/whcms/main/scripts/install.sh \
  | sudo bash -s -- --domain billing.example.com
```

That runs [`scripts/install.sh`](scripts/install.sh), which detects your
OS/arch, installs Docker (+ Go if `WHCMS_VERSION=source`), downloads a release
archive (or builds from source), writes `~/.whcms/config/whcms.env` with
freshly generated secrets, brings up Postgres/Redis/RustFS via a generated
Compose file, runs migrations, installs `whcms-api`/`whcms-worker` as systemd
services, and drops the `whcms` CLI at `/usr/local/bin/whcms`. Override
`WHCMS_VERSION` (`latest` / a release tag / `source`) or `WHCMS_HOME`
(default `~/.whcms`) as environment variables before the pipe.

Once installed, manage the deployment with the `whcms` CLI
(`backend/cmd/cli`, implementation in `backend/internal/cli`):

| Command | What it does |
|---|---|
| `whcms status [--json]` | Show status of the API/worker services |
| `whcms logs [--api\|--worker]` | Tail service logs |
| `whcms backup [--output <path>] [--no-storage]` | Dump the database (and RustFS storage) to a `.tar.gz` |
| `whcms restore --input <path>` | Restore from a backup archive |
| `whcms update [--version <tag>] [--check]` | Update binaries from a GitHub release, with automatic rollback on failure |
| `whcms reset-admin --email <email> --password <password>` | Reset an admin/staff password directly in the database |
| `whcms uninstall` | Stop services and remove the installation |

Run `whcms help` at any time for the full flag reference.

## Environment configuration

Full annotated lists live in [`deploy/env-examples/`](deploy/env-examples/). Key variables:

| Variable | Component | Description |
|---|---|---|
| `APP_ENV` / `APP_PORT` | api, worker | Environment name / API listen port (8080) |
| `APP_BASE_URL` | api | Public API URL (Duitku callback URLs are built from it) |
| `FRONTEND_URL` | api | Public frontend URL (email links, payment return URL) |
| `JWT_SECRET` | api | HS256 access-token signing key |
| `APP_ENCRYPTION_KEY` | api, worker | base64 32-byte AES-256-GCM key for at-rest secrets |
| `DATABASE_URL` | api, worker | PostgreSQL DSN |
| `REDIS_ADDR` / `REDIS_PASSWORD` / `REDIS_DB` | api, worker | Redis connection |
| `RUSTFS_ENDPOINT` / `RUSTFS_ACCESS_KEY` / `RUSTFS_SECRET_KEY` / `RUSTFS_BUCKET` / `RUSTFS_USE_SSL` | api, worker | S3 object storage |
| `DUITKU_MERCHANT_CODE` / `DUITKU_API_KEY` / `DUITKU_ENV` / `DUITKU_BASE_URL` | api, worker | Payment gateway (`DUITKU_BASE_URL` empty → derived from env; point to mockserver in dev) |
| `RDASH_RESELLER_ID` / `RDASH_API_KEY` / `RDASH_BASE_URL` | api, worker | Domain registrar |
| `MAIL_DRIVER` (`log`\|`smtp`\|`http`) + `SMTP_*` / `MAIL_HTTP_URL` | api, worker | Outbound mail |
| `WORKER_CONCURRENCY` | worker | asynq concurrency (default 10) |
| `ADMIN_ALERT_EMAIL` | worker | Recipient for permanent job-failure alerts |
| `API_URL` | frontend | Server-side Go API base URL (BFF) |
| `PUBLIC_APP_NAME` | frontend | Browser-visible app name |
| `PORT` / `ORIGIN` or `PROTOCOL_HEADER`+`HOST_HEADER` | frontend | adapter-node listen port / origin handling behind a proxy |

Most module secrets (SMTP, CAPTCHA, …) come from env **only**. The two
exceptions are the Duitku gateway and RDash registrar API keys: they may also
be set live from the admin settings UI, stored **AES-256-GCM-encrypted** in
the database (never echoed back by any endpoint), with the env vars as
fallback for whichever field is left blank — see `docs/CONTRACTS.md` §10–§11.

## Testing

Everything runs through the root `Makefile` (`make help` lists all targets):

| Command | What it does |
|---|---|
| `make up` | Start the full local stack (mockserver + api + seed + worker + frontend). Wraps `scripts/e2e-up.sh`. |
| `make down` | Stop the local stack. Wraps `scripts/e2e-down.sh`. |
| `make test-backend` | Backend build + vet + tests **with coverage, enforcing the >90% gate**. Needs a running Postgres (auto-runs migrations). |
| `make test-frontend` | Playwright E2E against the running stack (run `make up` first). Filter with `SPEC=<file>`. |
| `make test` | `test-backend` then `test-frontend`. |

### Backend coverage obligation — >90% (mandatory)

Per the quality gate in [`docs/CONTRACTS.md`](docs/CONTRACTS.md) §14, **total statement
coverage over `internal/...` + `pkg/...` must stay above 90%.** `make test-backend`
measures it and **fails the build if it drops below the threshold** (override with
`make test-backend COVERAGE_MIN=95`). Every new exported function needs tests; open
`backend/coverage.html` to find uncovered lines.

Because the repository layer is exercised by `//go:build integration` tests against a
real PostgreSQL, the gate runs the **full** suite (unit + integration) — which is why
`make test-backend` needs a running, migrated database (it runs `migrate-up` for you).
Plain unit-only coverage (`cd backend && make cover`) does **not** reach 90% on its own.

Underlying commands, if you need them directly:

```bash
cd backend && make cover        # unit-only coverage (fast, no DB)
cd backend && make cover-gate   # full >90% gate coverage (needs migrated PG)
cd frontend && npm run check    # svelte-check (type/a11y)
cd frontend && npm run build    # production build
cd frontend && npx playwright test   # E2E (needs the stack up)
```

## Deploying to Coolify / Dokploy

Both platforms import the compose file directly (PRD §14.3):

1. **Build & push images** (CI): build `deploy/Dockerfile.backend` and
   `deploy/Dockerfile.frontend` with the repo root as context, push to your
   registry as `<registry>/backend:<tag>` and `<registry>/frontend:<tag>`.
2. **Create the app** in Coolify/Dokploy from this repo using
   `deploy/docker-compose.prod.yml` (Docker Compose build pack).
3. **Set secrets** in the platform UI (referenced by name, never committed):
   `REGISTRY`, `TAG`, `APP_BASE_URL`, `FRONTEND_URL`, `JWT_SECRET`,
   `APP_ENCRYPTION_KEY`, `RUSTFS_ACCESS_KEY`, `RUSTFS_SECRET_KEY`,
   `DUITKU_MERCHANT_CODE`, `DUITKU_API_KEY`, `ADMIN_ALERT_EMAIL`,
   `POSTGRES_PASSWORD`, SMTP + RDash credentials.
   Using managed Postgres/Redis/S3 instead of the in-stack services? Remove
   those service blocks and set `DATABASE_URL` / `REDIS_ADDR` /
   `RUSTFS_ENDPOINT` accordingly (comments in the compose file).
4. **Domains & TLS**: attach your domain to `frontend` (:3000) and, if the
   Duitku webhook needs a direct hostname, to `api` (:8080). The platform's
   Traefik terminates TLS and injects labels; the apps honor `X-Forwarded-*`.
5. **Deploy**. Migrations run idempotently on API startup; `/healthz` and
   `/readyz` drive platform health checks; rolling deploys are safe because
   the API is stateless.
6. **Redeploys**: trigger the platform deploy hook/webhook from CI after
   pushing a new image tag.

Notes: whitelist the server's egress IP with RDash before go-live, and set the
production callback URL in the Duitku dashboard to
`{APP_BASE_URL}/api/v1/webhooks/duitku`.

## Contributing

Contributions are welcome — start with [`CONTRIBUTING.md`](CONTRIBUTING.md)
(dev setup, the >90% backend coverage gate, the E2E-spec-per-feature rule, and
the Conventional Commits format). Please report security issues privately via
[`SECURITY.md`](SECURITY.md), never in public issues.

## License

WHCMS is released under the [MIT License](LICENSE).

WHMCS® is a trademark of WHMCS Ltd. This project is an independent,
from-scratch implementation and is not affiliated with, endorsed by, or
derived from WHMCS Ltd's products.
