# WHCMS — Frontend Guide

> Binding frontend architecture guide. Read with docs/CONTRACTS.md (§9 API surface, §13 frontend contract) and the
> scaffold code on disk under `frontend/src/` (hooks.server.ts, lib/server/api.ts, lib/components/, lib/i18n/).
> Backend DTO field names = JSON tags in `backend/internal/domain/entities.go` wrapped in the httpx envelope
> `{data, meta, error}`; list meta = `{page, per_page, total}`. Read entities.go to type responses.

## 0. Chassis rules

- **Ownership**: feature work stays inside its route group (§2) and its own `src/lib/i18n/messages/<area>.ts`
  namespace file. The shared chassis — package.json/lock (no new npm deps without a deliberate decision),
  hooks.server.ts, lib/server/*, lib/components/*, lib/i18n/{id,en,i18n.svelte,index}.ts, app.css, root
  layouts — changes only in focused cross-cutting commits, never forked locally inside a feature.
- **Data flow**: `+page.server.ts` `load` via `apiFetch(event, '/api/v1/...')` → returns `{data, meta, error, status}`
  (never throws; `unwrap()` to throw). Mutations = form `actions` (progressive enhancement `use:enhance`), success →
  toast (`$lib/stores/toast.svelte`) or redirect; failure → `fail(status, { errorKey | errorMessage, values })`
  matching the login page pattern. Client-side polling allowed ONLY where specified via a colocated `+server.ts` proxy.
- **Components**: use the scaffold design system (DataTable server-mode with `?page=` URL state, StatusBadge,
  MoneyText for EVERY amount, DateText for every date, Modal/ConfirmDialog for destructive actions, FormField,
  Tabs, StatCard, EmptyState, LoadingButton, Alert, Breadcrumb). Svelte 5 runes only ($props/$state/$derived,
  function bindings for FormField value). No `export let`.
- **i18n**: ALL user-visible strings through `t('...')`. Add keys ONLY in your own
  `src/lib/i18n/messages/<agent>.ts` (default export `{ id: {...}, en: {...} }`, deep-merged; namespace your keys
  under a unique top-level key, e.g. `orderfe.cart.title`). Bahasa Indonesia primary, English complete.
- **data-testid**: every page's primary interactive elements and key data displays get stable `data-testid`
  attributes (kebab-case: `invoice-status`, `pay-button`, `domain-search-input`, `ticket-reply-submit`, table rows
  `row-<entity>-<id>`). E2E depends on these.
- **Errors/empty/loading**: every list has EmptyState; every load handles `error` (Alert with message); actions show
  field errors under inputs.
- **WHMCS look**: dense tables, breadcrumbs, tabbed detail pages, KPI cards; blue accents; badge colors:
  green=active/paid, yellow=pending/unpaid, red=overdue/suspended/terminated, gray=cancelled.
- **Gate**: `npm run check` and `npm run build` with zero errors, and every new user-facing flow ships its own
  Playwright spec (CLAUDE.md §5). If an API shape is unclear, read the backend DTOs — don't guess; when frontend
  and backend disagree on a shape, reconcile per `docs/RECONCILE.md`.
- Smoke-test against the real stack (`make up`) — contract fidelity plus the E2E suite is the bar, not
  "looks right in isolation".

## 1. Shared conventions

- Pagination: DataTable reads `?page=&per_page=`; loads forward those to the API and return `meta`.
- Filters/search: querystring-driven (`?search=&status=`), form GET.
- Client guard: `(client)` layout already redirects anonymous → /login?redirect=. Admin: role guard exists;
  staff-permission hiding: nav shows what `locals.user.role==='admin'` OR permission map allows — keep simple:
  render all for admin, all for staff too (backend enforces; UI hiding is best-effort P2).
- Payment status colors + labels via StatusBadge semantic map (already in scaffold).
- Forms for money input: plain number inputs (IDR, no decimals), display via MoneyText.
- 2FA setup: display secret + otpauth:// URI as copyable text (NO QR — no new deps).
- Reports charts: pure CSS/SVG bars (no chart libs).

## 2. Route map by area (routes under frontend/src/routes; each area has its own i18n namespace file)

### FE-AUTH — `(public)/register`, `(public)/verify-email`, `(public)/forgot-password`, `(public)/reset-password`
Register: full form (email, password+confirm, first/last name, company?, address, city, postcode, country default ID,
phone) → POST /api/v1/auth/register → success page "check your email" (+ resend button → /auth/resend-verification).
Verify-email: reads ?token= → POST /auth/verify-email → success → auto-login? No: link to /login with success alert.
Forgot: email → POST /auth/forgot-password (always success message). Reset: ?token= + new password →
POST /auth/reset-password → redirect /login. All with proper error surfacing (expired token etc.).

### FE-ORDER — `(public)/order/**`, `src/lib/stores/cart.svelte.ts`
`/order`: product groups + product cards (GET /products, /product-groups) w/ pricing per cycle.
`/order/product/[slug]`: configure (cycle select w/ prices, configurable options, domain input: register-new
(availability check POST /domains/check debounced) | transfer (epp later) | own-domain), add to cart.
`/order/domain`: standalone domain search (multi-TLD result list, prices, add register/transfer to cart).
`/order/cart`: items list, remove, coupon apply (POST /coupons/validate), totals w/ tax preview (client-side calc
display only; server is source of truth), proceed → if !locals.user: inline login/register panel (reuse auth
endpoints; on register instruct email verification; block checkout until verified — show state clearly);
checkout submit → POST /api/v1/orders → redirect `/billing/invoices/<invoice_id>` (client area) to pay.
Cart store: $state in localStorage-persisted rune store (guard browser), items typed per CONTRACTS order input.

### FE-CLIENT-CORE — `(client)/dashboard`, `(client)/account/**`, owns `(client)/+layout.svelte` nav polish
Dashboard: StatCards (active services, domains, unpaid invoices w/ total, open tickets — from list endpoints w/
per_page=1 meta.total or a summary endpoint if present), recent invoices table, quick links.
Account: tabs Profile (PATCH profile fields), Security (change password; 2FA setup/enable/disable via /auth/2fa/*),
Contacts (CRUD sub-accounts w/ permission checkboxes), Credit (balance display + ledger table + link deposit).
Client nav: Home/Services/Domains/Billing/Support/Account + locale switcher + logout (keep existing structure).

### FE-CLIENT-SERVICES — `(client)/services/**`
List: DataTable (product name, domain, price MoneyText, next due, status). Detail `[id]`: overview card (product,
domain, server?, dates, recurring), actions: change password (modal, POST /services/:id/change-password), SSO
(GET /services/:id/sso → open url new tab), cancel (modal immediate|end_of_term → POST /services/:id/cancel),
upgrade (pick product+cycle → POST upgrade endpoint → redirect to created invoice), renewal invoice link if unpaid.

### FE-CLIENT-DOMAINS — `(client)/domains/**`
List: DataTable (domain, registrar status badge, expiry DateText, auto-renew toggle inline PATCH). Detail `[id]`:
tabs Overview (dates, status, renew now → POST /domains/:id/renew → redirect invoice), Nameservers (2-4 inputs,
PATCH), DNS (records table editor: type select A/AAAA/CNAME/MX/TXT/NS, host, value, TTL, prio; add/edit/delete
rows client-side then PUT full set), EPP (reveal button GET /domains/:id/epp shows code copyable), Contact
(registrant form PATCH).

### FE-CLIENT-BILLING — `(client)/billing/**`, `(public)/payments/return`
`/billing`: tabs Invoices (DataTable: number, date, due, total, status; filter status) + Transactions (history) +
link deposit. Invoice detail `/billing/invoices/[id]`: WHMCS-style invoice view (company block from settings? just
render invoice fields; items table, subtotal/discount/tax/credit/total), status banner, PDF download (link
/api/v1/invoices/:id/pdf via proxy +server.ts streaming), IF unpaid/overdue: payment panel — methods grid (GET
/payments/methods?invoice_id=, icons+fees), pay button → POST /invoices/:id/pay {method} → render result: redirect
paymentUrl if present, else show VA number / QR string (copyable) + amount + expiry; credit balance option when
sufficient (method credit). Status POLLING while unpaid: colocated `+server.ts` GET returning {status} (proxies
GET /invoices/:id), client interval 4s, on paid → invalidate + success toast. `/billing/deposit`: amount form
(min 10000) → POST /account/credit/deposit → redirect invoice. `/payments/return`: reads ?merchantOrderId= — page
calls its own +server.ts proxy hitting GET /api/v1/payments/return mapping → then redirects to the invoice page
(or shows "payment processing" with link).

### FE-CLIENT-SUPPORT — `(client)/support/**`
List (status filter tabs), `/support/new` (department select GET /ticket-departments, subject, priority, message,
file input multi w/ ext/size hints; multipart POST /tickets), detail `[id]`: thread (replies chronological,
attachments as links via presign redirect endpoint), reply box w/ attachments, close button. Status badge mapping
open/answered/customer_reply/closed.

### FE-ADMIN-CORE — `/admin` dashboard, `/admin/clients/**`, `/admin/orders/**`, owns `(admin)/admin/+layout.svelte` + admin nav
Admin nav (complete, grouped WHMCS-style): Dashboard; Clients; Orders; Billing→Invoices,Transactions,Reports;
Services; Domains; Support→Tickets,Departments; Catalog→Products,Groups,Coupons; Infrastructure→Servers,Registrars,
Gateways; Configuration→Settings,Email Templates,Staff; Logs→Audit,Email,Integration. Dashboard: KPI StatCards
(GET /admin/dashboard: income today/month, orders today, unpaid/overdue, open tickets, active services, pending
provisioning) + recent orders/tickets lists. Clients: DataTable (search/status filter/pagination), create form,
detail tabs: Summary (profile edit + status + credit balance + add/deduct credit modal + impersonate button POST →
sets returned tokens via a dedicated +server.ts that swaps cookies then redirects to /dashboard), Services,
Domains, Invoices, Tickets (embedded filtered DataTables), Notes (admin notes textarea), Contacts. CSV export
button (streams /admin/clients/export.csv via proxy). Orders: DataTable + detail (items, linked invoice, client;
actions accept/cancel/fraud w/ ConfirmDialog).

### FE-ADMIN-BILLING — `/admin/invoices/**`, `/admin/transactions`, `/admin/reports/**`, `/admin/gateways`
Invoices: DataTable (filters status/date/client), detail (invoice view + items editor for draft?, add manual
payment modal {amount, method}, refund modal, cancel, edit due date/notes, download PDF), create manual invoice
(client select — searchable via /admin/clients?search=, items rows, due date). Transactions: DataTable w/ filters.
Reports: revenue (date range, group day|month, SVG/CSS bar chart + totals table + gateway split, CSV export link),
orders report, services report (by product/status). Gateways: Duitku config display (merchant code editable,
mode select, API-key-presence indicator), save.

### FE-ADMIN-OPS — `/admin/services/**`, `/admin/domains/**`, `/admin/servers/**`, `/admin/registrars`
Services: DataTable (filters status/product/server), detail: overview + lifecycle action buttons (Create/Suspend
w/ reason/Unsuspend/Terminate/Change Package select/Change Password) each ConfirmDialog → POST
/admin/services/:id/<action>, pending_upgrade banner, edit next_due_date/notes. Domains admin: DataTable, detail
(sync button, renew, NS override, status). Servers: list (name, module badge, host, accounts/capacity, active),
create/edit form (module select cpanel|directadmin, host/port/username/token|password/ssl/nameservers/max),
test-connection button showing result, server groups CRUD (strategy select, member servers). Registrars: rdash row
config (non-secret JSONB fields + key-presence), test button.

### FE-ADMIN-SNIPES — `/admin/snipes/**` (HostPanel `hp-*` theme)
`/admin/snipes`: overview cards (hunting now, active, won, reseller balance), supervisor state badge (Running/
Paused/Disabled) + red banner with the pause reason, Search/Filter (domain, status), DataTable + `HpPager`, and a
"Hunt Settings" panel editing the `snipe.*` settings through the generic settings endpoint (hidden when the
staff user lacks the `settings` permission). `/admin/snipes/new`: free hunt on behalf of a client (search-driven
client picker, domain, years, optional drop window start/end and give-up time — `datetime-local` values are
Asia/Jakarta and sent as RFC 3339 `+07:00` — or run-for-days, optional nameservers). `/admin/snipes/[id]`: status,
phase, attempts, billing/refund figures, activity trail, Cancel Hunt (modal; "also refund the fee" for paid
snipes). testids: `snipes-table`, `row-snipe-<id>`, `snipe-row-status-<id>`, `snipe-add-new`, `snipe-create-*`,
`snipe-status`, `snipe-cancel-button`/`-confirm`, `snipe-events`, `snipe-paused`, `snipe-settings-*`. E2E:
`tests/e2e/admin-snipes.spec.ts`.

### FE-CLIENT-SNIPES — `(client)/snipes/**` (Twenty-One `.ca` theme, i18n `portal.snipe.*` in `messages/fe-client-snipes.ts`)
`/snipes`: DataTable (status filter, domain search, page-size selector) with an empty state inviting the first
hunt. `/snipes/new`: domain, plan radios (only active plans), years, optional drop window (`datetime-local`,
WIB), optional nameservers, and a LIVE price breakdown (debounced `POST /snipes/quote`, a BFF proxy endpoint so
the browser never calls the Go API) showing fee, domain price, success fee, tax, total and "credited back if not
secured"; submit creates the hunt and redirects to `/billing/invoices/:id` to pay (spinner + `aria-busy` per the
`.ca` rule). `/snipes/[id]`: status, phase, deadline, attempts, billing/refund figures, owner-visible history,
Pay Now (unpaid), Manage Domain (won), Cancel (modal states what is refunded). Nav: account menu `nav-snipes`,
store menu `nav-domain-snipe`. testids: `snipes-table`, `row-snipe-<id>`, `snipe-row-status-<id>`,
`snipe-new-cta`, `snipe-domain-input`, `snipe-plan-<id>`, `snipe-years-select`, `snipe-quote-total`,
`snipe-quote-refundable`, `snipe-quote-error`, `snipe-submit`, `snipe-create-error`, `snipe-status`, `snipe-phase`,
`snipe-pay`, `snipe-cancel`, `snipe-cancel-confirm`, `snipe-refunded`, `snipe-events`. E2E: `client-snipes.spec.ts`.

Admin plans (`hp-*`): `/admin/snipes/plans` — table + add/edit modal (name, window days, service fee, success
fee, sort, active) and a per-row activate/deactivate toggle; testids `snipe-plan-add`, `snipe-plans-table`,
`row-snipe-plan-<id>`, `snipe-plan-edit-<id>`, `snipe-plan-toggle-<id>`, `snipe-plan-form`, `snipe-plan-submit`,
`snipe-plan-error`. E2E: `admin-snipe-plans.spec.ts`.

### FE-ADMIN-CATALOG — `/admin/products/**`, `/admin/product-groups`, `/admin/coupons`
Groups: CRUD (name, slug, sort, hidden). Products: DataTable, create/edit multi-tab form (Details: name/slug/type/
group/description/hidden/welcome template select; Module: module select + package_name + server group + auto_setup
radio; Pricing: table per cycle (one_time..biennially) price+setup editable + stock toggle/qty; Options:
configurable option groups/values editor w/ per-cycle price deltas). Coupons: CRUD (code, type percentage|fixed,
value, applies-to products multiselect, max uses, recurring, expiry, active).

### FE-ADMIN-SUPPORT — `/admin/tickets/**`, `/admin/departments`, `/admin/email-templates/**`, `/admin/logs/**`, `/admin/staff/**`, `/admin/settings/**`
Tickets: DataTable (filters status/dept/priority/assigned), detail: thread incl. internal notes highlighted,
reply box w/ internal-note toggle + attachments, assign select (staff list), status/priority selects, close.
Departments CRUD. Email templates: list by key+locale, edit (subject, body_html textarea w/ variables hint),
preview (POST preview → rendered iframe srcdoc), reset? no. Logs: Audit (filters actor/entity/date, before/after
JSON viewer modal), Email log (status filter, retry failed button), Integration (provider/success filters,
request/response JSON viewer). Staff: CRUD (email, name?, password set, permissions checkboxes per module, active).
Settings: tabbed form (General: company name/address/email/logo upload → if upload endpoint exists else URL field;
Billing: tax enabled/rate/inclusive, due days, renewal lead, late fee, reminders arrays as comma inputs; Automation:
suspend/terminate days; Mail: from name/email, driver info readonly; Tickets: allowed ext, max size) → GET/PUT
/admin/settings.

## 3. Client/Public theme — Twenty-One (`.ca`)

The `(public)/**` and `(client)/**` areas render in a **WHMCS "Twenty-One" theme** (see `DESIGN-FRONT.md`),
the client-side analogue of the admin HostPanel theme. This **supersedes** the earlier "client/public stay
Tailwind" guidance (§0/§1) — it was deliberately changed to match real WHMCS.

- **Theme CSS** `frontend/src/lib/styles/twentyone.css` — all rules scoped under `.ca` (tokens on that class;
  primary `#336699`, success `#218739`, body `#F1F1F1`, radius `3.5px`, container `1140px`, Open Sans; mobile-first
  576/768/992/1200). It re-tints the reused shared components by overriding `--color-*`/`font-family` inside `.ca`
  only — **do not edit the shared components' internals** (admin relies on their untouched Tailwind globals).
- **Shell** `frontend/src/lib/components/ca/*` + `nav.ts`: `CaShell` (loads Open Sans + Font Awesome via CDN,
  wraps content in `.ca`, variants portal/auth/store/client), `CaHeader`, `CaNav`, `CaBreadcrumb`, `CaFooter`,
  `CaLanguageModal`, `CaPanel`, `CaProductCard`, `CaTile`, `CaStoreSidebar`, `CaOrderSummary`, `CaDomainHero`, `CaPrice`.
- **Keep the E2E contract**: reuse `FormField` (`id="field-<name>"`), `DataTable` (real `<table>`), `StatusBadge`
  (word text), `Modal`/`ConfirmDialog` (`role="dialog"`, confirm last), `Toast`, `MoneyText`, `DateText`; preserve all
  `data-testid`s/form `action`s/input `name`s and success-toast copy. Money: `MoneyText` where already used; `CaPrice`
  (`Rp… IDR`) for new store/hero prices.
- **i18n**: new client strings in `messages/portal.ts` (`portal.*`); admin-portal strings in `messages/fe-admin-portal.ts`
  (`adminPortal.*`). Never edit `id.ts`/`en.ts`.
- **New public routes**: `/` = portal Home (anon; authed users redirect to `/dashboard`/`/admin`), `/announcements`
  (+`/[slug]`), `/knowledgebase` (+`/category/[slug]`, `/article/[slug]`, `?search=`), `/network-status`, `/contact`.
  Backed by the announcements/knowledgebase/networkstatus/contact API groups (CONTRACTS §9). Admin management for
  these lives under `(admin)/admin/{announcements,knowledgebase,network-status}` in the `hp-*` theme.
