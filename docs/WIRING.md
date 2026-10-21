# WHCMS — Composition Root Wiring Guide

> A map of how the composition root (`internal/composition/build.go`) is structured
> and why it is structured that way. Ground truth is always the code on disk
> (`internal/modules/*/service.go|repo.go|handler.go|dto.go`, `internal/worker/*.go`,
> `internal/integration/*/*.go`, `internal/ports/ports.go`) — if this doc and the code
> disagree, trust the code and fix this doc.

## 0. The circular-dependency problem (read this first)

Several modules depend on each other's **service-level** (not repository-level) interfaces in a cycle:

- `orders` needs `billing` (as `ports.InvoiceCreator`, to create the checkout invoice) — but `billing` needs
  `orders` (as `ports.ServiceActivator`, to activate paid orders).
- `billing` needs `payments` (as `ports.PaymentApplier`, for admin manual payment) — but `payments` needs
  `billing` (as `ports.PaidInvoiceProcessor`, to dispatch post-payment effects).
- `billing` needs `provisioning` (`ports.ServiceRenewer`) and `provisioning` needs `billing`
  (`ports.InvoiceCreator`, for upgrade invoices).
- `billing` needs `domains` (`ports.DomainRenewer`) and `domains` needs `billing` (`ports.InvoiceCreator`,
  client-initiated renewals).
- `billing` needs `clients` (credit add/deduct) and `clients` needs `billing` (`ports.InvoiceCreator`, deposits).
- Almost everything needs `notifications` (`ports.NotificationSender`); nothing notifications needs back is a
  service (only repos — see below), so this one is NOT circular, but it still benefits from the same treatment
  for a uniform pattern.

**None of this is a Go import cycle** — module packages only import `ports`, `domain`, `jobs`, `pkg/*`,
`platform/*`, never each other. The cycle only exists at the **value** level in the composition root: you cannot
construct concrete service A before concrete service B if A's constructor needs B and B's constructor needs A.

**Fix: late-bound forwarder handles.** For each of these cross-module **service** interfaces, create a tiny
struct in the composition root that holds a settable field of the interface type and forwards every method to
it. Construct one forwarder per interface FIRST (zero value), pass the forwarder (which already satisfies the
interface) into every `Deps` struct that needs it, construct all the real services in any order, then at the end
set each forwarder's field to the real concrete service. Nothing calls these methods during wiring — only during
real request/job handling, long after wiring finishes — so a temporarily "empty" forwarder is safe.

Example:

```go
type invoiceCreatorFwd struct{ inner ports.InvoiceCreator }

func (f *invoiceCreatorFwd) CreateInvoice(ctx context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
	return f.inner.CreateInvoice(ctx, in)
}

// ... construct: icFwd := &invoiceCreatorFwd{}
// ... pass icFwd anywhere a ports.InvoiceCreator is needed
// ... after billingSvc := billing.New(...): icFwd.inner = billingSvc
```

**You need exactly 8 forwarder types** (name them however you like, e.g. in a new file
`internal/composition/forwarders.go`):

| Forwarder | Interface | Real impl (bind after construction) | Consumed by (Deps field) |
|---|---|---|---|
| invoice creator | `ports.InvoiceCreator` (`CreateInvoice`) | `billing.Service` | orders.Invoices, clients.Invoices, provisioning.Billing, domains.Invoices |
| service activator | `ports.ServiceActivator` (`ActivateOrder`) | `orders.Service` | billing.Activator, worker Deps.Orders |
| service renewer | `ports.ServiceRenewer` (`RenewService`, `ApplyUpgrade`) | `provisioning.Service` | billing.Renewer |
| domain renewer | `ports.DomainRenewer` (`RenewDomainAfterPayment`) | `domains.Service` | billing.DomainRenewer |
| payment applier | `ports.PaymentApplier` (`ApplyPayment`) | `payments.Service` | billing.Payments |
| paid invoice processor | `ports.PaidInvoiceProcessor` (`ProcessPaid`) | `billing.Service` | payments.Processor |
| credit service | local narrow ifaces (`AddCredit`, `DeductCredit`, both methods on one struct so it satisfies every narrower consumer interface via structural typing) | `clients.Service` | billing.Credit, payments.Credits (DeductCredit only), provisioning.Credit (AddCredit only) |
| notification sender | `ports.NotificationSender` (`SendTemplate`, `AlertAdmin`) | `notifications.Service` | auth.Notify, orders.Notifier, billing.Notifier, provisioning.Notify, domains.Notifier, tickets.Notifier, worker Deps.Notifier |

