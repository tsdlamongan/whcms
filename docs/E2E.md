# WHCMS — Full-Stack E2E Guide

> How the full local stack (Postgres + Redis + RustFS + mockserver + api + worker + frontend)
> is stood up as one live system and driven with Playwright, covering PRD §13.2's 8 critical
> flows plus the per-feature specs that have grown around them.

## 1. Local topology (all on localhost, native processes — no Docker needed)

You don't start the app processes by hand: **`make e2e`** (or `make test`, which runs it last) builds and starts
them from `.env.testing` (`scripts/stack.sh up testing`), runs Playwright, and always stops them again —
whether the suite passed or not. Logs stay in `.stack/*.log`. `make e2e SPEC=admin-ops.spec.ts` runs one spec.

| Process | Port | Started by |
|---|---|---|
| PostgreSQL | 5432 | you — already running (root/postgres). The stack creates+uses its own `whmcs_e2e` database, separate from whatever `whmcs` database you use for manual local testing — see §3. |
| Redis | 6379 | you — already running, no password. The stack uses its own DB index (`REDIS_DB=13`). |
| RustFS (S3) | 9000 | you — already running (rustfsadmin/rustfsadmin). The stack uses its own bucket, `whmcs-e2e`, created on first start. |
| mockserver | 9090 | `make e2e` (built from `mockserver/`) |
| backend api | 8080 | `make e2e` (`cmd/api`, runs migrations) |
| backend worker | (none) | `make e2e` (`cmd/worker`) |
| frontend | 5173 | `make e2e` (`npm run dev`; Playwright's `webServer` then just reuses it) |

The specs hardcode these ports, so `make e2e` kills whatever is listening on 8080/5173/9090 first — including a
dev server from `make up`.

## 2. Env for the E2E stack (must match mockserver's defaults exactly)

The whole stack reads **`.env.testing`** at the repo root. It is created on first use from
[`.env.testing.example`](../.env.testing.example) (committed, no secrets) with freshly generated `JWT_SECRET` /
`APP_ENCRYPTION_KEY` — keep the generated file: settings stored in `whmcs_e2e` are encrypted with that key, and
changing it makes them undecryptable (drop the database too if you must regenerate it). `.env.testing` is never
committed; your dev setup lives in a separate `.env` that the test targets never load into the tests.

`DUITKU_MERCHANT_CODE`/`DUITKU_API_KEY` MUST equal mockserver's own `DUITKU_MERCHANT_CODE`/`DUITKU_API_KEY` env
(both default to `DEMO`/`secretkey` — the example sets both sides from the same file) or every Duitku signature
check fails with 400. `make cover` and `make lint` use the same file.

## 3. Database & storage reset

E2E/test runs use their own **`whmcs_e2e`** database — separate from the `whmcs` database a developer's own
locally-running backend uses for manual testing (via the installer wizard or a hand-set `DATABASE_URL`). This
means running `make cover`/`make e2e`/`make test` never touches or clobbers your local dev data. Both create
`whmcs_e2e` if it doesn't exist yet (`scripts/lib.sh`'s `ensure_database`), and refuse to run at all unless
`DATABASE_URL` contains `_e2e` or `_test` — a guard against ever pointing the integration tests (which create and
delete real rows) or the e2e seeder at a real database.

**Redis and S3 are isolated too:** `.env.testing` uses its own Redis DB index (13; your dev `.env` uses 0, and the
Go unit tests use 15 and `FLUSHDB` it) and its own bucket (`whmcs-e2e`). asynq hands each queued job to whichever
worker listens on the same Redis DB, so sharing one would let a developer's own `go run ./cmd/worker` (pointed at
`whmcs`) consume jobs the E2E api enqueued against `whmcs_e2e`, run them against the wrong database and fail them —
a slow, widely failing suite ("waiting for mail…", services stuck `pending`) that looks nothing like its cause.
Separate DB indexes remove that; every `make e2e` also wipes `whmcs:*` / `asynq:*` in DB 13 so no stale jobs
survive from an earlier run. (If `.env` and `.env.testing` ever point at the same Redis DB, the stack warns and
leaves it alone.)

