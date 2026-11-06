# Changelog

All notable changes to WHCMS are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[Semantic Versioning](https://semver.org/) once tagged releases begin.

## [Unreleased]

### Added
- Domain sniper (admin side): background hunts that keep trying to register a
  dropping domain until the registrar accepts it. A supervisor in the worker
  claims hunts under a Postgres lease (restart/second-worker safe), probes
  availability cheaply and bursts register calls inside the drop window,
  verifies ambiguous replies before retrying, pauses all hunts when the
  reseller credentials/balance fail, and refunds paid hunts that lose.
  Admin pages under `/admin/snipes`; new `snipe.*` settings, migrations
  `000025`/`000026`; mockserver gains `/mock/rdash/{script,attempts,fault,balance}`.
- Domain sniper (client side, paid up front): admin-priced plans
  (`/admin/snipes/plans`), a client area under `/snipes` with a live price
  breakdown, one invoice (service fee + domain price) whose payment starts
  the hunt, and refunds to account credit when a hunt ends without winning
  (domain price back, service fee kept; a payment that arrives after the
  hunt ended is credited back in full). Unpaid hunts are swept once their
  invoice is a day overdue. Client drop windows need both ends and are capped
  at 6 hours; the sniper accepts new paid hunts only while `snipe.max_active_global`
  (default 50) has room; refunds are held back until the registrar confirms the
  domain is not ours.
- Modular payment-gateway registry: Duitku config (incl. base URL) editable
  live from the admin, plus a manual bank-transfer gateway with
  admin-confirmed settlement; VA/QRIS instructions render inline and pending
  payments resume on reload instead of starting over.
- Global `_layout` email wrapper, redesigned default templates, and a
  synchronous admin "Send Test Email" (migration `000020`).
- Per-user email delivery history for clients (`/account/email-history`,
  migration `000021`).
- Billing: "Invoice Selected Items" and admin-initiated prorated upgrade
  invoices.
- Admin "Pending Module Actions" queue (view/retry/dismiss stuck provisioning
  and domain jobs) with a dashboard KPI card.
- Catalog: product duplication, per-product shell/CGI access and panel
  feature-set selection; provisioning reuses control-panel packages whose
  dynamic specs already match, and supports WHM reseller package-name
  prefixes.
- Orders: `on_order` products activate immediately without payment; checkout
  invoices with nothing due auto-settle without a gateway round-trip.
- `make tunnel` (cloudflared) to expose the local API for real gateway
  callbacks.
- Broader audit-log coverage across client and admin actions.
- Open-source scaffolding: MIT LICENSE, CONTRIBUTING, SECURITY policy,
  Code of Conduct, GitHub Actions CI (backend coverage gate, lint,
  frontend check/build, Playwright E2E), issue/PR templates, dependabot.
- golangci-lint configuration (`backend/.golangci.yml`) and `make lint`.
- Prettier configuration for the frontend (`npm run format`).
- `PANEL_TLS_INSECURE_SKIP_VERIFY` env var: cPanel/WHM and DirectAdmin TLS
  certificates are now **verified by default**, with an explicit opt-out for
  self-signed panels.
- Performance indexes migration (`000014`): invoice_items related lookup,
  admin list sorts (orders/domains/tickets), housekeeping prunes, domain
  renewal cron.
- Password reset/change now revokes every outstanding session
  (`RevokeAllForUser`).
- Baseline security headers on all BFF responses (X-Frame-Options, nosniff,
  Referrer-Policy, Permissions-Policy, frame-ancestors CSP, HSTS on HTTPS).
- Production compose: required `POSTGRES_PASSWORD`, optional Redis auth,
  log rotation, `stop_grace_period`, pinned RustFS version,
  `BODY_SIZE_LIMIT` for uploads through the BFF.

### Changed
- Repository structure: `PRD.md`, `DESIGN.md`, and `DESIGN-FRONT.md` moved
  from the repo root into `docs/`.
- SMTP mailer hardened for real-world providers (implicit-TLS port 465,
  STARTTLS/auth negotiation, HELO hostname).
- Client profile: address fields persist correctly and province is required
  (registrar contact requirements).
- RDash: an existing customer is reused by email instead of erroring on
  duplicate registration.
- Production startup refuses publicly-known dev `JWT_SECRET` /
  `APP_ENCRYPTION_KEY` values.
- Several SvelteKit `load` functions now fetch independent data concurrently.
- `scripts/e2e-up.sh` generates per-checkout secrets, preflights required
  services, and installs frontend dependencies when missing.
- Tooling: the root `Makefile` is now the only one (`backend/Makefile` is
  gone) with `up`, `down`, `cover`, `lint`, `format`, `test` and `help`
  (plus `e2e`, `tunnel`, `hooks`). `make up` is a dev server — api, worker
  and frontend from `.env`, killing whatever holds their ports; `make down`
  stops it and clears leftovers. The old `make test-backend` / `make
  test-frontend` / `scripts/e2e-up.sh` / `scripts/e2e-down.sh` are replaced by
  `make cover` / `make e2e` and `scripts/stack.sh`.
- Config is split into two root env files: `.env` (dev, used by `make up`) and
  `.env.testing` (mocked; used by cover/lint/test/e2e, created from
  `.env.testing.example` on first use). `backend/.env` and `frontend/.env` are
  superseded by the root `.env`. The test stack now has its own Redis DB
  (13) and S3 bucket (`whmcs-e2e`), so it can no longer share queues with a
  dev worker.
- `make lint` now covers the frontend too (prettier + svelte-check) and
  `make format` formats both sides.

### Fixed
- Provisioning: account creation is idempotent; username collisions and
  reserved-username rejections retry with a fresh candidate; DirectAdmin
  account creation sends the server IP and tolerates DA's no-error-key
  package dumps.
- E2E infrastructure can no longer silently target a real database, and
  teardown sweeps orphaned dev workers.
- `.env` parsing strips trailing comments.

### Removed
- Committed `backend/seed` build artifact (13.7 MB) — untracked and ignored.
