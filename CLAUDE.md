# CLAUDE.md — WHCMS engineering guide

Guidance for any developer or AI agent adding features to this repo. Read this
first, then the authoritative docs it points to. **When this file and the code
disagree, trust the code and fix this file.**

WHCMS (Web Hosting Central Management System) is a **WHMCS-clone billing & automation platform** for hosting
providers: client management, product catalog, orders, IDR invoicing, Duitku
payments, automatic cPanel/DirectAdmin provisioning, RDash domain management,
support tickets, and a WHMCS-style admin + client area. Go API + SvelteKit BFF.

---

## 0. Golden rules (do not violate)

1. **Backend coverage must stay > 90%.** `make cover` enforces it and fails the build below the threshold. New exported functions need tests. (See §5.)
2. **Money is IDR-only, `int64` whole rupiah**, DB `BIGINT`. Never floats for money. Currency defaults to `'IDR'`.
3. **Secrets never touch source control, plaintext DB columns, or API responses.** Most module secrets (SMTP, CAPTCHA, etc.) are environment-only. The RDash registrar and Duitku gateway API keys are the exception: admin-configurable dynamically (no restart) via `AES-256-GCM`-encrypted `*_enc` columns/settings (`ports.Encryptor`), decrypted ad-hoc at the call site — env vars remain the fallback for whichever field is left blank, and no endpoint ever echoes back a stored key or its ciphertext, only a `*_present` boolean. The RDash base URL (`registrars.base_url`, a plain non-secret "custom endpoint" override — e.g. to point at the mockserver instead of production without restarting) follows the same dynamic/env-fallback pattern, just unencrypted. See `docs/CONTRACTS.md` §5, §10, §11.
4. **Respect the ownership/layering map** in [`docs/CONTRACTS.md`](docs/CONTRACTS.md) §1. `docs/CONTRACTS.md` is the **binding architecture contract** — align to it; don't redefine enums, envelopes, or signatures.
5. **The HTTP envelope is fixed**: success `{"data":…,"meta":…|null,"error":null}`, error `{"data":null,"error":{"code","message","details"}}`. Pagination `?page=&per_page=` (default 1/10, max 1000). Every paginated list in the UI must offer a page-size selector with options 10/25/50/100/500/1000, defaulting to 10.
6. **Never break the E2E contract, and every new frontend flow ships its own E2E test.** Preserve every `data-testid`, form `action`, and field `name` the Playwright suite depends on (`frontend/tests/e2e/`), and add/extend a spec that drives any new user-facing feature/route/flow (the FE analog of the >90% backend gate — see §5). Run `make e2e` before claiming done on frontend work.
7. **Don't hand-edit generated/foundation-owned files** to route around a rule (router wiring, migrations history, `internal/ports/ports.go` signatures). Add migrations forward; don't rewrite applied ones.
8. **No global mutable state** in the backend; config is loaded once into `platform/config.Config` and injected. No `panic` in the request path.

---

## 1. Commands (root `Makefile` — run `make help`)

| Command | What it does |
|---|---|
| `make up` | Start the **dev server** (api + worker + frontend) from `.env` — your real DB/SMTP/gateway, no mockserver. Kills whatever holds its ports first; api/worker run under `air` when installed. Logs in `.stack/`. |
| `make down` | Stop it: kill the processes (and stray workers), free the ports, clear the app's Redis cache/locks and built binaries. |
| `make cover` | Backend `build` + `vet` + tests **with coverage, enforcing >90%** (`.env.testing`). Creates/migrates `whmcs_e2e` and starts the mockserver itself; needs Postgres/Redis/S3 running. |
| `make lint` | gofmt, prettier, `go vet`, golangci-lint, svelte-check and the e2e fixture-import guard (`.env.testing`) — the CI lint set plus the frontend format check. |
| `make format` | Auto-format: gofmt (backend, mockserver) + prettier (frontend). Run it when `make lint` complains about formatting. |
| `make test` | `lint`, then `cover`, then `e2e`. |
| `make e2e` | Only the Playwright suite, on a fresh mocked stack (mockserver + api + seed + worker + frontend from `.env.testing`) that it starts and always stops. Filter: `make e2e SPEC=admin-ops.spec.ts`. |
| `make help` | List the commands. |
| `make hooks` / `make tunnel` | Install the git hooks (pre-push runs `make lint`; bypass once: `SKIP_LINT=1 git push`) / cloudflared tunnel to the dev API for real gateway callbacks. |