Everything else that looks cross-module is actually **repository-level** and has NO ordering problem — a
`<module>.NewRepo(db)` only needs `*db.DB`, so build ALL repos first, in one block, in any order:
`auth.NewUserRepo`, `clients.NewRepo` (satisfies `ports.ClientRepo` + `ports.CreditRepo`),
`catalog.NewRepo` (+ `.CouponRepo()` view for `ports.CouponRepo`), `orders.NewRepo`, `billing.NewRepo`
(satisfies `ports.InvoiceRepo` AND the narrow `InvoiceStore` subset that orders/provisioning need),
`payments.NewRepo` (`ports.TransactionRepo`), `provisioning.NewRepo` (+ `.Servers()` view for `ports.ServerRepo`),
`domains.NewRepo` + `domains.NewRegistrarRepo`, `tickets.NewRepo`, `notifications.NewTemplateRepo`,
`adminops.NewRepo`, plus the pre-existing `repository.NewAuditRepo` / `NewEmailLogRepo` / `NewIntegrationLogRepo`
/ `NewSettingsRepo` / `NewAuditLogger` / `NewIntegrationLogger`. None of these repo constructors depend on any
service, so there's no cycle here — build them all before any service.

## 1. Recommended structure: one shared builder, two thin mains

Do **not** duplicate ~300 lines of wiring in both `cmd/api/main.go` and `cmd/worker/main.go` — `cmd/worker` is a
**separate OS process**, so it cannot reuse Go values from `cmd/api`; it must build its own copy of every
repo/service by calling the same shared builder function.

Create `backend/internal/composition/build.go` (new package, owned by wiring):

```go
package composition

type App struct {
    Auth          *auth.Service
    Clients       *clients.Service
    Catalog       *catalog.Service
    Orders        *orders.Service
    Billing       *billing.Service
    Payments      *payments.Service
    Provisioning  *provisioning.Service
    Domains       *domains.Service
    Tickets       *tickets.Service
    Notifications *notifications.Service
    AdminOps      *adminops.Service
    Settings      *settingssvc.Service // existing reference module
    AuthUsersRepo ports.UserRepo       // needed by cmd/api for the PermissionSource
    // + anything else a caller needs directly (e.g. for handlers)
}

func Build(ctx context.Context, cfg config.Config, database *db.DB, rdb *redis.Client, s3 *storage.Client, log *slog.Logger) (*App, error) {
    // 1. repos (any order)
    // 2. 8 forwarders (zero value)
    // 3. adapters (duitku, cpanel, directadmin, rdash) from cfg
    // 4. services (any order, using forwarders for the 8 cross-service deps)
    // 5. bind forwarders to the real services
    // 6. return *App
}
```

`cmd/api/main.go` calls `composition.Build(...)`, then for each service constructs its `Handler` and calls
`RegisterRoutes(api)` where `api := app.Group("/api/v1")` (the bare group — **every module's `RegisterRoutes`
creates its own `/admin` sub-group internally** with its own `RequireAuth`/`RequireRole`/`RequirePermission`
chain; do not pre-wrap an outer `/admin` group for them, only the existing settings reference module used that
older pattern and can keep it as-is).

`cmd/worker/main.go` calls `composition.Build(...)` too (fresh DB/Redis connections in its own process), then
calls `worker.RegisterHandlers(mux, worker.Deps{...})` using the built services directly (no forwarders needed
here — by the time `worker.Deps` is assembled, every field is a real concrete service already returned by
`Build`), and replaces its static `cronSpecs` map with `worker.Schedules()`.

