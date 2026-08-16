# WHCMS — Architecture Contracts

> **This document is the binding architecture contract for the codebase.**
> Read it fully before writing code. If a detail here conflicts with your intuition, follow this document.
> For the finest-grained contracts, the code itself is authoritative: `backend/internal/ports/ports.go`,
> `backend/internal/domain/*.go`, `backend/migrations/*.sql`, and `docs/STACK.md` — read them from disk.

Product: **WHCMS** — WHMCS-clone billing & automation platform for hosting providers (docs/PRD.md).
Working decisions (final, do not re-litigate): RustFS (S3) for ALL file storage; SvelteKit SSR (adapter-node);
mock-first external integrations; **IDR-only** money as `int64` whole rupiah (currency column kept for future).

---

## 1. Repository layout (ownership map)

```
/backend                    Go module "github.com/tsdlamongan/whcms/backend"
  /cmd/api                  HTTP server entrypoint; builds the app via composition and mounts all module routes
  /cmd/worker               asynq worker + scheduler entrypoint
  /cmd/seed                 dev/E2E seeder
  /cmd/migrate              standalone migration runner (the API also migrates on startup)
  /internal/domain          entities, enums, state machines, money, business rules (foundation-owned)
  /internal/ports           ALL interfaces: repos, integrations, infra (foundation-owned; modules read-only)
  /internal/jobs            asynq task type constants + payload structs (foundation-owned)
  /internal/modules/<mod>   vertical slice per module: dto.go, handler.go, service.go, repo.go (+ tests)
  /internal/repository      cross-cutting repos only: audit, emaillog, integrationlog, settings
  /internal/service         cross-cutting services only: authtoken, captcha, settings
  /internal/integration/duitku|cpanel|directadmin|rdash|manual|turnstile   external adapters
  /internal/transport/http  middleware + shared handlers (settings, public config); no router file —
                            each module handler exposes RegisterRoutes(r) and cmd/api/main.go mounts them
  /internal/composition     build.go — the composition root wiring repos/services/adapters together
  /internal/worker          asynq worker runtime: handlers, cron/periodic schedules
  /internal/installer       pre-config bootstrap phase behind the /install wizard (§15)
  /internal/platform        config, db, redis, logger, storage(s3), crypto, mailer, queue, lock, token, …
  /pkg/apperr               error codes/types (foundation-owned)
  /pkg/httpx                response envelope, pagination helpers (foundation-owned)
  /migrations               golang-migrate SQL files (forward-only)
/frontend                   SvelteKit 2 + Svelte 5 (runes) + TS + Tailwind v4, adapter-node
/mockserver                 separate Go module "github.com/tsdlamongan/whcms/mockserver": mock Duitku/WHM/DA/RDash/mail-capture
/deploy                     docker-compose.yml, docker-compose.prod.yml, Dockerfiles, env-examples
/docs                       CONTRACTS.md (this), STACK.md, MODULES.md, WIRING.md, FRONTEND.md, E2E.md,
                            RECONCILE.md, PRD.md, DESIGN.md, DESIGN-FRONT.md
```

**Rule: feature work lives in its module's vertical slice.** Foundation-owned files (`ports.go`
signatures, applied migrations, domain enums) are extended, never rewritten, and shared files
(`cmd/api/main.go`, `app.css`, `+layout.svelte`) change only for genuine cross-cutting needs.

## 2. Stack (pinned by foundation in docs/STACK.md with exact versions)

- Go ≥1.26, **Fiber v3** (`github.com/gofiber/fiber/v3`) — note v3 API: `fiber.Ctx` is an interface, `c.Bind().Body(&x)`, `c.JSON`, middleware `func(c fiber.Ctx) error`.
- `jackc/pgx/v5` + `pgxpool` (NO ORM; hand-written SQL, parameterized only)
- `redis/go-redis/v9`; key prefix **`whmcs:`**
- `hibiken/asynq` (queue + periodic scheduler), Redis-backed
- `golang-jwt/jwt/v5` (HS256), `golang.org/x/crypto/argon2` (Argon2id PHC strings), `pquerna/otp` (TOTP)
- `minio/minio-go/v7` for RustFS S3
- `golang-migrate/migrate/v4` — API runs migrations on startup (idempotent)
- `go-playground/validator/v10`, `stretchr/testify`, `google/uuid` (public tokens only)
- PDF: `github.com/go-pdf/fpdf` (fallback `jung-kurt/gofpdf`)
- SMTP: `github.com/wneessen/go-mail`
- Logging: stdlib `log/slog` JSON, with `request_id` attr
- Frontend: SvelteKit 2, Svelte 5 runes, TypeScript strict, Tailwind CSS v4, `@sveltejs/adapter-node`, Playwright

## 3. Conventions