This is the **only** Makefile (there is no `backend/Makefile`). Process lifecycle lives in `scripts/stack.sh`; `scripts/with-env.sh <env-file> <cmd…>` runs any command with an env file loaded (e.g. `cd backend && ../scripts/with-env.sh .env.testing go test ./internal/modules/billing/...`).

**Two env files at the repo root, never mixed:** `.env` (dev; `make up`; template `.env.example`, which also holds the frontend's `API_URL`/`PUBLIC_APP_NAME`; optional — without it the API serves the `/install` wizard) and `.env.testing` (mocked; cover/lint/test/e2e; created from `.env.testing.example` on first use with generated secrets — keep it, the test DB's encrypted settings depend on its key). Neither is committed. Env files are parsed like the Go loader does, never `source`d/`include`d, and the file wins over the caller's shell env.

Prerequisites: Go ≥1.26, Node 22, and locally-running **PostgreSQL 18** (`postgres://root:postgres@localhost:5432/whmcs`), **Redis** (`:6379`), **RustFS/S3** (`:9000`, `rustfsadmin`/`rustfsadmin`, bucket `whmcs`). Full setup: [`README.md`](README.md) and [`docs/E2E.md`](docs/E2E.md). Seeded admin login: `admin@e2e.test` / `AdminE2E!2026`.

`whmcs` above is the developer's own **local** database — untouched by any test target. `.env.testing`, `make cover`/`make e2e` and every `_integration_test.go` instead use a separate **`whmcs_e2e`** database on the same Postgres server (auto-created if missing; the test targets refuse a `DATABASE_URL` without `_e2e`/`_test` in it), plus their own Redis DB index (13) and S3 bucket (`whmcs-e2e`) — so running tests never resets or clobbers local manual-testing data or shares queues with a dev worker (`docs/CONTRACTS.md` §11, `docs/E2E.md` §3). First-time local setup (creating/configuring the `whmcs` database, the first admin account) is handled by the installation wizard at `/install` — see `docs/CONTRACTS.md` §15 — rather than by a manual `createdb`/seed step.

Manual migrations against your **dev** DB (check first — `make up` already migrates on start): `cd backend && ../scripts/with-env.sh .env go run ./cmd/migrate up|down N`.

---

## 2. Where things live (and which doc is authoritative)

| Concern | Location | Authoritative doc |
|---|---|---|
| Architecture, enums, envelope, ports, DB schema, API surface, settings keys, env vars | — | [`docs/CONTRACTS.md`](docs/CONTRACTS.md) **(binding)** |
| Pinned stack + versions | — | [`docs/STACK.md`](docs/STACK.md) |
| Backend module work orders | `backend/internal/modules/*` | [`docs/MODULES.md`](docs/MODULES.md) |
| Composition / DI wiring | `backend/internal/composition` | [`docs/WIRING.md`](docs/WIRING.md) |
| Frontend routes/components/i18n | `frontend/src` | [`docs/FRONTEND.md`](docs/FRONTEND.md) |
| Full-stack E2E topology | `scripts/`, `frontend/tests/e2e` | [`docs/E2E.md`](docs/E2E.md) |
| FE↔BE request/response shape reconciliation | — | [`docs/RECONCILE.md`](docs/RECONCILE.md) |
| Product spec | — | [`docs/PRD.md`](docs/PRD.md) |
| Admin UI theme (HostPanel) | `frontend/src/lib/styles/hostpanel.css`, `frontend/src/lib/components/hp/` | this file §4 |
| Client/Public UI theme (Twenty-One) | `frontend/src/lib/styles/twentyone.css`, `frontend/src/lib/components/ca/` | this file §4 |