Stray workers are still a hazard within one environment: `go run` runs the real worker as a *child* process, so
killing the `go run` PID leaks the child — it survives with `PPID=1`, runs stale code and keeps consuming the
queue invisibly. The stack builds its binaries into `.stack/bin` and runs them directly, and `make down` (also run
first by `make up` / `make e2e`) sweeps anything left behind: pid files, the binaries, and any hand-started
`air` / `go run` / compiled worker whose working directory is `backend/` (other projects' `cmd/worker` are never
touched). To check by hand: `pgrep -fl 'cmd/worker|go-build.*/worker$'` should be empty, and
`redis-cli -n 13 --scan --pattern 'asynq:servers:*'` should list exactly one worker while the stack is up.

To force a full clean slate (wipe accumulated per-module-test leftovers), drop and recreate it explicitly:
```sh
psql -U root -h localhost -d postgres -c "DROP DATABASE IF EXISTS whmcs_e2e WITH (FORCE);" -c "CREATE DATABASE whmcs_e2e;"
```
The api binary runs migrations on startup (idempotent), so the next `make e2e` after the drop/recreate re-applies
schema + seed (settings, email templates, ticket departments, rdash registrar row).
RustFS: clear and recreate the `whmcs-e2e` bucket via the S3 API (same pattern used at project start — `mc` isn't
installed, use `curl --aws-sigv4` against `localhost:9000` with `rustfsadmin`/`rustfsadmin`, or a tiny Go/Python
script using the same HMAC signing) so no stray ticket-attachment objects survive from earlier module test runs.

To reset your separate **local** `whmcs` database (e.g. to start fresh with the installation wizard), run the
same drop/recreate against `whmcs` instead, then run `make up` again — see root `README.md`.

## 4. Seed data (build `backend/cmd/seed`, per repo layout — a small idempotent CLI)

Minimum fixtures E2E flows need (use `ON CONFLICT DO NOTHING`/upsert so the seeder is safe to re-run):
- Admin user (email `admin@e2e.test`, password known to test authors, role `admin`).
- Staff user (email `staff@e2e.test`, role `staff`, permissions WITHOUT `settings` — needed for the RBAC flow
  that proves staff can't reach `/admin/settings`).
- A server row pointing at mockserver for **both** modules: hostname `localhost`, port `9090`, `use_ssl=false`,
  module `cpanel` (username/token any non-empty string, NOT `badtoken`) and a second row module `directadmin`
  (username/password any non-empty string, NOT `bad`). A server group per module referencing its server.
- Two products: one `shared_hosting` bound to the cpanel server group, `auto_setup=on_payment`, with pricing for
  at least `monthly` (a small IDR amount, e.g. 50000) and a `setup_fee` of 0; one `domain` type product (or rely
  on the domain order flow's own product-less domain-register path — check how `orders` module models a
  standalone domain purchase per its DTO on disk and seed whatever it needs).
- The `rdash` registrar row already exists from the core seed migration — update its `config` JSONB if the
  domains/orders module expects anything beyond what's there (check `domains.RegistrarRepo`/`orders` service on
  disk).
- One active coupon for a coupon-application spot-check (optional, nice-to-have, not one of the 8 mandated flows).
- Do NOT hand-craft `services`/`domains`/`invoices` rows for the "happy path" flows — those are created by
  driving the real order→pay flow through the API/UI, which is the point of E2E. You MAY need to fabricate
  state directly (DB write or an admin API call) for the **renewal/unsuspend** flow, since waiting real calendar
  days isn't feasible — see §5.5 below.

## 5. The 8 flows (PRD §13.2) — one Playwright spec file per flow, `frontend/tests/e2e/<flow>.spec.ts`

Use `data-testid` selectors documented by the frontend builders (see `docs/RECONCILE.md`'s note on the
`<span class="contents" data-testid=...>` wrapper pattern for buttons and `#field-<name>` for FormField inputs,
plus each page's own testids — grep the relevant route's `.svelte` files for `data-testid` if unsure). Use
UNIQUE test data per spec (timestamp/random-suffixed emails, domains, coupon codes) — specs may run concurrently
against the SAME live backend, so never assert exact global counts (e.g. "exactly N products in the list");
assert your own created row's presence/state instead.