## 2. Per-module Deps — exact fields (verify against each module's service.go if anything mismatches)

### auth
`auth.New(auth.Deps{Users: auth.NewUserRepo(db), Clients: clientsRepo, Tx: txManager, Hasher: crypto.NewPasswordHasher(), Tokens: authtoken.New(cfg.JWTSecret, rdb, clk), OneTime: tokenstore.New(rdb), Notify: notificationFwd, Limiter: ratelimit.New(rdb), Audit: auditLogger, Clock: clk, Encryptor: encryptor, FrontendURL: cfg.FrontendURL})`. Also: `authUsersRepo := auth.NewUserRepo(db)` (use this same instance for `Deps.Users` AND for `transporthttp.Middleware.Perms = auth.NewPermissionSource(authUsersRepo)` AND for `adminops.Deps.Users` AND `notifications` narrow `UserGetter` AND `provisioning`/`domains`/`tickets` narrow `UserStore`/`UserRepo` needs). Handler: `auth.NewHandler(authSvc, mw).RegisterRoutes(api)` — mounts `/auth/*` AND `/admin/clients/:id/impersonate` itself.

### clients
`clientsRepo := clients.NewRepo(db)` (satisfies `ports.ClientRepo`, `ports.CreditRepo`, and module-local `ContactStore`/`SearchStore`/`AggregateStore`/`ExportStore` — pass the same instance for all of `Clients/Credits/Contacts/Search/Aggregate/Export` Deps fields). `clients.New(clients.Deps{Tx, Clients: clientsRepo, Credits: clientsRepo, Contacts: clientsRepo, Search: clientsRepo, Aggregate: clientsRepo, Export: clientsRepo, Users: authUsersRepo, Invoices: invoiceCreatorFwd, Settings: settingsRepo, Hasher, Audit, Clock})`. This `*clients.Service` IS the credit-service forwarder target (`AddCredit`/`DeductCredit` methods) — bind it to the credit forwarder after construction. Handler: `clients.NewHandler(svc, mw).RegisterRoutes(api)`.

