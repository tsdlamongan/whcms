# WHCMS — Backend Module Guide

> Binding reference for every backend module. Read together with docs/CONTRACTS.md, docs/STACK.md, and the code on disk:
> `backend/internal/ports/ports.go`, `backend/internal/domain/`, `backend/migrations/`, and the **reference
> implementation** (settings module: `internal/repository/settings_repo.go`, `internal/service/settings/`,
> `internal/transport/http/settings_handler.go`).

## 0. Layout rule for modules (IMPORTANT — differs from settings reference location)

Each module lives in **its own feature package** so modules stay independently buildable and testable:

```
backend/internal/modules/<mod>/          package <mod>
    service.go        use-cases (constructor New(deps Deps) *Service; Deps struct of interfaces)
    repo.go           pgx repository impl for this module's tables (uses platform/db Querier(ctx) pattern)
    handler.go        Fiber v3 handlers + RegisterRoutes(r fiber.Router, s *Service, mw Middlewares)
    dto.go            request/response DTOs with validate tags
    *_test.go         service tests (mock ports), handler tests (fiber app.Test + fake service iface), repo tests (real local PG, auto-skip on conn failure)
```

- Import ONLY: stdlib, domain, ports, ports/mocks, jobs, pkg/apperr, pkg/httpx, platform/* — **never another module package**.
- Cross-module calls go through **interfaces declared in ports.go** (already curated) or narrow consumer-side
  interfaces declared in YOUR package with the EXACT signatures pinned in §2 below (they are satisfied implicitly at wiring time).
- Do NOT edit casually: ports/, domain/, migrations/, platform/, pkg/, other modules' dirs, cmd/, transport/http (except reading middleware helpers). If something you need is missing there, extend it in a focused, contract-conscious change (new ports method + mock, forward-only migration) rather than working around it inside the module.
- Fast inner loop: `go build ./internal/modules/<mod>/... && go vet ./internal/modules/<mod>/... && go test ./internal/modules/<mod>/...`; the real gate before "done" is the full `make test-backend` from the repo root.
- RegisterRoutes signature convention (copy from settings reference but adapt): handlers receive service interface, middleware bundle struct `Middlewares{RequireAuth, RequireRole, RequirePermission, RequireClient, RateLimit fiber.Handler-ish}` — mirror whatever `internal/transport/http/middleware.go` exposes.

## 1. Scope decisions (final — implement exactly, do not re-litigate)

- IDR-only everywhere; reject non-IDR input; amounts int64 whole rupiah.
- Coupons: applied at order time; if `recurring=true`, `services.coupon_id` keeps it and renewal invoices re-apply the discount.
- Deposit invoices (item related_type `deposit`) cannot be paid with credit.
- Refund = manual mark-as-refunded (invoice + its success transaction + audit); no gateway call.
- Proforma mode = "PROFORMA" watermark/title on unpaid invoice PDF when `billing.proforma_enabled`; numbering unchanged.
- Upgrade/downgrade: prorated diff invoice (`related_type=service_upgrade`, meta in `services.pending_upgrade` JSONB); applied via provision change-package after payment. Downgrade credit floor 0 (no negative invoices; excess → client credit).
- Sub-accounts: `client_contacts` CRUD with permissions JSONB; **no separate login** (documented limitation).
- Fraud: settings `fraud.max_orders_per_day` (default 10) + `fraud.email_domain_blacklist` (array); violations → order status `fraud`, invoice cancelled.
- Skipped (documented): DNSSEC, child NS, domain forwarding, credit notes, email piping, multi-currency math.
- 2FA TOTP available to all roles; required-if-enabled at login (`totp_code` field).
- Impersonation: admin-only, returns client tokens, audit-logged.
- Late fee: once per invoice (guard: existing `late_fee` item), amount from settings (`billing.late_fee_amount` fixed IDR).
- Every state transition on invoice/order/service/domain MUST go through domain state-machine check; invalid → apperr CONFLICT.
- Every client-facing query filters by client_id from `httpx.Identity(c)`; admins pass explicit ids. 404 (not 403) for other clients' resources.
- Notifications: modules call `ports.NotificationSender` (never mailer directly). Template keys exist in seed migration.
- All external adapter calls happen in worker jobs or explicitly-async paths; HTTP request path may call: Duitku (methods/inquiry/check), RDash availability check, server test-connection, SSO. Everything else via Enqueuer.

## 2. Cross-module signatures (EXACT — consumer interfaces and provider methods must match verbatim)

```go
// Providers (module → implements method on *Service)          // Consumers
billing:   CreateInvoice(ctx context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error)   // orders, clients(deposit), domains(renew), provisioning(upgrade)
billing:   ProcessPaid(ctx context.Context, invoiceID int64) error                                     // payments
payments:  ApplyPayment(ctx context.Context, invoiceID int64, tx ports.ApplyTx) error                  // billing(manual pay), payments(callback/credit/reconcile)
orders:    ActivateOrder(ctx context.Context, orderID int64) error                                     // billing.ProcessPaid
provisioning: RenewService(ctx context.Context, serviceID int64) error                                 // billing.ProcessPaid
provisioning: ApplyUpgrade(ctx context.Context, serviceID int64) error                                 // billing.ProcessPaid (service_upgrade items)
domains:   RenewDomainAfterPayment(ctx context.Context, domainID int64) error                          // billing.ProcessPaid (enqueues domain:renew)
notifications: SendTemplate(ctx context.Context, userID int64, key string, data map[string]any) error  // everyone (via ports.NotificationSender)
notifications: AlertAdmin(ctx context.Context, subject, message string) error                          // provisioning/worker failures
clients:   AddCredit(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID int64) error   // billing(deposit paid), payments(credit pay uses DeductCredit)
clients:   DeductCredit(ctx context.Context, clientID int64, amount int64, reason string, relatedInvoiceID int64) error // payments, billing(auto credit apply)

// Cron/job methods (worker consumes; (int, error) returns processed count)
billing:   GenerateRenewalInvoices(ctx) (int, error); MarkOverdue(ctx) (int, error); SendReminders(ctx) (int, error); ApplyLateFees(ctx) (int, error); GenerateInvoicePDF(ctx, invoiceID int64) error
payments:  ReconcilePending(ctx) (int, error); ReconcileOne(ctx, transactionID int64) error
provisioning: AutoSuspend(ctx) (int, error); AutoTerminate(ctx) (int, error); ProvisionCreate(ctx, serviceID int64) error; ProvisionSuspend(ctx, serviceID int64, reason string) error; ProvisionUnsuspend(ctx, serviceID int64) error; ProvisionTerminate(ctx, serviceID int64) error; ProvisionChangePackage(ctx, serviceID int64) error
domains:   RegisterDomainJob(ctx, domainID int64) error; TransferDomainJob(ctx, domainID int64) error; RenewDomainJob(ctx, domainID int64) error; SyncDomainJob(ctx, domainID int64) error; SyncAllDomains(ctx) (int, error)
notifications: DeliverEmail(ctx, emailLogID int64) error
adminops:  Housekeep(ctx) (int, error)
```

If ports.go already defines matching cross-service interfaces/structs (`InvoiceCreator`, `PaymentApplier`,
`PaidInvoiceProcessor`, `ServiceActivator`, `ServiceRenewer`, `DomainRenewer`, `NotificationSender`,
`CreateInvoiceInput`, `ApplyTx`) use those; where a narrow consumer-side interface is cleaner, declare it locally
with these exact method signatures.

## 3. Module assignments

### M-AUTH `internal/modules/auth`
FR-AUTH-001..008. Endpoints per CONTRACTS §9 auth group. Register creates users(role client)+clients rows in tx,
sends verify_email (TokenStore verify_email 24h → link `FRONTEND_URL/verify-email?token=`). Login: argon2 verify,
lockout via ports.RateLimiter (5/min → 15min lock), TOTP check when enabled, update last_login_at, audit admin
logins, returns access+refresh+user DTO. Refresh rotates. Logout revokes. Forgot/reset via TokenStore
reset_password 1h (always-200 on forgot). /auth/me GET+PATCH (profile fields + password change w/ current
password; email immutable). 2FA: setup (generate secret via pquerna/otp, store encrypted, return otpauth url +
secret), enable (verify code → twofa_enabled), disable (password+code). Impersonate: POST
/admin/clients/:id/impersonate (RequireRole admin) → tokens for that client's user, audit. Owns users repo
(Create, GetByEmail, GetByID, Update, UpdatePassword, SetTwoFA, SetVerified, staff list/create/update for M-ADMINOPS reuse via ports.UserRepo).
Verify-email required before checkout — expose on user DTO `email_verified`.

### M-CLIENTS `internal/modules/clients`
FR-CLI-001..008. Admin CRUD+search (ILIKE name/email/company, filter status, has-product; server-side pagination),
client detail aggregate (counts + recent of services/domains/invoices/tickets/transactions), status changes,
admin notes, contacts CRUD, credit: AddCredit/DeductCredit (tx + ledger + balance update, negative-balance guard),
ledger listing, deposit: POST /account/credit/deposit {amount>=10000} → CreateInvoice(deposit item, taxed=false),
CSV export (stream). Client self: profile via auth /me for identity; client-facing profile PATCH here
(/api/v1/account/profile) for address fields.

### M-CATALOG `internal/modules/catalog`
FR-PROD-001..007. Product groups CRUD, products CRUD (module binding cpanel|directadmin|none, package_name,
server_group_id, auto_setup, stock, welcome template), pricing per cycle CRUD (IDR), configurable option
groups/options/values CRUD, coupons CRUD + Validate(code, productIDs, subtotal) → discount amount. Public:
GET /products (grouped, visible only, cached via ports.Cache 10m, invalidate on write), GET /products/:slug,
GET /product-groups, POST /coupons/validate. Admin test data integrity: cannot delete group with products; soft
delete products.

### M-ORDERS `internal/modules/orders`
FR-ORD-001..007. POST /orders (auth + verified email required): input {items:[{product_id?, item_type, domain?,
cycle, options{}, domain_years?}], coupon_code?, notes?}. Validate: product visible+stock, cycle priced, domain
syntax + availability via ports.RegistrarModule.CheckAvailability for registers (skip check for transfers, require
epp later at job), configurable options valid, coupon valid → compute pricing (unit prices from product_pricing,
option deltas, setup fees; domain price from registrar check result or product pricing type domain), fraud checks,
then tx: create order(pending)+order_items, decrement stock, CreateInvoice (due = now + billing.invoice_due_days,
items linked related_type=order_item) — respond order+invoice. ActivateOrder(ctx, orderID): idempotent (skip already-activated items via order_items.service_id/domain_id backrefs);
per item: product hosting/reseller/other+module → create service row (username from domain via domain helper,
strong password encrypted, status pending, next_due_date = today+cycle, recurring_amount net incl. recurring
coupon, coupon_id if recurring) + enqueue provision:create (unless auto_setup=manual → leave pending, notify
admin); domain_register/transfer → create domains row (pending, nameservers default from settings or product) +
enqueue domain:register|transfer; other/none module → service active immediately. Order → active; notification
order activated. Admin: list/detail/set-status (accept=activate manually, cancel → cancel unpaid invoice + restore
stock, fraud → same + audit).

### M-BILLING `internal/modules/billing`
FR-BILL-001..009 + credit apply + PDF + ProcessPaid dispatcher. CreateInvoice: tx → number via counters
(`invoice:YYYYMM`), tax from settings (rate, inclusive/exclusive; taxed items only), optional auto credit apply
(ApplyCredit flag: deduct min(credit,total) via clients.DeductCredit, add credit_applied; if fully covered → mark
paid immediately through payments.ApplyPayment with gateway credit)… simpler: CreateInvoice never auto-applies;
explicit credit pay endpoint handles it. Items persisted; totals computed via domain helpers; audit. ProcessPaid
(idempotency note: caller (payments) already ensures single transition; ProcessPaid itself must tolerate re-run):
dispatch per item related_type as §2; then SendTemplate payment_received + enqueue invoice:generate_pdf.
GenerateRenewalInvoices: services active w/ next_due_date <= today+renewal_lead_days lacking open renewal invoice
(track via invoice_items related service_renewal + invoice status unpaid/overdue) → one invoice per service
(recurring_amount, recurring coupon discount, tax) + notification invoice_created; same for domains (next_due_date,
domain_renewal, registrar renewal price = recurring_amount on domains row). The per-entity body (eligibility check,
open-invoice dedupe, CreateInvoice) is factored into generateServiceRenewalInvoice/generateDomainRenewalInvoice,
each run inside one `Tx.WithinTx` that row-locks the service/domain (`GetByIDForUpdate`) before checking
`HasOpenRenewalInvoice` — the lock+check+create happen atomically, so two concurrent callers (the cron, another
cron run, or the admin action below) can never both pass the dedupe check and create two invoices for the same
service/domain. GenerateSelectedRenewalInvoices(ctx, actorUserID, in) is the admin "Invoice Selected Items" entry
point (POST /admin/invoices/generate-selected, CONTRACTS §9): fetches the given service_ids/domain_ids via
GetByIDs (no due-date window — force it now, mirroring WHMCS) and calls the same two helpers per id, so it shares
the exact dedupe guarantee above; returns `{created, skipped}` (both always `[]`, never `null`) and audits one
`invoice.generate_selected` entry. MarkOverdue: unpaid past due →
overdue + notification. SendReminders: pre-due (billing.reminder_days before due) & overdue day offsets — dedupe
via email_log check (template+invoice ref within day). ApplyLateFees: overdue invoices w/o late_fee item → add
item + recalc totals + notify. GenerateInvoicePDF: render via ports.PDFGenerator → Storage.Put
`invoices/<number>.pdf` → save pdf_object_key. Client endpoints: list/detail/pdf (presign or stream), POST
/invoices/:id/pay {method: "credit"} handled by payments (route there). Admin: list w/ filters, detail, create
manual invoice, add manual payment {amount, method} → payments.ApplyPayment(gateway manual), cancel (unpaid only),
refund (paid → refunded + tx refunded + audit + optional AddCredit refund-to-credit flag), edit due date/notes.

### M-PAYMENTS `internal/modules/payments`
FR-PAY-001..011 + PRD §8.1 exactly. Owns transactions repo. GET /payments/methods?invoice_id → validate ownership
+ unpaid → aggregate GetPaymentMethods(remaining total) across the gateway registry (Deps.Gateways/GatewayOrder;
cache 10m per amount bucket), each method tagged `gateway`. POST /invoices/:id/pay {method}: unpaid/overdue only;
routes by method — `"credit"` → PayWithCredit; `ManualMethodBankTransfer` ("bank_transfer") → the manual gateway;
anything else → Duitku (DefaultGateway) — merchantOrderID = fmt "%s-%02d" invoice_number attempt (attempt = count
existing tx for invoice +1); CreateTransaction (details from client profile, callback URL APP_BASE_URL +
/api/v1/webhooks/duitku, return URL FRONTEND_URL + /payments/return, expiry per method or default 1440); store tx
pending (raw response, `Gateway` = the routed gateway code); return {payment_url, va_number, qr_string,
bank_accounts, note, reference, amount, expires_at} (bank_accounts/note: manual gateway only). method="credit" →
PayWithCredit: tx: lock invoice, guard deposit-items, DeductCredit(total - credit_applied?) full remaining, then
ApplyPayment(gateway credit). Webhook POST /webhooks/duitku (public, form-urlencoded, rate-limited 120/min/IP, body
capped 16KB before parsing → 413): parse, find tx by
merchantOrderId (unknown → 404), VerifyCallbackSignature → 400 + integration log on fail; resultCode!=00 → mark tx
failed, 200 "OK"; else CheckTransaction (double verify) → statusCode 00 → ApplyPayment; 01 → leave pending; 02 →
mark cancelled. Always 200 "OK" for valid-signature callbacks (idempotent re-delivery → no-op OK). Both the
webhook (HandleCallback) and ReconcileOne look up a FIXED `Gateways["duitku"]` entry, never dispatching on a
transaction's own Gateway field — a manual (or any future non-polled) gateway has neither a webhook shape nor an
external status to poll. ApplyPayment (THE idempotent core, gateway-agnostic): WithinTx: InvoiceRepo.GetForUpdate;
if paid/refunded → nil; validate amount >= remaining (log mismatch, still accept if >= total-credit_applied; short
→ apperr CONFLICT); upsert tx success (merchant_order_id match or create for credit/manual), invoice →
paid(paid_at), then billing.ProcessPaid(ctx) same tx. GET /payments/return?merchantOrderId= → lookup tx → redirect
FRONTEND_URL/billing/invoices/<id>?paid=<status>. ReconcilePending: Duitku tx pending older than 5m →
ReconcileOne: CheckTransaction → 00 ApplyPayment / 02 expire (manual transactions are never auto-reconciled — no
external system to poll — see ConfirmManualTransaction below). Signature helpers tested against PRD formulas with
fixed vectors. Refund marking done by billing (owns invoice); payments exposes tx listing (admin
/admin/transactions?search&status&gateway&page&per_page — `gateway` finds pending manual rows awaiting
confirmation).

**ConfirmManualTransaction** (`POST /admin/transactions/:id/confirm`, role admin/staff, perm `payments`): settles a
`Gateway=manual, Status=pending` transaction via the SAME ApplyPayment core, keyed by that transaction's own
merchant_order_id (settles the exact pending row, never creates a new one) — 409 CONFLICT if the transaction isn't
a pending manual row. Distinct from, and doesn't touch, the pre-existing freeform `billing.AddManualPayment` /
`POST /admin/invoices/:id/payment` (records an out-of-band payment with no linked pending transaction — that flow
always creates a NEW TxSuccess row, so it can never collide with a client-initiated bank-transfer's pending row).

### M-PROVISIONING `internal/modules/provisioning`
FR-PROV-001..010, FR-SVC-001..006. Owns services+servers+server_groups repos. Server CRUD + test-connection
(module.AccountInfo or version ping via adapter — sync call), groups CRUD (strategy round_robin|least_used —
least accounts among active servers with capacity). Service lifecycle: ProvisionCreate(serviceID): status pending
required (idempotent: active → nil); pick server if nil (by product.server_group_id); adapter Create (ServerConfig
from servers row, decrypt creds); success → active + registration_date + welcome email (product template or
service_activated) + panel_meta; failure → remains pending, IntegrationLogger already recorded, asynq retry (return
err), final failure → AlertAdmin (detect via asynq MaxRetry in worker wrapper — expose method
NotifyProvisionFailure(ctx, serviceID, taskType, errMsg) for worker to call on last-retry). ProvisionSuspend/
Unsuspend/Terminate/ChangePackage similar with state machine transitions (+suspend_reason, terminated_at).
RenewService: next_due_date += cycle (from current next_due_date), if suspended → enqueue provision:unsuspend,
notification renew. ApplyUpgrade: read pending_upgrade {product_id, cycle, price, specs?} → update product_id/
cycle/recurring_amount, rewrite panel_meta.chosen_specs from the pending specs (cleared when the target is a
flat product) + enqueue provision:change_package, clear pending. UpgradeService(client initiation): active
only, target product same module; custom-spec (configurable) targets take a specs[] input ({key,qty,unlimited},
unchosen knobs default) validated/priced with the SAME rules as order checkout (resolveUpgradeSpecs mirrors
orders.priceSpec) so the target price = base cycle price + per-spec charges; same product+cycle with different
specs = a resize (identical specs rejected). Compute prorate: unused = recurring_amount * days_left/period_days;
charge = target_price * (days_left/period)… use domain.Prorate helper; diff>0 → invoice service_upgrade item
(description carries the spec summary) + store pending_upgrade (incl. resolved specs); diff<=0 → apply
immediately (chosen_specs rewritten in the same tx) + AddCredit(excess). ProvisionChangePackage: flat products
push product.package_name; configurable products rebuild the dynamic package from panel_meta.chosen_specs
(EnsurePackage, same as ProvisionCreate), update panel_meta package_name/limits, and delete the old dynamic
package once no sibling service on the server references it (CountByServerAndPackage, same rule as terminate). Client cancel: immediate → enqueue terminate +
cancel open renewal invoices; end_of_term → flag (services.notes/cancel_at_period_end bool in panel_meta JSON) —
honored by AutoTerminate/renewal generation. AutoSuspend: active services whose renewal invoice overdue past
automation.suspend_after_days → enqueue suspend (reason "Overdue on payment"). AutoTerminate: suspended >
terminate_after_days (or cancel_at_period_end past due) → enqueue terminate. ChangePassword (client+admin):
strong-validate, adapter call sync, re-encrypt store. SSO: adapter SSOURL sync. Client endpoints per CONTRACTS §9.
Admin actions endpoints sync-call adapter directly (faster feedback) but share the same service methods; audit all.
Configurable/dynamic products ("customer sets their own specs"): buildPackageSpec resolves
services.panel_meta.chosen_specs into a ports.PackageSpec whose Name is
domain.DynamicPackageName(prefix, limits, toggles domain.PackageToggles) — a deterministic hash of
the *resolved limits and package toggles* (FeatureList, ShellAccess, CGIAccess, TemplatePackage —
all copied from product), not the service ID — so two services landing on identical limits AND
toggles on the same server share one EnsurePackage-managed package instead of each getting their
own (EnsurePackage's addpkg→editpkg/create→modify idempotency absorbs the collision); a different
toggle combination always gets its own package name even with identical limits. cpanel's
FeatureList is a live reference (admin manages the named WHM Feature List directly in WHM; editing
it there updates every package pointing to it) — ShellAccess/CGIAccess (hasshell/cgi) are sent as
flat addpkg/editpkg params. DirectAdmin has no feature-list-style object, so TemplatePackage
instead names an existing DA package whose full raw fields EnsurePackage reads
(CMD_API_PACKAGES_USER?package=<name>) once and merges as the base underneath its own computed
fields (cgi, ssh — always win) before create/modify — a point-in-time clone, not a live reference:
editing the template package later does not retroactively update packages already cloned from it.
The 8 canonical ProvisionKey resource-limit fields (quota, bandwidth, vdomains, nsubdomains,
domainptr, nemails, mysql, ftp) and their u<field> companions are the one exception: this app fully
owns them, so they are excluded from the template base entirely (daManagedFields) — present in the
resolved Limits → that value wins; absent (the admin hasn't modeled that resource as a Dynamic Spec
on this product) → omitted, exactly as for a product with no TemplatePackage at all — never
silently inherited, so a template with every field checked "Unlimited" can't leak "Unlimited" onto
an unmodeled resource. u<field> is a real checkbox on DirectAdmin's side keyed on the field's mere
presence in the request, not its string value — confirmed live: sending u<field>=OFF to explicitly
clear a stale unlimited flag was itself read as "checked" and made even a modeled resource with a
correct concrete quota come out unlimited. daPackageParams already gets this right (sets
u<field>=ON only when truly unlimited, otherwise omits the key) — never send "OFF" as a value here,
only ON or omitted. ProvisionTerminate only calls DeletePackage once
ServiceRepo.CountByServerAndPackage confirms no other non-terminal service on that server still
references the name, so a shared package survives any single owner's termination.
Log service_actions into audit_logs (action prefix "service.").

### M-DOMAINS `internal/modules/domains`
FR-DOM-001..010. Owns domains+registrars repos. POST /domains/check (public, rate-limited 20/min/IP): syntax
validate (idn ok), RegistrarModule.CheckAvailability. RegisterDomainJob: domains row pending → registrar.Register
(contact from client profile, ns from input/default) → active + dates + notification domain_registered; failure
retry/alert as provisioning. Transfer similar (epp from epp_code_enc). RenewDomainAfterPayment → enqueue
domain:renew; RenewDomainJob: registrar.Renew(1y default per cycle) → expiry_date/next_due_date advance +
notification. Client: list/detail, PATCH nameservers (validate 2-4 hosts) → registrar.UpdateNameservers + store,
GET/PUT dns (records CRUD via registrar), GET epp (decrypt or registrar.GetEPPCode), POST renew → billing
renewal invoice now (years=1), PATCH auto_renew + contact update. Admin: list/detail/sync (SyncDomainJob:
registrar.SyncDomain → update status/expiry/ns), force-renew, registrar config endpoints (get/update rdash row
config JSONB + test via account profile call). SyncAllDomains: active domains → SyncDomainJob each (bounded, log).
Auto-renew=false + expired → status expired (sync). Renewal invoice generation handled by M-BILLING using
domains.next_due_date (billing reads DomainRepo).

### M-TICKETS `internal/modules/tickets`
FR-TIC-001..006. Departments CRUD (admin) + public list. Client: create (subject, dept, priority, message,
attachments multipart → validate ext/size per settings → Storage.Put `tickets/<ticket>/<uuid>-<filename>`), reply,
close, list/detail (no internal notes). Ticket number TKT counter. Status transitions: client reply → customer_reply;
staff reply → answered; close → closed (either side). Staff/admin: list (filters status/dept/assigned), assign,
reply (is_internal flag), status set, detail incl. internal notes. Notifications: ticket_opened (to dept email +
client ack), ticket_replied (to client on staff reply; to assigned staff/dept on client reply). Attachment download:
GET /tickets/:id/attachments/:idx → presigned URL redirect (ownership checked).

### M-NOTIFICATIONS `internal/modules/notifications`
FR-NOTIF-001..005. Implements NotificationSender: SendTemplate(userID, key, data): load user+client (locale
preference default id), load template (key, locale fallback en→id), render subject/body via text/template with
data + global vars (company name/logo, frontend url), insert email_log queued, enqueue mail:send{email_log_id}.
DeliverEmail(emailLogID): load, Mailer.Send, mark sent/failed(+error). AlertAdmin: send to ADMIN_ALERT_EMAIL via
template admin_alert. Admin endpoints: email templates CRUD + POST preview {key, locale, sample_data} → rendered
HTML; email log list + retry endpoint (re-enqueue failed). Template variables documented in dto/docstring.

### M-ADMINOPS `internal/modules/adminops`
FR-ADM-001..005 + reports + logs + staff. GET /admin/dashboard: KPIs (income today/month MTD, orders today,
unpaid+overdue counts&sums, open tickets, active services, pending provisioning, recent activity) via dedicated
aggregate SQL (own dashboard queries in repo.go; cache 60s). Reports: revenue (paid transactions grouped
day/month between from/to; totals + gateway split), orders report, services report (by product/status), export
CSV variants. Staff mgmt: list/create/update staff users (role staff + permissions JSONB, uses ports.UserRepo),
deactivate. Logs: audit list (filter actor/entity/date), email log list (reuse), integration logs list (filter
provider/success). Housekeep: delete integration_logs & email_log older than 90d, audit older than 365d (counts).
Gateways config endpoint: GET/PUT /admin/gateways (duitku row: merchant_code, mode — from settings/gateways table;
secrets remain env — expose masked presence only).

### M-WORKER `internal/worker` (package worker)
RegisterHandlers(mux *asynq.ServeMux, d Deps) binding every jobs.* task type → Deps method (Deps = struct of
narrow interfaces w/ §2 signatures). Payload unmarshal + slog + on-final-retry AlertAdmin hook (asynq
`ResultWriter`/retry-count check). Schedules() returning []PeriodicTask{cronspec, task}: invoices_generate
"0 1 * * *", reminders "30 1 * * *", late_fees "0 2 * * *", overdue-mark "15 1 * * *", auto_suspend "0 3 * * *",
auto_terminate "30 3 * * *", payment_reconcile "*/10 * * * *", domain_sync "0 4 * * *", housekeeping "0 5 * * 0".
Each cron handler wraps in Locker.WithLock("cron:<name>", 10m). Unit tests with fake Deps verifying dispatch +
lock usage (fake Locker).