```
/backend                  Go module "github.com/tsdlamongan/whcms/backend"
  /cmd/{api,worker,seed,migrate}   entrypoints (api auto-migrates on startup)
  /internal/domain        entities, enums, state machines, money, business rules
  /internal/ports         ALL interfaces (repos, integrations, infra) — read-only for modules
  /internal/modules/<mod> vertical slice: dto.go, handler.go, service.go, repo.go (+ _test.go)
  /internal/repository    cross-cutting repos (audit, emaillog, integrationlog, settings)
  /internal/service       cross-cutting services shared across modules (authtoken, settings)
  /internal/integration/{duitku,cpanel,directadmin,rdash}   external adapters
  /internal/transport/http middleware + shared handlers (routes are mounted in cmd/api/main.go)
  /internal/platform      config, db, redis, logger, storage(s3), crypto, mailer, queue, lock, token
  /internal/jobs          asynq task constants + payload structs
  /internal/worker        asynq worker runtime: worker.go, handlers.go, schedules.go (cron/periodic)
  /internal/composition   build.go — the composition root wiring everything
  /pkg/{apperr,httpx}     error codes + response envelope/pagination
  /migrations             golang-migrate SQL (forward-only; never rewrite applied files)
/frontend                 SvelteKit 2 + Svelte 5 runes + TS + Tailwind v4 (adapter-node, BFF)
/mockserver               "whcms-mock": mock Duitku/WHM/DA/RDash + mail capture (:9090)
/deploy                   Dockerfiles, docker-compose{,.prod}.yml, env-examples
```

Modules: `auth, catalog, orders, billing, payments, provisioning, domains, tickets, clients, adminops, notifications, announcements, knowledgebase, networkstatus, contact, snipe, install`.

---

## 3. Backend — how to add or change a feature

Work as a **vertical slice inside `internal/modules/<mod>/`**. Typical order:

1. **Domain** (`internal/domain`): if you need a new entity/enum/state transition, add it here. Enum string values must match DB CHECK constraints (CONTRACTS §4). Prefer `CanTransition(from,to)` guards over ad-hoc status writes.
2. **Migration** (`backend/migrations`): add a new forward migration pair for schema changes. Never edit an applied migration. The API runs migrations on startup.
3. **Ports** (`internal/ports`): if a service needs a new capability, add the interface method here (foundation-owned; keep signatures minimal and `ctx`-first). Add/extend the fake in `internal/ports/mocks` (`Mock<Interface>`).
4. **Repo** (`<mod>/repo.go`): hand-written parameterized SQL via pgx (no ORM, no string interpolation). Add a `//go:build integration` test in `<mod>/repo_integration_test.go` against real PG.
5. **Service** (`<mod>/service.go`): use-case logic. Takes `ctx` first, returns `*apperr.Error` (codes: `VALIDATION, UNAUTHORIZED, FORBIDDEN, NOT_FOUND, CONFLICT, RATE_LIMITED, PAYMENT_REQUIRED, EXTERNAL, INTERNAL`). Unit-test with fakes of ports. Call `AuditLogger` for sensitive actions.
6. **DTO** (`<mod>/dto.go`): request/response structs with `validate:` tags; validate via `platform/validate.Struct`.
7. **Handler** (`<mod>/handler.go`): thin Fiber v3 handler — bind+validate DTO, call service, write via `httpx` envelope helpers. Read identity with `httpx.Identity(c)`. Never put business logic here.
8. **Wire it** in `internal/composition/build.go` and mount the module's `RegisterRoutes` in `cmd/api/main.go`. Protect admin routes with `RequireRole("admin","staff")` + `RequirePermission("<module>")`; client routes with `RequireClient` and **always filter queries by `client_id` from the token**.
9. **Test to keep coverage > 90%** (§5), then `make cover`.

Key conventions (full list: CONTRACTS §3): `int64` IDs/money; `TIMESTAMPTZ`/UTC internally; soft-delete only on `clients`/`products`/`product_groups`; Redis key prefix `whmcs:`; idempotent payment activation via row lock + partial unique index + status check (never trust a callback alone — confirm via Check Transaction); adapters log every external call via `IntegrationLogger` with secrets redacted.

---

## 4. Frontend — how to add or change a feature