- **IDs**: `BIGINT GENERATED ALWAYS AS IDENTITY` primary keys; Go `int64`.
- **Money**: `int64` whole IDR. DB `BIGINT`. Field names `amount`, `subtotal`, `tax_total`, `total`. Currency `TEXT NOT NULL DEFAULT 'IDR'`.
- **Time**: `TIMESTAMPTZ`, Go `time.Time` UTC internally; display TZ Asia/Jakarta is frontend concern.
- **Soft delete** only on `clients`, `products`, `product_groups` (`deleted_at TIMESTAMPTZ NULL`).
- **Context**: every service/repo method takes `ctx context.Context` first.
- **Errors**: return `*apperr.Error` from services. Codes: `VALIDATION`, `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`, `CONFLICT`, `RATE_LIMITED`, `PAYMENT_REQUIRED`, `EXTERNAL`, `INTERNAL`. Constructor `apperr.New(code, message)`, wrap with `.WithDetails(...)`, `errors.Is/As` compatible. HTTP mapping lives in one Fiber error handler (transport).
- **HTTP envelope** (pkg/httpx): success `{"data": ..., "meta": {...}|null, "error": null}`; error `{"data": null, "error": {"code": "...", "message": "...", "details": [...]}}`. Pagination query `?page=&per_page=` (default 1/10, max 1000; frontend page-size selector offers 10/25/50/100/500/1000), meta `{page, per_page, total}`.
- **Auth**: `Authorization: Bearer <access JWT>` (TTL 15m, claims: `sub`(user_id), `role`, `cid`(client_id, 0 for staff/admin), `jti`). Refresh: opaque 43-char random token in Redis `whmcs:refresh:<token>` → JSON{user_id, jti}, TTL 30d, rotated on use, revocable. Middleware (transport): `RequireAuth`, `RequireRole("admin","staff")`, `RequireClient` (must have client profile). Handlers read identity via `httpx.Identity(c)` helper (set by middleware in `c.Locals`).
- **Validation**: DTO structs with `validate:` tags; run via shared `platform/validate.Struct(dto)`; return VALIDATION error with per-field details.
- **RBAC**: role `admin` = everything. `staff` = per-module permissions JSONB on users row: `{"clients":true,"billing":true,"support":true,"products":false,...}`; check via middleware `RequirePermission("billing")`. `client` = own resources only (every client query filters by client_id from token).
- **Rate limiting**: Redis fixed-window helper `platform/ratelimit`; login 5/min/IP+email then 15-min lockout; public endpoints 60/min/IP.
- **Audit**: services call `ports.AuditLogger.Log(ctx, actorUserID, action, entity, entityID, before, after any)` for sensitive actions.
- **Integration logging**: adapters log each external call via `ports.IntegrationLogger` (provider, endpoint, status, latency, redacted request/response JSON). Redact keys: `apiKey, api_key, password, passwd, token, signature, authorization`.
- **Idempotent payment activation**: invoice row lock (`SELECT ... FOR UPDATE`) + partial unique index on successful transactions per invoice + status check before transition. Never trust callback alone — always confirm via Check Transaction.
- **Tests**: table-driven, testify. Services tested with hand-written fakes/mocks of ports (shared mocks live in `internal/ports/mocks`; add a missing mock THERE ONLY, named `Mock<Interface>`). Adapters tested with `httptest.Server`. Repos get `//go:build integration`-tagged tests against real PG (skipped in unit gate). Handler tests use Fiber app + mocked service interfaces. **Every exported function needs coverage; target >90% total.**

## 4. Domain enums (exact string values — DB CHECK constraints match)

- user role: `admin|staff|client`; user status: `active|inactive`
- client status: `active|inactive|closed`
- product type: `shared_hosting|reseller_hosting|domain|other`; module: `cpanel|directadmin|none`
- auto_setup: `on_payment|on_order|manual`
- billing cycle: `one_time|monthly|quarterly|semiannually|annually|biennially`
- order status: `pending|active|fraud|cancelled`
- invoice status: `draft|unpaid|paid|overdue|cancelled|refunded`
- transaction status: `pending|success|failed|expired|refunded`
- service status: `pending|active|suspended|terminated|cancelled`
- domain status: `pending|active|pending_transfer|expired|cancelled`
- ticket status: `open|answered|customer_reply|on_hold|closed`; priority: `low|medium|high`
- ticket dept is a table; coupon type: `percentage|fixed`
- job/email statuses etc. defined by foundation in domain package.

State machines (domain package exposes `CanTransition(from,to)` per entity + typed transition funcs):
- invoice: draft→unpaid; unpaid→paid|cancelled|overdue; overdue→paid|cancelled; paid→refunded
- order: pending→active|fraud|cancelled
- service: pending→active|cancelled; active→suspended|terminated; suspended→active|terminated
- domain: pending→active|cancelled; active→expired; pending_transfer→active|cancelled; expired→active(renew)

## 5. Database schema (foundation writes exact DDL in /backend/migrations)

Tables (all have `id`, `created_at`, `updated_at` unless noted): `users` (email unique, password_hash, role, status, permissions JSONB, twofa_secret_enc, twofa_enabled, email_verified_at, last_login_at), `clients` (user_id FK unique, first_name, last_name, company, address1, address2, city, state, postcode, country default 'ID', phone, currency, credit_balance BIGINT default 0, status, notes_admin, deleted_at), `client_contacts`, `product_groups` (name, slug unique, sort, hidden, deleted_at), `products` (group_id, name, slug unique, description, type, module, server_group_id NULL, package_name, auto_setup, configurable, shell_access, cgi_access, feature_list, template_package, stock_enabled, stock_qty, hidden, sort, welcome_email_template, deleted_at) — shell_access/cgi_access/feature_list/template_package are only read when configurable is true (see §12 EnsurePackage), `product_pricing` (product_id, cycle, price BIGINT, setup_fee BIGINT, currency, UNIQUE(product_id,cycle,currency)), `configurable_option_groups`/`configurable_options`/`configurable_option_values` (with per-cycle price deltas JSONB), `coupons` (code unique, type, value, applies_to JSONB, max_uses, used_count, recurring bool, expires_at, active), `orders` (order_number unique, client_id, status, subtotal/discount/tax_total/total BIGINT, coupon_id NULL, ip, notes), `order_items` (order_id, item_type `product|domain_register|domain_transfer`, product_id NULL, description, domain, cycle, unit_price, setup_fee, options JSONB, service_id NULL, domain_id NULL — back-refs filled on activation), `invoices` (invoice_number unique, client_id, status, subtotal, discount, tax_rate NUMERIC(5,2), tax_total, credit_applied, total, currency, due_date DATE, paid_at, notes, pdf_object_key), `invoice_items` (invoice_id, description, amount BIGINT, taxed bool, related_type `order_item|service_renewal|domain_renewal|late_fee|deposit|manual`, related_id NULL), `transactions` (invoice_id, gateway `duitku|credit|manual`, method_code, merchant_order_id unique NULL, gateway_reference, amount, fee BIGINT default 0, status, raw JSONB, paid_at; **partial unique index: one `success` per invoice_id**), `credit_ledger` (client_id, delta BIGINT, balance_after BIGINT, reason, related_invoice_id NULL), `server_groups` (name, strategy `round_robin|least_used`), `servers` (group_id NULL, name, module, hostname, port, username, password_enc, api_token_enc, use_ssl, nameserver1..4, max_accounts, package_prefix, ip_address, active), `services` (client_id, order_item_id NULL, product_id, server_id NULL, domain TEXT, username, password_enc, status, billing_cycle, recurring_amount BIGINT, setup_fee BIGINT, next_due_date DATE, registration_date DATE, terminated_at, suspend_reason, panel_meta JSONB, notes), `registrars` (name `rdash`, active, config JSONB non-secret, reseller_id TEXT non-secret, api_key_enc TEXT AES-256-GCM ciphertext, base_url TEXT non-secret "custom endpoint" override — all four are admin-configurable dynamic credentials/config, env vars (`RDASH_RESELLER_ID`/`RDASH_API_KEY`/`RDASH_BASE_URL`) are the fallback for whichever is blank, see §10/§11), `domains` (client_id, registrar_id, name unique-active, status, registration_date, expiry_date, next_due_date DATE, recurring_amount BIGINT, billing_cycle default 'annually', auto_renew bool, nameservers JSONB, epp_code_enc, id_protection bool, registrar_meta JSONB), `dns_records` cached optional, `ticket_departments` (name, email, active, sort), `tickets` (ticket_number unique, client_id NULL, department_id, subject, status, priority, assigned_user_id NULL, last_reply_at, closed_at), `ticket_replies` (ticket_id, user_id NULL, author_name, message TEXT, is_internal bool, attachments JSONB [{object_key,filename,size,content_type}]), `email_templates` (key, locale, subject, body_html, body_text, UNIQUE(key,locale)) — the reserved key `_layout` is the **global HTML wrapper** (branded header/footer + mobile styles) that notifications renders around every other template's body_html, exposing it as `{{.Content}}`; that slot is mandatory (save is rejected without it) and a missing/broken layout degrades to the bare body rather than blocking a send, `email_log` (user_id NULL FK → users, set by SendTemplate for a per-user delivery history, NULL for AlertAdmin/SendTestEmail; to_email, template_key, subject, status `queued|sent|failed`, error, sent_at), `settings` (key TEXT PK, value JSONB), `counters` (scope TEXT PK, value BIGINT) — invoice/order/ticket numbering via `UPDATE ... RETURNING` inside tx, `audit_logs` (user_id NULL, action, entity, entity_id, before JSONB, after JSONB, ip), `integration_logs` (provider, endpoint, method, status_code, success bool, latency_ms, request JSONB redacted, response JSONB redacted, error). Indexes: `services(next_due_date) WHERE status='active'`, `invoices(status, due_date)`, `domains(expiry_date)`, `tickets(status)`, FKs indexed, `clients` trigram/ILIKE search index on name/email helper columns.