### Adapters `internal/integration/<name>` (packages duitku, manual, cpanel, directadmin, rdash)
Implement ports interfaces exactly; constructor takes config struct + *http.Client + ports.IntegrationLogger +
ports.Clock. Timeouts 30s, retry 2x on 5xx/network (idempotent GETs only — POSTs no auto-retry except explicitly
safe), circuit-breaker-lite (consecutive-failure counter → EXTERNAL error fast-fail 60s). Redact per CONTRACTS §3.
- duitku: 3 endpoints + signature helpers (MD5 inquiry/callback/check, SHA256 getmethod, datetime format
  `2006-01-02 15:04:05`); amount decimals: none (int). Map statusCode/resultCode per PRD §8.1. Live merchant
  code/mode-or-overridden base URL/API key resolved fresh per call (no restart) — see resolveDuitkuCredentials in
  WIRING.md.
- manual: no HTTP calls at all — `GetPaymentMethods` returns one static channel (only when enabled + ≥1 bank
  account configured), `CreateTransaction` returns the configured bank accounts + instructions instead of a
  paymentUrl/vaNumber/qrString, `CheckTransaction`/`VerifyCallbackSignature` are unsupported/false (never invoked —
  no webhook route, no reconciliation entry for this gateway). Config resolved live from settings
  (`gateway.manual.config`, adminops), same live/no-restart pattern as duitku/rdash.
