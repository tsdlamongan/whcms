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
- **Ops** — asynq job queue with cron schedules, health probes, structured JSON logs, first-run `/install` wizard, >90% enforced backend coverage, full Playwright E2E suite.

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
disposable `whmcs_e2e` database that `make cover`/`make e2e`/`make test` use
for automated test runs, so running tests never resets or clobbers your local
data (see `docs/E2E.md` §3).

**New install?** Skip straight to step 2 without creating `.env` —
`cmd/api` serves an installation wizard at `http://localhost:8080/install`
(proxied by the frontend at `/install`) whenever required config is missing.
It walks you through the database/Redis/RustFS/mail connection, writes
`.env` for you, and creates your first admin account.

```bash
# 1. Env (optional — skip this and use the /install wizard instead)
cp .env.example .env
#    → set APP_ENCRYPTION_KEY (openssl rand -base64 32) and JWT_SECRET.

# 2. Start the dev server: api (auto-migrates the DB) + worker + frontend
make up        # api :8080, frontend :5173 — logs in .stack/
make down      # stop it again
```

There are two env files at the repo root, and they never mix:

| File | Used by | What it points at |
|---|---|---|
| `.env` | `make up` | **Your** dev setup — real database, SMTP, payment gateway, … Not committed. |
| `.env.testing` | `make cover` / `lint` / `e2e` / `test` | Everything mocked: `whmcs_e2e` database, mockserver, its own Redis DB and S3 bucket. Created from [`.env.testing.example`](.env.testing.example) on first use. Not committed. |

`make up` starts the api and worker under [`air`](https://github.com/air-verse/air)
(live reload) when it is installed, and always kills whatever is listening on
the api/frontend ports first. `make down` stops everything, frees the ports and
clears leftovers (stray workers, the app's Redis cache and locks, built binaries).
The seeded admin login of the **test** stack is **`admin@e2e.test` /
`AdminE2E!2026`** (never reachable in a production deployment you seed yourself).

To run the pieces by hand instead (config is read from the root `.env` via `ENV_FILE`):

```bash
cd mockserver && go run .                        # optional: mocks of Duitku/WHM/DA/RDash + mail capture on :9090
cd backend && ENV_FILE=../.env go run ./cmd/api  # auto-migrates the DB
cd backend && ENV_FILE=../.env go run ./cmd/worker
cd frontend && npm install && npm run dev        # http://localhost:5173 (API_URL defaults to :8080)
# Dev/demo data (admin+staff users, a cPanel/DirectAdmin server pair pointed at the mockserver,
# a shared_hosting product) — idempotent; only needed if you skipped the wizard's admin step:
cd backend && ../scripts/with-env.sh .env go run ./cmd/seed
```

Health probes: `GET :8080/healthz` (liveness), `GET :8080/readyz` (PG+Redis+S3).

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

Everything runs through the one root `Makefile` (`make help` lists the targets):

| Command | What it does |
|---|---|
| `make up` | Start the dev server (api + worker + frontend) with `.env`; kills anything on its ports first. |
| `make down` | Stop it: kill the processes, free the ports, clear the app's Redis cache/locks and other leftovers. |
| `make cover` | Backend build + vet + tests **with coverage, enforcing the >90% gate** (`.env.testing`). Needs Postgres/Redis/S3 running; creates and migrates the test DB and starts the mockserver itself. |
| `make lint` | gofmt, `go vet`, golangci-lint, prettier and svelte-check (`.env.testing`). |
| `make format` | Auto-format: gofmt (backend, mockserver) + prettier (frontend). |
| `make test` | `lint`, then `cover`, then the Playwright e2e suite (`.env.testing`). |
| `make e2e` | Only the Playwright suite, on a fresh mocked stack it starts and stops itself. Filter with `SPEC=<file>`. |
| `make help` | List the commands. |

`make test` / `make e2e` start their own stack on the ports the specs hardcode
(api `8080`, frontend `5173`, mockserver `9090`), so a dev server from `make up`
is stopped to make room.

### Backend coverage obligation — >90% (mandatory)

Per the quality gate in [`docs/CONTRACTS.md`](docs/CONTRACTS.md) §14, **total statement
coverage over `internal/...` + `pkg/...` must stay above 90%.** `make cover`
measures it and **fails if it drops below the threshold** (override with
`make cover COVERAGE_MIN=95`). Every new exported function needs tests; open
`backend/coverage.html` to find uncovered lines.

Because the repository layer is exercised by `//go:build integration` tests against a
real PostgreSQL, the gate runs the **full** suite (unit + integration) — which is why
`make cover` needs a running database (it creates and migrates `whmcs_e2e` for you).
A unit-only `go test` run does **not** reach 90% on its own.

Underlying commands, if you need them directly (`scripts/with-env.sh` loads an env file first):

```bash
cd frontend && npm run check    # svelte-check (type/a11y)
cd frontend && npm run build    # production build
cd backend && ../scripts/with-env.sh .env.testing go test ./internal/modules/billing/...   # one package
cd backend && ../scripts/with-env.sh .env go run ./cmd/migrate up                          # manual migration of your dev DB
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