### catalog
`catalogRepo := catalog.NewRepo(db)`. `catalog.New(catalog.Deps{Products: catalogRepo, Options: catalogRepo, Coupons: catalogRepo.CouponRepo(), Tx, Cache: cache.New(rdb), Audit, Clock})`. Handler: `catalog.NewHandler(svc, mw).RegisterRoutes(api)`. Give `orders` a reference to `catalogRepo.CouponRepo()` for `ports.CouponRepo`, and the catalog `*Service` itself if `orders` needs `ValidateCoupon` (check orders' actual Deps field name/type on disk — it may want `ports.CouponRepo` only, or the narrow `ValidateCoupon` method; wire whichever the code declares).

### orders
`ordersRepo := orders.NewRepo(db)` (satisfies `Deps.Orders` AND `Deps.Stock`). `orders.New(orders.Deps{Tx, Orders: ordersRepo, Products: catalogRepo, Stock: ordersRepo, Coupons: catalogRepo.CouponRepo(), Clients: clientsRepo, Users: authUsersRepo, Services: provisioningRepo, Domains: domainsRepo, Registrars: domainsRegistrarRepo, Registrar: rdashAdapter, Invoices: invoiceCreatorFwd, InvoiceSt: billingRepo, Settings: settingsRepo, Enqueuer: enqueuer, Notifier: notificationFwd, Encryptor: encryptor, Audit, Clock})`. This `*orders.Service` IS the service-activator forwarder target — bind after construction. Handler: `orders.NewHandler(svc, mw).RegisterRoutes(api)`.

### billing
`billingRepo := billing.NewRepo(db)` (satisfies `ports.InvoiceRepo` AND module-local `InvoiceStore` — reuse this ONE instance everywhere billing's repo is needed: `Deps.Invoices` here, `orders.Deps.InvoiceSt`, `provisioning.Deps.Invoices`, `payments.Deps.Invoices`). `billing.New(billing.Deps{Tx, Invoices: billingRepo, Transactions: paymentsRepo, Clients: clientsRepo, Users: authUsersRepo, Services: provisioningRepo, Domains: domainsRepo, Coupons: catalogRepo.CouponRepo(), Settings: settingsRepo, Credit: creditFwd, Payments: paymentApplierFwd, Activator: serviceActivatorFwd, Renewer: serviceRenewerFwd, DomainRenewer: domainRenewerFwd, Notifier: notificationFwd, PDF: pdf.New(), Storage: s3, Enqueuer: enqueuer, Audit, Clock, FrontendURL: cfg.FrontendURL})`. This `*billing.Service` IS both the invoice-creator forwarder target AND the paid-invoice-processor forwarder target — bind both after construction. Handler: `billing.NewHandler(svc, mw).RegisterRoutes(api)`.

### payments
`paymentsRepo := payments.NewRepo(db)`. `payments.New(payments.Deps{Tx, Invoices: billingRepo, Transactions: paymentsRepo, Clients: clientsRepo, Users: authUsersRepo, Gateways: map[string]ports.PaymentGateway{"duitku": duitkuAdapter, "manual": manualAdapter}, GatewayOrder: []string{"duitku", "manual"}, DefaultGateway: "duitku", Processor: paidInvoiceProcessorFwd, Credits: creditFwd, Cache: cache.New(rdb), Clock, Audit, IntLog: integrationLogger, Log: log, AppBaseURL: cfg.AppBaseURL, FrontendURL: cfg.FrontendURL})`. `Gateways` is a registry keyed by `domain.Gateway` code — `GetPaymentMethods` aggregates across `GatewayOrder`; `PayInvoice` routes `"credit"` → credit, `payments.ManualMethodBankTransfer` ("bank_transfer") → the `"manual"` entry, anything else → `DefaultGateway` ("duitku", preserving raw Duitku channel codes like "VA"/"OV" unprefixed). `HandleCallback`/`ReconcileOne` stay hardcoded to a fixed `Gateways["duitku"]` lookup (webhook shape + external status polling are inherently Duitku-specific; manual has neither). This `*payments.Service` IS the payment-applier forwarder target — bind after construction. Handler: `payments.NewHandler(svc, mw).RegisterRoutes(api)`.

### provisioning
`provisioningRepo := provisioning.NewRepo(db)` (satisfies `Deps.Services`; use `provisioningRepo.Servers()` for anything needing `ports.ServerRepo`, including `Deps.Servers` itself). `provisioning.New(provisioning.Deps{Services: provisioningRepo, Servers: provisioningRepo, Products: catalogRepo, Clients: clientsRepo, Users: authUsersRepo, Invoices: billingRepo, Modules: map[string]ports.ServerModule{"cpanel": cpanelAdapter, "directadmin": directadminAdapter}, Crypt: encryptor, Queue: enqueuer, Notify: notificationFwd, Billing: invoiceCreatorFwd, Credit: creditFwd, Settings: settingsRepo, Tx, Audit, Clock})`. This `*provisioning.Service` IS the service-renewer forwarder target — bind after construction. Handler: `provisioning.NewHandler(svc, mw).RegisterRoutes(api)`.

### domains
`domainsRepo := domains.NewRepo(db)`; `domainsRegistrarRepo := domains.NewRegistrarRepo(db)`. `domains.New(domains.Deps{Domains: domainsRepo, Registrars: domainsRegistrarRepo, Clients: clientsRepo, Users: authUsersRepo, Settings: settingsRepo, Registrar: rdashAdapter, Enqueuer: enqueuer, Invoices: invoiceCreatorFwd, Notifier: notificationFwd, Encryptor: encryptor, Audit, Clock, Log: log})`. This `*domains.Service` IS the domain-renewer forwarder target — bind after construction. Handler: `domains.NewHandler(svc, mw).RegisterRoutes(api)`. **`POST /domains/check` is registered by THIS module** — do not also look for it in catalog.

### tickets
`ticketsRepo := tickets.NewRepo(db)` (satisfies `Deps.Tickets` AND `Deps.Search`). `tickets.New(tickets.Deps{Tx, Tickets: ticketsRepo, Search: ticketsRepo, Clients: clientsRepo, Users: authUsersRepo, Storage: s3, Settings: settingsRepo, Notifier: notificationFwd, Audit, Clock, FrontendURL: cfg.FrontendURL})`. Handler: `tickets.NewHandler(svc, mw).RegisterRoutes(api)`.

### notifications
`templateRepo := notifications.NewTemplateRepo(db)`. `notifications.New(notifications.Deps{Templates: templateRepo, Logs: repository.NewEmailLogRepo(db), Users: authUsersRepo, Clients: clientsRepo, Settings: settingsRepo, Storage: s3, Mailer: mailerImpl, Enqueuer: enqueuer, Audit, Clock, FrontendURL: cfg.FrontendURL, AdminAlertEmail: cfg.AdminAlertEmail})`. This `*notifications.Service` IS the notification-sender forwarder target — bind after construction (do this EARLY since so many other services' construction just stores the forwarder value, order doesn't matter, but the BIND must happen before `main()` returns / before the server starts accepting traffic). Handler: `notifications.NewHandler(svc, mw).RegisterRoutes(api)`. `mailerImpl, err := mailer.New(cfg, log)`.

### adminops
`adminopsRepo := adminops.NewRepo(db)`. `adminops.New(adminops.Deps{Dashboard: adminopsRepo, Logs: adminopsRepo, Staff: adminopsRepo, Users: authUsersRepo, Audit: repository.NewAuditRepo(db), EmailLogs: repository.NewEmailLogRepo(db), Settings: settingsRepo, Cache: cache.New(rdb), Hasher: crypto.NewPasswordHasher(), AuditLog: auditLogger, Clock: clk, Secrets: adminops.GatewaySecrets{DuitkuMerchantCode: cfg.DuitkuMerchantCode, DuitkuMode: cfg.DuitkuEnv, DuitkuBaseURL: cfg.DuitkuBaseURL, DuitkuAPIKeySet: cfg.DuitkuAPIKey != ""}})`. Handler: `adminops.NewHandler(svc, mw).RegisterRoutes(api)`.

### adapters (construct before services that need them)
```go
httpClient := &http.Client{Timeout: 30 * time.Second}
integrationLogger := repository.NewIntegrationLogger(repository.NewIntegrationLogRepo(db), log)
// resolveDuitkuCredentials re-reads merchant code/mode-or-overridden base
// URL/API key from settings (env fallback per field) on every call — see
// resolveRDashCredentials below for a similar pattern. Base URL precedence
// (resolveDuitkuBaseURL): DB setting override > explicit DUITKU_BASE_URL env
// pin (e.g. dev/E2E pinning at the mockserver) > live mode-derived
// sandbox/production URL — the env pin must outrank live mode, or every
// dev/E2E run would suddenly call real Duitku the moment this wiring landed
// (a real regression caught by the E2E suite, not just designed on paper).
resolveDuitkuCredentials := func(ctx context.Context) (duitku.Credentials, error) {
    merchantCode, err := settingsRepo.GetString(ctx, adminops.SettingDuitkuMerchantCode, cfg.DuitkuMerchantCode)
    if err != nil { return duitku.Credentials{}, err }
    mode, err := settingsRepo.GetString(ctx, adminops.SettingDuitkuMode, cfg.DuitkuEnv)
    if err != nil { return duitku.Credentials{}, err }
    settingBaseURL, err := settingsRepo.GetString(ctx, adminops.SettingDuitkuBaseURL, "")
    if err != nil { return duitku.Credentials{}, err }
    baseURL := resolveDuitkuBaseURL(settingBaseURL, cfg.DuitkuBaseURLExplicit, mode)
    enc, err := settingsRepo.GetString(ctx, adminops.SettingDuitkuAPIKeyEnc, "")
    if err != nil { return duitku.Credentials{}, err }
    apiKey := cfg.DuitkuAPIKey
    if enc != "" {
        if apiKey, err = encryptor.Decrypt(enc); err != nil { return duitku.Credentials{}, err }
    }
    return duitku.Credentials{MerchantCode: merchantCode, APIKey: apiKey, BaseURL: baseURL}, nil
}
duitkuAdapter := duitku.New(duitku.Config{
    MerchantCode: cfg.DuitkuMerchantCode, APIKey: cfg.DuitkuAPIKey, BaseURL: cfg.DuitkuBaseURL,
    CallbackURL: cfg.AppBaseURL + "/api/v1/webhooks/duitku", ReturnURL: cfg.FrontendURL + "/payments/return",
}, resolveDuitkuCredentials, integrationLogger, clk)
cpanelAdapter := cpanel.New(cpanel.Config{}, httpClient, integrationLogger, clk)
directadminAdapter := directadmin.New(directadmin.Config{}, httpClient, integrationLogger, clk)
rdashAdapter := rdash.New(rdash.Config{BaseURL: cfg.RDashBaseURL, ResellerID: cfg.RDashResellerID, APIKey: cfg.RDashAPIKey}, nil, integrationLogger, clk)
```

### worker Deps (cmd/worker/main.go, after calling composition.Build)
```go
worker.RegisterHandlers(mux, worker.Deps{
    Orders:        app.Orders,        // ports.ServiceActivator
    Provisioning:  app.Provisioning,  // satisfies ProvisioningJobs
    Domains:       app.Domains,       // satisfies DomainJobs
    Billing:       app.Billing,       // satisfies BillingJobs
    Payments:      app.Payments,      // satisfies PaymentJobs
    Notifications: app.Notifications, // satisfies MailDeliverer
    AdminOps:      app.AdminOps,      // satisfies Housekeeper
    Notifier:      app.Notifications, // ports.NotificationSender (final-retry alerts)
    Locker:        lock.New(rdb),
    Log:           log,
})
for _, sched := range worker.Schedules() {
    if _, err := scheduler.Register(sched.Spec, asynq.NewTask(sched.Type, nil)); err != nil { return err }
}
```
Replace the whole static `cronSpecs` map with `worker.Schedules()`. Note the worker package's own schedule (05:00
Sunday housekeeping, 03:00/03:30 suspend/terminate, plus `cron:mark_overdue` at 01:15) supersedes the skeleton's
approximation — use `worker.Schedules()`, don't hand-merge.

## 3. Verifying a wiring change (in order)

1. `cd backend && gofmt -l .` — clean.
2. `go build ./...` — must succeed with zero errors across the whole module (if a field name or type
   mismatches this doc, the actual `Deps` struct in the module's `service.go` is authoritative — fix
   the composition root and this doc, not the module).
3. `go vet ./...` — clean.
4. `go test -race ./...` — all packages green (existing per-module tests must still pass unmodified).
5. Boot test: run `cmd/api` against the local services (DATABASE_URL, JWT_SECRET, APP_ENCRYPTION_KEY,
   RUSTFS_* env vars — see .env.example) in the background, `curl localhost:8080/healthz` (200) and
   `/readyz` (all `ok`), then hit 2-3 real endpoints to prove wiring end-to-end, e.g.:
   - `GET /api/v1/products` → 200 with envelope (empty list is fine, catalog has no seed products yet)
   - `POST /api/v1/auth/register` with a throwaway email → 201
   - `GET /api/v1/auth/me` with the returned access token → 200 with the registered user
   Then SIGTERM it (graceful shutdown must still work) and check logs for panics.
6. Boot test `cmd/worker` similarly: start it, wait ~5s, confirm no panic/crash in logs, confirm the process is
   still alive, then SIGTERM it.