- **BFF only.** The browser never calls the Go API directly. SvelteKit `load`/actions call the API server-side via `$lib/server/api.ts` (`apiFetch`), which attaches the Bearer token from httpOnly cookies and unwraps the envelope into `{data, meta, error}`. `locals.user` comes from `/auth/me`.
- **Route groups** (`frontend/src/routes`): `(public)` (login/register/order/payments), `(client)` (guarded), `(admin)` (guarded, role admin/staff). Each page = `+page.server.ts` (load + form `actions`) + `+page.svelte`. Guards live in the group `+layout.server.ts`.
- **Svelte 5 runes**: `$props`, `$state`, `$derived`, `$effect`. TypeScript strict. Prefer server-side `load` over client fetches; use `use:enhance` for form actions.
- **Money**: `Intl.NumberFormat('id-ID',{style:'currency',currency:'IDR',maximumFractionDigits:0})` (or `MoneyText`). **Dates**: Asia/Jakarta.
- **i18n**: `$lib/i18n`, `t('key')`, dictionaries `id`/`en` (default `id`). New client/public strings go through `t()`.
- **Preserve `data-testid`s, form `action`s, and field `name`s** — the Playwright suite keys on them. If you must change one, update the spec in the same change and re-run `make e2e`.

### Admin UI theme — HostPanel (WHMCS "blend")

The `(admin)/admin/**` area uses a **dedicated WHMCS-blend theme called HostPanel**, separate from the Tailwind/Inter look of the client & public areas. (This supersedes the older admin-design note in CONTRACTS §13.)

- Theme CSS: `frontend/src/lib/styles/hostpanel.css` — **every rule is scoped under `.hp-admin`** (set on the admin layout root) so it never leaks into client/public. Palette: navy `#1A4D80` chrome, `#337AB7` primary; status green `#46A546` / yellow `#F89406` / red `#C43C35` / gray `#BFBFBF`.
- Shell components: `frontend/src/lib/components/hp/` — `HpNavbar`, `HpSidebar`, `HpFooter`, `HpPanel`, `HpStatCard`, `HpModal`, and `nav.ts` (top-menu model + `sidebarKind(pathname)` contextual sidebar + `pageTitle(pathname)`).
- Font Awesome 6 + Open Sans are loaded via CDN `<link>` in the admin `+layout.svelte` `<svelte:head>` (admin routes only).
- When adding/rethemeing an admin page: use the `hp-*` classes (`hp-table`, `hp-panel`, `hp-btn`/`hp-btn-primary`, `hp-badge`, `hp-input`/`hp-select`, `hp-filter`, `hp-pager`, `hp-formrow`, `hp-tabs`…), keep the existing `+page.server.ts` load/actions and all testids, and use English labels to match the theme. Admin uses `.hp-*`; the client/public areas use the Twenty-One `.ca` theme below — don't cross the two.

### Client/Public UI theme — Twenty-One (`.ca`)

The `(public)/**` and `(client)/**` areas use a **dedicated WHMCS "Twenty-One" theme** (per [`docs/DESIGN-FRONT.md`](docs/DESIGN-FRONT.md)). **This supersedes the earlier "client/public stay Tailwind" note** — that direction was deliberately changed to make the client area match real WHMCS.