Number formats: invoice `INV-YYYYMM-XXXXXX` (counter scope `invoice:YYYYMM`), order `ORD-YYYYMM-XXXXXX`, ticket `TKT-XXXXXX` (scope `ticket`).

## 6. Ports (interfaces — foundation writes `internal/ports/ports.go`; signatures below are binding)

Repositories (pgx impls in /internal/repository): `UserRepo`, `ClientRepo`, `ProductRepo` (incl. groups/pricing/options), `CouponRepo`, `OrderRepo`, `InvoiceRepo` (incl. items, `NextNumber(ctx, tx, scope)`, `GetByIDForUpdate` row-lock), `TransactionRepo`, `CreditRepo`, `ServiceRepo` (incl. `GetByIDForUpdate` row-lock, `GetByIDs(ctx, ids)` batch fetch — both used by billing's renewal-invoice dedupe), `ServerRepo` (incl. groups, `PickServer(ctx, groupID)` capacity-aware), `DomainRepo` (incl. `GetByIDForUpdate`, `GetByIDs` — same dedupe use), `RegistrarRepo`, `TicketRepo` (incl. departments, replies), `EmailTemplateRepo`, `EmailLogRepo`, `SettingsRepo` (typed getters: `GetString/GetInt/GetBool/GetJSON(ctx,key,default)`), `AuditRepo`, `IntegrationLogRepo`, `DashboardRepo` (aggregate queries for admin dashboard/reports). Transactions: `ports.TxManager` — `WithinTx(ctx, func(ctx context.Context) error) error`; repos detect tx from ctx (pgx tx stored in context by TxManager). Every repo method that must join a tx uses the ctx-aware pool wrapper `platform/db.Querier(ctx)`.

Integrations:
```go
type PaymentGateway interface { // duitku and manual adapters implement; payments.Deps.Gateways is a registry keyed by domain.Gateway code ("duitku","manual") — see MODULES.md
    GetPaymentMethods(ctx, amount int64) ([]PaymentMethod, error)              // {Code, Name, Image, Fee int64, Gateway string} — Gateway is set by payments.Service when aggregating, not by the adapter
    CreateTransaction(ctx, req CreateTxRequest) (*CreateTxResult, error)       // req: MerchantOrderID, Amount, Method, ProductDetails, Email, Phone, CustomerName, ReturnURL, CallbackURL, ExpiryMinutes; res: Reference, PaymentURL, VANumber, QRString, Amount, BankAccounts []BankAccount, Note (BankAccounts/Note: manual gateway only)
    CheckTransaction(ctx, merchantOrderID string) (*TxStatus, error)           // {Reference, Amount, StatusCode "00"|"01"|"02", StatusMessage} — manual gateway returns "not supported" (never polled: no external system, no webhook)
    VerifyCallbackSignature(p CallbackPayload) bool                            // MD5(merchantCode+amount+merchantOrderId+apiKey) — Duitku only; manual returns false (no webhook route registered for it)
}
type ServerModule interface { // cpanel & directadmin adapters implement
    Name() string
    Create(ctx, s ServerConfig, a CreateAccountParams) (*AccountResult, error) // params: Username, Domain, Password, Package, Email, IP
    Suspend(ctx, s ServerConfig, username, reason string) error
    Unsuspend(ctx, s ServerConfig, username string) error
    Terminate(ctx, s ServerConfig, username string) error
    ChangePackage(ctx, s ServerConfig, username, pkg string) error
    ChangePassword(ctx, s ServerConfig, username, password string) error
    EnsurePackage(ctx, s ServerConfig, spec PackageSpec) error                // creates the package if absent, else updates it to match spec (idempotent — safe to retry). spec: Name, FeatureList (cpanel only: existing WHM Feature List name, a live reference — "" defaults to WHM's "default"), ShellAccess/CGIAccess (cpanel hasshell/cgi; DA ssh/cgi), TemplatePackage (DA only: existing DA package whose raw fields are read via CMD_API_PACKAGES_USER?package=<name> and merged as the base underneath our own computed fields before create/modify — EXCEPT the 8 canonical ProvisionKey-mapped resource-limit fields and their u<field> companions, which this app fully owns and never inherits from the template: present in spec.Limits → our resolved value wins; absent → omitted entirely, same as a product with no TemplatePackage, never silently copied from the template (e.g. an all-"Unlimited" base package must not leak "Unlimited" onto a resource the admin never modeled as a Dynamic Spec) — no cpanel equivalent needed since FeatureList is already a live reference), Limits map[ProvisionKey]int64. cpanel addpkg→editpkg; DA CMD_API_MANAGE_USER_PACKAGES action=create→modify
    DeletePackage(ctx, s ServerConfig, name string) error                     // a missing package is not an error. cpanel killpkg; DA CMD_API_MANAGE_USER_PACKAGES action=delete
    ListPackages(ctx, s ServerConfig) ([]string, error)                       // read-only: packages/plans defined on the panel, for the admin product form's package-name/template-package pickers. cpanel `listpkgs` (data.pkg[].name); DA CMD_API_PACKAGES_USER (repeated list[]=<name>) — the same DA command, given `package=<name>`, instead returns that one package's full raw field set (used internally by EnsurePackage's TemplatePackage merge, not exposed as its own ServerModule method)
    AccountInfo(ctx, s ServerConfig, username string) (*AccountInfo, error)
    TestConnection(ctx, s ServerConfig) (*ServerInfo, error)                  // read-only probe, NO account needed: cpanel `version`(+best-effort gethostname, get_nameserver_config, nvget nameserver[2-4] fallback); DA CMD_API_SHOW_USERS. ServerInfo{Version,Hostname,Nameservers[]}. NS empty for resellers that inherit from root (use a root token)
    SSOURL(ctx, s ServerConfig, username string) (string, error)              // cpanel: create_user_session; DA: login key URL or error Unsupported
}
type RegistrarModule interface { // rdash adapter
    CheckAvailability(ctx, names []string) ([]DomainAvailability, error)       // {Name, Available, Premium, Price int64}
    Register(ctx, req RegisterDomainRequest) (*DomainResult, error)            // Name, Years, NS []string, Contact RegistrantContact
    Transfer(ctx, req TransferDomainRequest) (*DomainResult, error)            // + EPPCode
    Renew(ctx, name string, years int) (*DomainResult, error)
    GetNameservers(ctx, name string) ([]string, error)
    UpdateNameservers(ctx, name string, ns []string) error
    GetContact(ctx, name string) (*RegistrantContact, error)
    UpdateContact(ctx, name string, c RegistrantContact) error
    GetEPPCode(ctx, name string) (string, error)
    GetDNSRecords(ctx, name string) ([]DNSRecord, error)                       // {Type,Host,Value,TTL,Prio}
    UpdateDNSRecords(ctx, name string, recs []DNSRecord) error
    SyncDomain(ctx, name string) (*DomainSyncInfo, error)                      // {Status, ExpiryDate, NS}
    AccountInfo(ctx) (*RegistrarAccountInfo, error)                            // {AccountID, Name, Currency, Balance int64} — registrar test-connection
}
```
Infra ports: `Mailer` (`Send(ctx, msg MailMessage) error` — drivers: smtp|http|log), `Storage` (`Put(ctx, key string, r io.Reader, size int64, contentType string) error; Get(ctx,key) (io.ReadCloser, error); Delete; PresignGet(ctx, key, ttl) (string, error)`), `Encryptor` (`Encrypt/Decrypt(string) (string, error)` AES-256-GCM base64), `PasswordHasher` (Argon2id `Hash/Verify`), `TokenStore` (Redis: `Create(ctx, kind, userID, ttl) (token string, err)`, `Consume(ctx, kind, token) (userID int64, err)` — kinds: `verify_email`, `reset_password`), `Enqueuer` (`Enqueue(ctx, taskType string, payload any, opts ...JobOption) error` wraps asynq), `Locker` (`WithLock(ctx, key string, ttl, fn) error` Redis SETNX), `RateLimiter`, `Cache` (`GetJSON/SetJSON/Delete`, used for product catalog & dashboard), `AuditLogger`, `IntegrationLogger`, `Clock` (`Now() time.Time` — inject everywhere time matters), `PDFGenerator` (`InvoicePDF(ctx, inv InvoicePDFData) ([]byte, error)`).

Cross-service ports (implemented by services, consumed by others — keeps modules decoupled):
`InvoiceCreator` (used by orders/billing cron), `PaymentApplier` (`ApplyPayment(ctx, invoiceID int64, tx ApplyTx) error` — the ONE idempotent entrypoint that marks paid + enqueues activation), `ServiceActivator` (`ActivateOrder(ctx, orderID) error`), `NotificationSender` (`SendTemplate(ctx, userID int64, templateKey string, data map[string]any) error` — renders template, enqueues mail job), `RenewalInvoiceChecker` (`HasOpenRenewalInvoice(ctx, relatedType InvoiceItemRelatedType, relatedID int64) (bool, error)` — implemented by billing's invoice repo; consumed outside billing by `domains.RenewNow` so its own renewal-invoice creation shares the exact same dedupe check as `GenerateRenewalInvoices`/`GenerateSelectedRenewalInvoices`, closing what would otherwise be a duplicate-invoice gap on a double "Renew Now" click).

## 7. Queue jobs (internal/jobs — constants + payload structs, foundation writes)

Task types: `provision:create`, `provision:suspend`, `provision:unsuspend`, `provision:terminate`, `provision:change_package`, `provision:change_password`, `domain:register`, `domain:transfer`, `domain:renew`, `domain:sync`, `mail:send` (payload {EmailLogID or full message}), `invoice:generate_pdf`, `payment:reconcile_one`.
Periodic (worker scheduler, all under Redis lock, idempotent): `cron:invoices_generate` (renewals N days before due; default 14), `cron:invoice_reminders` (pre-due/due/overdue), `cron:late_fees`, `cron:auto_suspend` (overdue > N days; default 7), `cron:auto_terminate` (suspended > N days; default 21), `cron:payment_reconcile` (pending duitku tx), `cron:domain_sync`, `cron:housekeeping`.
Retry policy: max 5 retries exponential backoff; on final failure write integration/audit log + email admin. Job handlers live in worker but call the same services.

## 8. Key flows (must match PRD §8.1)

**Checkout**: cart (client-side) → `POST /api/v1/orders` {items, coupon, domain contact?} → server validates (product exists, cycle price, domain availability for registers, coupon) → creates order (pending) + invoice (unpaid, due now+3d) in one tx → returns order+invoice. New visitor: `POST /api/v1/auth/register` first (checkout page collects account+profile), must verify email before checkout (FR-AUTH-001).

**Payment**: `GET /api/v1/payments/methods?invoice_id=` → aggregates `GetPaymentMethods(total)` across the registered gateways (`payments.Deps.Gateways`/`GatewayOrder` — currently `duitku` + `manual`; cached 10m), each method tagged `gateway`. `POST /api/v1/invoices/:id/pay` {method} → routes by method: `"credit"` → credit balance; `payments.ManualMethodBankTransfer` ("bank_transfer") → the manual gateway (no HTTP call — returns the admin-configured `bank_accounts`/`note` instead of `payment_url`/`va_number`/`qr_string`, transaction created `pending`, settled only via admin confirmation below — never auto-reconciled); anything else → Duitku, with `merchantOrderID = invoice_number + "-" + attempt` (unique per attempt), transaction stored pending, response carries `payment_url`/`va_number`/`qr_string`. **Callback** `POST /api/v1/webhooks/duitku` (form-urlencoded, NO auth, rate-limited 120/min/IP, body capped at 16KB before parsing): verify signature → if resultCode "00": CheckTransaction → statusCode "00" → `PaymentApplier.ApplyPayment` (row-lock invoice; if already paid → return OK no-op; else mark tx success, invoice paid, apply credit ledger if overpay ignore, enqueue `order activation` / renewal processing, enqueue pdf + receipt email). Return 200 body "OK" (even for already-processed; 400 for bad signature; 413 for an oversized body). **Return URL** `GET /payments/return?merchantOrderId=&resultCode=` → frontend page polls invoice status. **Renewal payment paid** → service next_due_date += cycle, if suspended → enqueue unsuspend. **Credit payment**: `POST /invoices/:id/pay {method:"credit"}` — deduct client credit atomically, same ApplyPayment path (gateway "credit"). **Manual bank-transfer confirmation**: `POST /api/v1/admin/transactions/:id/confirm` (role admin/staff, perm `payments`) — settles a `gateway=manual, status=pending` transaction via the same `ApplyPayment` core, keyed by its own `merchant_order_id` (409 if the transaction isn't a pending manual row); distinct from the pre-existing freeform `POST /admin/invoices/:id/payment` (billing module — records an out-of-band payment not tied to any pending transaction). `GET /api/v1/admin/transactions` additionally accepts `?gateway=` (e.g. `manual`) to find pending bank-transfer rows awaiting confirmation.

**Activation** (`ServiceActivator.ActivateOrder`): for each order_item: product w/ module → create `services` row (username generated from domain, password random strong, encrypted) + enqueue `provision:create`; domain_register → create `domains` row pending + enqueue `domain:register`. Order → active. auto_setup=manual → service stays pending, admin triggers. Provision job: pick server (group strategy), call module.Create, on success service→active + welcome email; on permanent failure keep pending + alert admin (invoice/payment status NEVER reverted).

**Suspension cron**: invoices overdue (status unpaid, due_date < today-N) → mark overdue (+late fee once if enabled) ; services with overdue renewal invoice past threshold → enqueue suspend. Termination similar from suspended_at.

## 9. HTTP API surface (path prefix /api/v1; each module mounts its own group)

- auth: `POST /auth/register`, `/auth/login` (returns access+refresh+user), `/auth/refresh`, `/auth/logout`, `/auth/verify-email` {token}, `/auth/resend-verification`, `/auth/forgot-password`, `/auth/reset-password`, `GET /auth/me`, `PATCH /auth/me` (profile+password), `POST /auth/2fa/setup|enable|disable`, login supports `{totp_code}` when enabled.
- catalog (public): `GET /products` (grouped catalog; configurable products embed their spec knobs + per-cycle pricing as `products[].specs` so pickers — e.g. the client-area upgrade modal — can configure without a per-product detail fetch), `GET /products/:slug`, `GET /product-groups`, `POST /domains/check` {names[]}, `POST /coupons/validate`.
- orders (client): `POST /orders`, `GET /orders`, `GET /orders/:id`.
- invoices (client): `GET /invoices`, `GET /invoices/:id`, `GET /invoices/:id/pdf` (presigned or stream), `POST /invoices/:id/pay`.
- payments: `GET /payments/methods?invoice_id=` (the Duitku adapter drops non-QRIS channels when the remaining amount is below Duitku's Rp10.000 per-transaction floor — only QRIS `SP/LQ/NQ` accepts micro-payments, so small invoices like prorated upgrade diffs never offer a channel that would be rejected upstream; gateway errors surface Duitku's own reason, e.g. `duitku error: Minimum Payment 10000 IDR`, never a bare "duitku error"), `POST /webhooks/duitku`, `GET /payments/return`; admin: `GET /admin/transactions?search&status&gateway&page&per_page`, `POST /admin/transactions/:id/confirm`; gateways: `GET/PUT /admin/gateways` (duitku), `PUT /admin/gateways/manual`.
- services (client): `GET /services`, `GET /services/:id`, `POST /services/:id/change-password`, `GET /services/:id/sso`, `POST /services/:id/cancel` {mode: immediate|end_of_term}, `POST /services/:id/upgrade` {product_id, cycle, specs?: [{key,qty,unlimited}]} — prorated product/cycle change; `specs` is required knowledge for custom-spec (configurable) targets (unchosen knobs fall back to their default qty, validated/priced server-side exactly like checkout; same product+cycle with different specs = a resize). An upgrade (positive prorated diff) creates a diff invoice (`invoice_items.related_type=service_upgrade`) and stores the pending change (incl. resolved specs) in `services.pending_upgrade` until that invoice is paid — then billing's ProcessPaid → ApplyUpgrade rebinds product/cycle/recurring_amount, rewrites `panel_meta.chosen_specs`, and enqueues `provision:change_package` (which for configurable products rebuilds the dynamic panel package via EnsurePackage and deletes the old dynamic package once no sibling service references it). A downgrade/zero diff applies immediately and credits the excess.
- domains (client): `GET /domains`, `GET /domains/:id`, `PATCH /domains/:id/nameservers`, `GET|PUT /domains/:id/dns`, `GET /domains/:id/epp`, `POST /domains/:id/renew` (creates renewal invoice), `PATCH /domains/:id` (auto_renew, contact).
- tickets (client): `GET|POST /tickets`, `GET /tickets/:id`, `POST /tickets/:id/replies` (multipart attachments→S3), `POST /tickets/:id/close`. Departments: `GET /ticket-departments`.
- account (client): `GET /account/credit` ledger, `POST /account/credit/deposit` (creates deposit invoice).
- announcements (public): `GET /announcements` (published, paginated), `GET /announcements/:slug`. Admin (perm `announcements`): `GET|POST /admin/announcements`, `GET|PATCH|DELETE /admin/announcements/:id`.
- knowledgebase (public): `GET /kb/categories`, `GET /kb/categories/:slug`, `GET /kb/articles?category_id&search`, `GET /kb/articles/:slug` (increments views). Admin (perm `knowledgebase`): `GET|POST /admin/kb/categories`, `GET|PATCH|DELETE /admin/kb/categories/:id` (DELETE → CONFLICT if it has articles), `GET|POST /admin/kb/articles`, `GET|PATCH|DELETE /admin/kb/articles/:id`.
- network status (public): `GET /network-status` (active/recent), `GET /network-status/:id`. Admin (perm `network`): `GET|POST /admin/network-issues`, `GET|PATCH|DELETE /admin/network-issues/:id`.
- contact (public, no auth, rate-limited): `POST /contact` {name,email,subject,message,department_id?} → creates a guest ticket (tickets.client_id NULL + guest_name/guest_email), returns `{ticket_number}` only.
- admin (prefix /admin, RequireRole admin/staff + permission): clients CRUD+search, orders list/detail/accept/cancel/fraud, invoices CRUD+manual payment+refund+cancel, `POST /admin/invoices/generate-selected` {service_ids[], domain_ids[]} → "Invoice Selected Items" (mirrors WHMCS): force-generates a renewal invoice right now for the given services/domains, no `billing.renewal_lead_days` window check — the only gate is the same eligibility + open-invoice dedupe `GenerateRenewalInvoices` uses (via the same row-locked helpers, so this can never race the cron or itself into a duplicate invoice for the same service/domain); response `{created:[{type,id,invoice_id,invoice_number}], skipped:[{type,id,reason}]}` (both always arrays, never null), services list/detail + `POST /admin/services/:id/{create|suspend|unsuspend|terminate|change-package|change-password}` (sync call to module or enqueue) + `POST /admin/services/:id/upgrade` {product_id, cycle, specs?} — runs the SAME prorated-diff + invoice flow as the client's own `POST /services/:id/upgrade` (an admin executing a billed plan change on a client's behalf, e.g. over a support call), unlike `change-package` which swaps the product immediately with no invoice/proration, domains list/detail/sync/renew/update, tickets (list/assign/reply/internal note/status), products & groups & pricing & options CRUD, coupons CRUD, servers & server-groups CRUD + test-connection (`POST /admin/servers/test-connection` pre-save probe from form body {module,hostname,port,username,password?,api_token?,use_ssl,id?} + `POST /admin/servers/:id/test-connection` saved-server probe; both → `{ok,message,version?,hostname?,nameservers?}`) + `GET /admin/server-groups/:id/packages` (previews packages on a representative server in the group via `ServerModule.ListPackages`, for the product form's package-name picker; → `{ok,message?,packages?[]}`, OK=false — not an HTTP error — on a panel probe failure so the UI can fall back to manual entry), registrars config + test, gateways config, ticket departments CRUD, email templates CRUD + preview, settings get/put (grouped keys), staff users CRUD + permissions, reports: `GET /admin/dashboard` (KPIs), `GET /admin/reports/revenue?from&to&group_by`, `/admin/reports/orders`, `/admin/reports/services`, audit logs list, email log list, integration logs list.
- ops (no prefix): `GET /healthz` (always 200 if process up), `GET /readyz` (checks PG+Redis+S3), `GET /metrics` (optional).

## 10. Settings keys (settings table, JSONB values; SettingsRepo typed getters)

`company.name`, `company.logo_key`, `company.address`, `company.email`, `billing.tax_enabled`, `billing.tax_rate` (11), `billing.tax_inclusive` (false), `billing.invoice_due_days` (3), `billing.renewal_lead_days` (14), `billing.late_fee_enabled`, `billing.late_fee_amount`, `billing.reminder_days` ([7,3,1]), `billing.overdue_reminder_days` ([1,3,7]), `automation.suspend_after_days` (7), `automation.terminate_after_days` (21), `mail.from_name`, `mail.from_email`, `tickets.allowed_extensions`, `tickets.max_attachment_mb` (8), `security.captcha_enabled` (false), `security.captcha_provider` ("turnstile"), `security.captcha_site_key` (""), `security.require_email_verification` (true — gates `POST /orders` checkout, disable only for testing; also mirrored — non-secret — in `GET /public/config` as `security.require_email_verification` so the cart's verify-required panel and the client dashboard's unverified-email banner can reflect the gate; `SessionUser.email_verified` itself comes from `/auth/me`'s nested `user.email_verified` via the frontend hooks — never re-parse `/auth/me` as a flat object), `gateway.duitku.merchant_code`, `gateway.duitku.mode`, `gateway.duitku.base_url`, `gateway.duitku.api_key_enc` (AES-256-GCM ciphertext — the one **secret** value stored here; see below), `gateway.manual.config` (JSON `{enabled, accounts: [{bank_name,account_number,account_holder}], instructions}` for the manual bank-transfer gateway — non-secret, no env fallback, admin-configurable only; read live by `internal/integration/manual`). CAPTCHA's `TURNSTILE_SECRET_KEY` and most other module secrets are ENV-only. The two exceptions are the RDash registrar API key (`registrars.api_key_enc` column, §5) and the Duitku gateway API key (`gateway.duitku.api_key_enc` setting, above) — both admin-configurable at runtime (no restart) via `ports.Encryptor`, decrypted ad-hoc at the call site by the adapter's live-credential resolver (`internal/composition/build.go`), with the ENV var as fallback for whichever is blank. Neither the ciphertext nor the plaintext key is ever returned by any endpoint — only a `*_present`/`api_key_set` boolean. `registrars.base_url` (§5) and `gateway.duitku.base_url` are companion non-secret fields resolved the same live/no-restart way, letting an admin point the RDash/Duitku adapter at a different endpoint (e.g. the local mockserver vs the real Dewabiz/Duitku API) without a process restart — `gateway.duitku.merchant_code`/`gateway.duitku.mode` are, likewise, fully live: the running `*duitku.Client` re-resolves all three (merchant code, mode-derived-or-overridden base URL, API key) fresh on every call, not just at process boot.

## 11. Environment variables (.env.example must list all)

```
APP_ENV=development APP_PORT=8080 APP_BASE_URL=http://localhost:8080 FRONTEND_URL=http://localhost:5173
JWT_SECRET=... APP_ENCRYPTION_KEY=<base64 32B>
DATABASE_URL=postgres://root:postgres@localhost:5432/whmcs?sslmode=disable
REDIS_ADDR=localhost:6379 REDIS_PASSWORD= REDIS_DB=0
RUSTFS_ENDPOINT=http://localhost:9000 RUSTFS_ACCESS_KEY=rustfsadmin RUSTFS_SECRET_KEY=rustfsadmin RUSTFS_BUCKET=whmcs RUSTFS_USE_SSL=false
DUITKU_MERCHANT_CODE=DXXXX DUITKU_API_KEY=xxx DUITKU_ENV=sandbox DUITKU_BASE_URL=   # empty→derive from DUITKU_ENV; set to mockserver URL in dev/E2E. All four are fallbacks for the admin-configurable gateway.duitku.merchant_code/mode/base_url/api_key_enc settings (§10) — whichever setting is blank falls back to its matching env var
RDASH_RESELLER_ID= RDASH_API_KEY= RDASH_BASE_URL=https://api.dewabiz.co.id/v1   # real Dewabiz "RDash" reseller API; mockserver in dev/E2E; RESELLER_ID/API_KEY/BASE_URL are all fallbacks for the admin-configurable registrars.reseller_id/api_key_enc/base_url columns (§5) — base_url ("custom endpoint") lets an admin flip a registrar between mock and production with no restart
TURNSTILE_SECRET_KEY= TURNSTILE_VERIFY_URL=   # optional CAPTCHA (Cloudflare Turnstile); secret ENV-only; VERIFY_URL empty→Cloudflare, mockserver in dev/E2E
MAIL_DRIVER=log SMTP_HOST= SMTP_PORT=587 SMTP_USER= SMTP_PASS= MAIL_HTTP_URL=   # http driver posts JSON to MAIL_HTTP_URL (mockserver); MAIL_DRIVER=smtp REQUIRES SMTP_HOST (boot fails otherwise)
SMTP_ENCRYPTION=auto SMTP_AUTH=auto SMTP_TIMEOUT_SECONDS=20 SMTP_INSECURE_SKIP_VERIFY=false   # ENCRYPTION auto|tls|starttls|none — auto derives from the port (465→implicit TLS/SMTPS, else STARTTLS); STARTTLS is mandatory, never opportunistic, so a relay that does not offer it fails the send instead of leaking credentials. AUTH auto|plain|login|cram-md5|none — auto negotiates whatever the relay advertises (LOGIN-only servers work out of the box). SKIP_VERIFY only for self-signed relays. Verify a live relay from Admin > Settings > Mail > Send Test Email (POST /admin/email/test)
WORKER_CONCURRENCY=10 ADMIN_ALERT_EMAIL=admin@example.com ADMIN_ALERT_WEBHOOK_URL=   # optional; JSON POST alongside every admin alert email
ENV_FILE=   # optional path to a KEY=VALUE file the installation wizard writes to (§15); defaults to ./.env
```
Config loaded once into `platform/config.Config` struct (env only, `caarlos0/env` or hand-rolled), passed by value/DI. **No global state.**

`whmcs` above is the **local/dev** database name (also what `.env.example` documents). Automated E2E/test runs use a
separate database name, **`whmcs_e2e`**, on the same Postgres server/port — `scripts/e2e-up.sh`, `make test-backend`,
and every `_integration_test.go`'s `DATABASE_URL` fallback point there, so running tests never touches or resets a
developer's local manual-testing data. See `docs/E2E.md` §3.

## 12. Mockserver contract (module `whcms-mock`, port 9090)

- Duitku: implements the 3 endpoints with REAL signature validation (uses its own MERCHANT_CODE/API_KEY env, defaults DEMO/secretkey — backend dev env must match); `POST /webapi/api/merchant/v2/inquiry` returns reference `MOCKREF-<n>` + paymentUrl `http://localhost:9090/payment/<reference>`; payment page HTML shows amount + "Bayar Sekarang" button; clicking POSTs `/mock/duitku/pay/<reference>` which sends a REAL callback (form-urlencoded, correct MD5 signature) to the callbackUrl captured at inquiry; transactionStatus reflects state (00 paid, 01 pending, 02 cancelled). Also `/mock/duitku/expire/<reference>`.
- WHM: `POST|GET /json-api/createacct|suspendacct|unsuspendacct|removeacct|changepackage|passwd|accountsummary|create_user_session` (token auth header checked loosely), in-memory account store, WHM JSON result format `{metadata:{result:1,reason:...}, data:{...}}`.
- DirectAdmin: `GET|POST /CMD_API_ACCOUNT_USER|CMD_API_SELECT_USERS|CMD_API_MODIFY_USER|CMD_API_USER_PASSWD|CMD_API_SHOW_USER_CONFIG` returning URL-encoded legacy format (`error=0&text=...`).
- RDash: emulates the real Dewabiz Domain Reseller Open API shape (customer→contact→domain resource graph keyed by numeric id, `{success,data,message}` envelope, form-urlencoded requests) — `/v1/domains/availability` (names containing `taken` → unavailable), `/v1/domains` (register) `/v1/domains/transfer`, `/v1/domains/{id}/renew|ns|contacts|auth_code|dns`, `/v1/domains/details` (name→id resolution), `/v1/customers[/{id}/contacts]`, `/v1/account/profile|balance`. Basic auth checked. In-memory registry.
- Mail capture: `POST /mail/send` {to, subject, html, text} → store; `GET /mail/messages?to=` → list (newest first); `DELETE /mail/messages`.
- Turnstile: `POST /turnstile/v0/siteverify` (form-urlencoded `secret`,`response`) mirrors Cloudflare's test-secret semantics (`1x…AA` always passes, `2x…AA` always fails, `3x…AA` token-spent, empty response → `missing-input-response`); lets the CAPTCHA verify path run hermetically in dev/E2E.
- `POST /mock/reset` clears all state. All state mutex-guarded in memory.

## 13. Frontend contract

- BFF pattern: SvelteKit server (`hooks.server.ts` + `$lib/server/api.ts`) holds `access_token`/`refresh_token` in httpOnly cookies; every server `load`/action calls Go API with Bearer; auto-refresh on 401 once. `locals.user` populated from `/auth/me` (cached per request). Client-side fetches go through SvelteKit route handlers or server actions only — **no direct browser→Go calls except invoice status polling via `/api/proxy` route if needed**. Env: `API_URL` (server-side), `PUBLIC_APP_NAME`.
- Routes: `(public)`: `/login /register /verify-email /forgot-password /reset-password /order` (product list→configure→cart→checkout), `/payments/return`. `(client)` guarded: `/dashboard /services /services/[id] /domains /domains/[id] /billing /billing/invoices/[id] /billing/deposit /support /support/[id] /support/new /account`. `(admin)` guarded role admin/staff under `/admin`: dashboard, clients(+detail tabs: summary/services/domains/invoices/tickets/notes), orders, invoices(+detail), services(+detail+actions), domains(+detail), tickets(+detail), products(+groups+options+coupons), servers(+groups), registrars, gateways, email-templates, settings(general/billing/mail/automation/tax), staff, reports(revenue/orders/services), logs(audit/email/integration).
- Design: WHMCS-like — admin: dark sidebar `#141b2d`-ish + blue accent `#336699`/`#4a90d9`, topbar w/ global search; client: horizontal navbar, card grid. Status badges: green active/paid, yellow pending/unpaid, red overdue/suspended/terminated, gray cancelled. Components in `$lib/components`: `DataTable.svelte` (server pagination/sort/filter slots), `StatCard`, `StatusBadge`, `Modal`, `FormField`, `Tabs`, `Toast` (+store), `Sidebar`, `Topbar`, `Breadcrumb`, `EmptyState`, `ConfirmDialog`, `MoneyText` (IDR format `Rp1.234.567`), `DateText` (Asia/Jakarta). i18n: `$lib/i18n` dictionary `id`/`en`, default `id`, switcher in footer/topbar; `t('key')` store-based.
- All money display IDR via `Intl.NumberFormat('id-ID',{style:'currency',currency:'IDR',maximumFractionDigits:0})`.

## 14. Quality gates

- Backend: `go build ./... && go vet ./... && go test -race -covermode=atomic ./...` — coverage >90% (excluding cmd/, migrations; measured over internal/... + pkg/...).
- Frontend: `npm run check` (svelte-check) + `npm run build` green; Playwright E2E per PRD §13.2 (8 flows) against full local stack with mockserver.
- Lint-ish: idiomatic Go (gofmt), no `panic` in request path, no TODO left for P0 features.

## 15. Installation wizard (`/api/v1/install/*`, `internal/installer`, `internal/modules/install`)

Two phases, because `composition.Build` (and therefore any DB-backed module/service) cannot run until
`DATABASE_URL`/`JWT_SECRET`/`APP_ENCRYPTION_KEY` already resolve:

- **Bootstrap phase** (`internal/installer`, no ports/domain/composition dependency): only reached when
  `config.Load()` itself fails (missing/invalid required env vars) — `cmd/api`'s `run()` serves a minimal
  standalone Fiber app instead of exiting fatally, exposing `/api/v1/install/bootstrap/{status,test-db,test-redis,
  test-s3,test-mail,save}`. `save` validates the submitted values via the same `config.LoadFrom`, auto-generates
  `JWT_SECRET`/`APP_ENCRYPTION_KEY` via `crypto/rand` if left blank, writes them to the `ENV_FILE` path (via
  `platform/config.WriteEnvFile`), then self-restarts the process in place (`syscall.Exec`, Unix-only) so the next
  boot picks up the completed config fresh. Production Coolify/Dokploy deploys are still expected to provide env
  vars via the platform's own secret manager per `PRD.md`'s `REQ-DEPLOY-003` — this bootstrap phase is the fallback
  for a bare VM/local machine with nothing configured yet, not the primary production path.
- **App-level phase** (`internal/modules/install`, a normal module wired like the other 15): reached once config/DB
  are up. `GET /api/v1/install/status` reports `installed` (defined as "does any `role='admin'` user exist" via
  `ports.UserRepo.ExistsAnyAdmin`); `POST /api/v1/install/admin` creates the first admin (mirrors
  `adminops.Service.CreateStaff` but `Role: domain.RoleAdmin`) and **must** re-check `ExistsAnyAdmin` server-side on
  every call, rejecting with `CONFLICT` once an admin already exists, so it can never be used to add a second admin
  after real install; `POST /api/v1/install/settings` is an optional passthrough to the existing
  `settings.Service.Update` for the whitelisted `company.*` keys (§10) and never gates `installed`.