1. **Register → verify → login**: fill the register form with a fresh email, capture the verification link via
   the mock mail sink (`GET http://localhost:9090/mail/messages?to=<email>` — parse the token out of the emailed
   verify-email link/body), visit it (or POST the token per the frontend's actual verify-email page contract),
   then log in with the same credentials and land on the client dashboard.
2. **Order hosting → checkout → pay → active**: browse the seeded hosting product, add to cart, checkout (as a
   just-registered-and-verified user or a fresh one created inline), land on the invoice page, choose a Duitku
   payment method, follow the mock payment page (`http://localhost:9090/payment/<reference>`, click "Bayar
   Sekarang"), get redirected back, poll until the invoice shows Paid, then confirm the service reaches `active`
   (worker must actually run for provisioning to fire — make sure the worker process is up for this spec).
3. **Check domain → order → register → active**: use the domain search (append `-available` or avoid the word
   "taken" in the chosen name so mockserver reports it available), add to cart as a domain registration, checkout
   + pay same as above, confirm the domain reaches `active` in the client domains list.
4. **Client manage service & domain**: on an active service from flow 2, change its password via the client UI
   and confirm success feedback; on the active domain from flow 3, change nameservers via the client UI and
   confirm the new values are reflected (mockserver stores and returns them).
5. **Pay renewal invoice → unsuspend**: since real calendar time can't pass in a test, fabricate the "overdue,
   about-to-be-suspended" state directly — either (a) call an admin endpoint to force a service into `suspended`
   with an open unpaid renewal invoice, or (b) directly UPDATE the service's `next_due_date` to the past and let
   an admin action (or a direct DB nudge) create the renewal invoice, then suspend it — whichever is less invasive
   given the actual admin API surface on disk (check `internal/modules/provisioning` and `internal/modules/billing`
   handler routes for anything usable, e.g. admin suspend + an admin "generate renewal invoice now" affordance;
   if nothing fits, direct SQL against the `whmcs_e2e` DB in a test setup step is acceptable ONLY for this one
   flow, clearly commented as to why). Then pay that renewal invoice via the client UI same as flow 2 and confirm
   the service returns to `active` (unsuspended).
6. **Support ticket**: as a client, open a ticket (with an attachment), confirm the mock mail sink received the
   department/admin notification, reply, confirm status transitions, close it.
7. **Admin**: log in as the seeded admin, create a NEW product from the admin UI, perform a manual provisioning
   action on an existing service (e.g. suspend then unsuspend from the admin services page), view the dashboard
   (confirm KPIs render without error) and at least one report page (revenue) without error.
8. **RBAC**: log in as the seeded staff user (no `settings` permission) and confirm `/admin/settings` is
   inaccessible (redirect, 403 page, or hidden nav — whatever the actual frontend guard does); log in as a plain
   client and confirm any `/admin/*` route redirects away / is inaccessible.

## 6. Fixing real mismatches as you go

`docs/RECONCILE.md` lists every shape the frontend GUESSED against the backend contract before the backend
modules existed. Now that both sides are real, when a flow breaks because of a mismatch: prefer fixing the
**frontend** loader/action to match the REAL backend response (the backend was built against the authoritative
`docs/MODULES.md` spec). Only add something to the **backend** if the frontend need is genuine and reasonable
and truly has no existing equivalent (e.g. a missing list endpoint) — keep such additions small and additive
(new handler method + route, not a redesign), and update `docs/RECONCILE.md`'s entry once fixed so it's not
mistaken for still-open.

## 7. Definition of done

- `frontend/tests/e2e/*.spec.ts` for all 8 flows pass headless Chromium, run together (not just individually —
  cross-test interference is a real risk since they share one backend + DB).
- No flow required disabling/skipping a real product feature to pass.
- `make e2e` (`scripts/stack.sh`) stands the whole stack up, runs the suite and tears it down, so the stack can be
  reproduced with one command.