- Theme CSS: `frontend/src/lib/styles/twentyone.css` — **every rule is scoped under `.ca`** (set on the shell root) so it never leaks into admin. Palette: primary `#336699`, success `#218739`, body `#F1F1F1`, breadcrumb `#E9ECEF`, footer `#404040`/`#EEEEEE`, radius `3.5px`, container `1140px`, Open Sans 14px. Mobile-first (breakpoints 576/768/992/1200).
- Shell components: `frontend/src/lib/components/ca/` — `CaShell`, `CaHeader` (logo + KB search + cart badge), `CaNav` (Store/Account dropdowns + "More" overflow + mobile drawer), `CaBreadcrumb`, `CaFooter` + `CaLanguageModal`, `CaPanel`, `CaProductCard`, `CaTile`, `CaStoreSidebar`, `CaOrderSummary`, `CaDomainHero`, `CaPrice`, and `nav.ts`. Open Sans + Font Awesome 6 load via CDN `<link>` in `CaShell` `<svelte:head>`.
- **Reuse the shared contract-owning components** (`FormField`, `DataTable`, `StatusBadge`, `Modal`, `ConfirmDialog`, `Toast`, `MoneyText`, `DateText`) — they are re-tinted by scoped `--color-*`/font overrides inside `.ca`, NOT edited (admin depends on their untouched globals). Use `.ca-*` classes / `Ca*` components for chrome; `CaPrice` for new store/hero prices (`Rp… IDR`), `MoneyText` elsewhere.
- **In-flight feedback is mandatory in `.ca` pages.** A button that triggers a request must disable itself and show `<span class="ca-spinner" aria-hidden="true"></span>` + `aria-busy` while it runs (`LoadingButton` does this for you but can't carry a `data-testid` — use the span inside a plain `.ca-btn` when a spec keys on the button). GET search/filter forms have no `use:enhance` state: derive it from `navigating` (`$app/state`), as `order/domain` and `CaDomainHero` do. `CaShell` also shows a top `.ca-navprogress` bar for any navigation slower than ~150ms.
- **Turnstile (`ca/Turnstile.svelte`) is always visible** (`appearance: 'always'`, `flexible`→`compact` under 300px) — never go back to `interaction-only`: the invisible verification window made fast submitters post an empty token. Every page that renders it must `bind:ready` and keep its submit button disabled while the token is missing (see login/register/cart `captchaPending`). The widget recovers from a failed `api.js` load via its own retry button; don't add page-refresh workarounds.
- i18n for new client strings lives in `frontend/src/lib/i18n/messages/portal.ts` (`portal.*`); admin-portal strings in `fe-admin-portal.ts` (`adminPortal.*`). Don't edit `id.ts`/`en.ts`.
- The public **portal Home** is served at `/` for anonymous visitors (`(public)/+page.*`); authenticated users redirect to `/dashboard` (client) or `/admin` (staff). Portal features: `/announcements`, `/knowledgebase`, `/network-status`, `/contact` (backed by the announcements/knowledgebase/networkstatus/contact backend modules); admin-managed under `(admin)/admin/{announcements,knowledgebase,network-status}` (`hp-*` theme).

---

## 5. Testing & the >90% coverage gate (mandatory)

- **Backend gate (CONTRACTS §14):** total statement coverage over `internal/...` + `pkg/...` must stay **> 90%**. `make cover` runs `build` + `vet` + the full test suite with coverage and **fails below 90%** (override `COVERAGE_MIN=95`). Inspect gaps with `backend/coverage.html`.
- The **repository layer is integration-tested** (`//go:build integration`) against real Postgres, so the gate runs unit + integration together and needs a migrated DB (`make cover` creates and migrates `whmcs_e2e` itself and starts the mockserver). A unit-only `go test` run is faster but does **not** reach 90% on its own.
- Test style (CONTRACTS §3): table-driven + testify; services with hand-written fakes of ports (shared in `internal/ports/mocks`, `Mock<Interface>`); adapters with `httptest.Server`; handlers with a Fiber app + mocked services; repos with integration-tagged tests. **Every exported function needs coverage.**
- **Frontend:** `cd frontend && npm run check` (svelte-check, must be 0 errors) + `npm run build` green; `make e2e` (Playwright, 8 critical flows per PRD §13.2) green against the mocked stack it starts itself.
- **E2E specs import `{ test, expect }` from `./fixtures`, never from `@playwright/test`** — the shared fixture's `page.goto` waits for SvelteKit hydration (`<html data-hydrated>`); without it, a spec's first click races hydration on slow machines and fails only in CI. `make lint` / `make e2e` enforce this. If a form submit may fall back to a native post (its click can race hydration too), call `waitForHydration(page)` before driving pure-JS controls on the resulting document.
- **Every new frontend feature/route/flow MUST ship its own Playwright E2E coverage** in `frontend/tests/e2e/` — add a new spec or extend an existing one to drive the new happy path (and key error/guard paths) end-to-end. Passing the existing suite without exercising the new behavior is **not** sufficient; new client/public/admin surfaces need `data-testid`s wired so the spec can key on them. This mirrors the backend rule "every exported function needs coverage" — for the frontend, **every new user-facing flow needs an E2E test.**

---

## 6. Definition of Done (checklist before you say it's finished)

- [ ] `make lint` clean (gofmt + prettier + `go vet` + golangci-lint + svelte-check + e2e import guard); `make format` fixes formatting.
- [ ] Backend: `make cover` passes (build + vet + tests, **coverage ≥ 90%**).
- [ ] Frontend: `npm run check` and `npm run build` clean; `make e2e` passes (or the relevant `SPEC`).
- [ ] **New frontend feature/route/flow adds or extends its own Playwright E2E spec** in `frontend/tests/e2e/` (drives the new flow itself — not just passing the pre-existing suite).
- [ ] New/changed behavior is exercised end-to-end (drove the real flow, not just unit tests).
- [ ] Envelope, enums, error codes, and money/int64 rules honored; secrets from env only.
- [ ] Migrations are forward-only and idempotent; `.env.example` (and `.env.testing.example` if the test stack needs it) updated if a var was added.
- [ ] `data-testid`s / form actions preserved (or specs updated in the same change).
- [ ] Admin pages use the HostPanel `hp-*` theme; client/public use the Twenty-One `.ca` theme (§4) + `t()` i18n.
- [ ] No `panic` in request path, no global state, `gofmt`/idiomatic Go, no leftover P0 TODOs.
- [ ] Commits (when the user asks for one) use **Semantic / Conventional Commit** messages (§7).

---

## 7. Git & commit conventions (mandatory)

**Every commit message MUST follow the [Conventional Commits](https://www.conventionalcommits.org) ("Semantic Commit") format.** This applies to all contributors, including AI agents.

- **Format:** `type(scope): subject` — e.g. `feat(billing): add proration on plan upgrade`.
  - **`type`** (required, lowercase) is one of: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`.
  - **`scope`** (optional but preferred) names the affected area — usually a backend module (`auth`, `billing`, `orders`, `payments`, `provisioning`, `domains`, `tickets`, `clients`, `adminops`, `notifications`), or `frontend`, `admin`, `docs`, `deploy`, `mockserver`.
  - **`subject`**: imperative mood, lowercase start, **no trailing period**, ≤ ~72 chars ("add", not "added"/"adds").
- **Body** (optional, after a blank line): explain *what* and *why*, not *how*. Wrap at ~72 cols.
- **Breaking changes:** append `!` after the type/scope (`feat(orders)!: …`) and/or add a `BREAKING CHANGE: …` footer.
- **Trailers** (e.g. `Co-Authored-By: …`) go in the footer, below the semantic subject/body.
- Keep commits focused: one logical change per commit; don't mix a refactor with a feature.

Examples:
```
fix(payments): confirm Duitku callback via Check Transaction before activating
docs: add internal/worker and RECONCILE.md to the CLAUDE.md map
refactor(catalog)!: drop legacy price_float column from products

BREAKING CHANGE: products.price is now int64 rupiah only.
```

---

## 8. Gotchas

- **Fiber v3**: `fiber.Ctx` is an interface; bind with `c.Bind().Body(&x)`; middleware is `func(c fiber.Ctx) error`.
- Coverage looks low (~80%) if you run unit-only — that's expected; the repository package is only covered by integration tests. Use `make cover` for the real number.
- **`make up`, `make e2e`/`make test` and `make cover` take over ports on purpose:** `up` frees the api/frontend ports (8080/5173), `e2e`/`test` also 9090 (the specs hardcode them), `cover` 9090 — by killing whatever listens there, including a developer's own `air`/`vite`/worker (which point at their real DB and real SMTP). If someone may have their own stack running, ask before running them.
- The stack runs detached (`nohup`); a stack started from a detached/CI context may be reaped when its launching process exits. Logs: `.stack/*.log`.
- Local ports: PG `5432`, Redis `6379`, RustFS `9000`, mockserver `9090`, api `8080`, frontend dev `5173` (Docker frontend `3000`).
- Duitku/RDash secrets in dev must match the mockserver defaults (`DEMO`/`secretkey`, `RDASH_BASE_URL=http://localhost:9090/v1`), or signature/auth checks fail.