- cpanel: WHM API 1 GET/POST w/ `Authorization: whm user:token`, TLS-skip-verify option per server config
  (use_ssl), map metadata.result!=1 → errors; createacct password rules; SSOURL via create_user_session.
- directadmin: Basic auth, legacy URL-encoded parsing (error=1&text=...) + JSON detection; commands per CONTRACTS §12 mock.
- rdash: Basic reseller_id:api_key, JSON envelope {code,message,data}, endpoints per mockserver contract §12.4;
  map 404/409; price fields int64 IDR.
Tests: httptest servers replaying real-shaped fixtures incl. error/edge cases + signature vectors; >95% coverage
(pure adapters). ALSO write a thin compile-time check `var _ ports.PaymentGateway = (*Client)(nil)` etc.

**Payment gateway registry** (payments module, MODULES.md §M-PAYMENTS below): `payments.Deps.Gateways
map[string]ports.PaymentGateway` keyed by `domain.Gateway` code, `GatewayOrder []string` (aggregation/display
order), `DefaultGateway string` ("duitku"). `PayInvoice` routes by method: `"credit"` → credit balance,
`payments.ManualMethodBankTransfer` ("bank_transfer") → the `"manual"` entry, anything else → `DefaultGateway`
(preserves raw Duitku channel codes like "VA"/"OV" unprefixed — no namespacing convention needed, PRD §8.1's
channel-code space cannot collide with "bank_transfer"). `GetPaymentMethods` aggregates every registered
gateway's methods, tagging each with its `Gateway` code; the default gateway erroring propagates, a non-default
gateway erroring or returning empty is silently omitted. `HandleCallback`/`ReconcileOne` stay hardcoded to a
fixed `Gateways["duitku"]` lookup (never dispatch on a transaction's own `Gateway` field) since webhook
shape/reconciliation are inherently Duitku-specific.

## 4. Definition of done per module

1. Own packages: build + vet + tests pass (`go test ./internal/modules/<mod>/... -race`), then the full
   `make test-backend` gate from the repo root.
2. Service logic coverage ≥90% for the module's packages (`go test -cover`); handlers+repo covered with
   fakes/integration tests.
3. RegisterRoutes exported + a package doc comment listing the route table.
4. Cross-module interfaces the module consumes are declared consumer-side (or in ports) and wired in
   `internal/composition/build.go`; docs/WIRING.md §2 updated if the Deps shape changed.
