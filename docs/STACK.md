# WHCMS — Stack Reference (written by foundation)

> Companion to `docs/CONTRACTS.md`. The code on disk under
> `backend/internal/ports/ports.go`, `backend/internal/domain/`,
> `backend/migrations/` and this file are the authoritative contracts.

## Resolved versions (pinned in backend/go.mod)

| Dependency | Version |
|---|---|
| Go toolchain | 1.26.3 (`go 1.26` in go.mod) |
| github.com/gofiber/fiber/v3 | **v3.4.0** (stable v3) |
| github.com/jackc/pgx/v5 | v5.10.0 |
| github.com/redis/go-redis/v9 | v9.21.0 |
| github.com/hibiken/asynq | v0.26.0 |
| github.com/golang-jwt/jwt/v5 | v5.3.1 |
| golang.org/x/crypto (argon2) | v0.53.0 |
| github.com/pquerna/otp | v1.5.0 |
| github.com/minio/minio-go/v7 | v7.2.1 |
| github.com/golang-migrate/migrate/v4 | v4.19.1 (pgx/v5 driver, iofs source) |
| github.com/go-playground/validator/v10 | v10.30.3 |
| github.com/stretchr/testify | v1.11.1 |
| github.com/google/uuid | v1.6.0 |
| github.com/go-pdf/fpdf | v0.9.0 (primary; jung-kurt/gofpdf fallback NOT needed) |
| github.com/wneessen/go-mail | v0.7.3 |

## Fiber v3 cheatsheet (verified against v3.4.0 source via `go doc`)

Fiber v3 differs from v2 — do not copy v2 snippets.

```go
import "github.com/gofiber/fiber/v3"

// fiber.Ctx is an INTERFACE (not *fiber.Ctx).
type Handler = func(fiber.Ctx) error            // handlers AND middleware
type ErrorHandler = func(fiber.Ctx, error) error

app := fiber.New(fiber.Config{
    AppName:      "WHCMS API",
    ErrorHandler: transporthttp.ErrorHandler(log), // single apperr→HTTP mapper
})

// Routing — IMPORTANT: handlers run in ARGUMENT ORDER. Middleware first,
// final handler LAST:  r.Get(path, mw1, mw2, handler)
app.Get("/x", mw.RequireAuth(), mw.RequireRole("admin"), h.Get)
grp := app.Group("/api/v1")                       // Group(prefix, handlers...) Router
admin := grp.Group("/admin", mw.RequireAuth(), mw.RequireRole("admin", "staff"))

// Ctx essentials
c.Params("id")            // path param (string; parse yourself)
c.Query("page")           // query param
c.Get("Authorization")    // request header
c.Set("X-Request-ID", id) // response header
c.Body()                  // raw body []byte
c.Bind().Body(&dto)       // parse JSON/form into struct (replaces BodyParser)
c.Bind().Query(&dto)      // parse query params into struct
c.JSON(v)                 // write JSON
c.Status(201).JSON(v)     // Status returns Ctx (chainable)
c.SendStatus(204)         // status only
c.SendString("OK")        // plain body (webhook "OK" replies)
c.SendStream(r, size)     // stream (PDF downloads)
c.IP()                    // client IP (rate limiting)
c.Locals(key, v); c.Locals(key)    // request-scoped values (any keys)
c.Context()               // context.Context for services  (NOT c.UserContext())
c.SetContext(ctx)         // replace the request context (request-id middleware)
c.FormFile("attachment")  // *multipart.FileHeader (ticket uploads)

// Listening / shutdown
app.Listen(":8080", fiber.ListenConfig{DisableStartupMessage: true})
app.ShutdownWithContext(ctx)

// Tests: app.Test(req) — default 1s timeout; pass fiber.TestConfig to change.
resp, err := app.Test(httptest.NewRequest("GET", "/x", nil))
```

Gotchas:
- Route path `"/"` on a group matches both `/prefix` and `/prefix/`.
- `c.Bind().Body` returns `*fiber.BindError` on bad input in manual mode; for
  a plain `map[string]any` body just `json.Unmarshal(c.Body(), &m)`.
- Webhook form-urlencoded bodies: `c.Bind().Form(&dto)` with `form:` tags
  (ports.CallbackPayload already has them).

## pgx ctx-tx pattern (platform/db)

Repositories NEVER hold `*pgxpool.Pool` directly. They hold `*db.DB` and run
every query through `d.Querier(ctx)`, which returns the transaction stored in
the context by `TxManager.WithinTx` (or the pool when there is none):

```go
type FooRepo struct{ db *db.DB }

func (r *FooRepo) GetByID(ctx context.Context, id int64) (*domain.Foo, error) {
    var f domain.Foo
    err := r.db.Querier(ctx).QueryRow(ctx,
        `SELECT id, name FROM foos WHERE id = $1`, id).Scan(&f.ID, &f.Name)
    if errors.Is(err, pgx.ErrNoRows) {
        return nil, apperr.NotFound("foo")
    }
    ...
}

// Service-level transaction: everything inside fn joins ONE tx.
err := s.tx.WithinTx(ctx, func(txCtx context.Context) error {
    inv, err := s.invoices.GetByIDForUpdate(txCtx, id) // row lock
    if err != nil { return err }
    if err := s.transactions.Create(txCtx, tx); err != nil { return err }
    return s.invoices.UpdateStatus(txCtx, id, domain.InvoicePaid, &now)
}) // commit on nil, rollback on error; nested WithinTx joins the outer tx
```

- Counters (`invoice/order/ticket` numbering) MUST be read inside the same tx:
  `UPDATE counters SET value = value + 1 WHERE scope = $1 RETURNING value`
  (insert the scope row with `ON CONFLICT` first). Scopes:
  `invoice:YYYYMM`, `order:YYYYMM`, `ticket` (helpers in `internal/domain/numbers.go`).
- Migrations are **embedded** (`backend/migrations/embed.go`) and run on API
  start (`db.Migrate`). Manual ops: `go run ./cmd/migrate up|down N`.

## How to add a module (copy the settings reference)

The settings module is the end-to-end reference. Mirror these files:

| Layer | Reference file | Notes |
|---|---|---|
| Repo | `internal/repository/settings_repo.go` | pgx impl; every query via `db.Querier(ctx)`; `//go:build integration` tests in `repos_integration_test.go` |
| Service | `internal/service/settings/settings.go` | depends ONLY on `ports.*` interfaces; returns `*apperr.Error`; audits via `ports.AuditLogger` |
| Service tests | `internal/service/settings/settings_test.go` | table-driven, uses `internal/ports/mocks` (function-field mocks, nil-safe) |
| Handler | `internal/transport/http/settings_handler.go` | consumes a small service interface; `Register(r fiber.Router)`; envelope via `pkg/httpx` |
| Handler tests | `internal/transport/http/settings_handler_test.go` | Fiber `app.Test` + mocked service + stub identity middleware |

Recipe:
1. Define/extend the repo interface ONLY in `internal/ports/ports.go` (already
   done by foundation for all modules — read it first; do not redefine).
2. Implement the repo in `internal/repository/<mod>_repo.go`.
3. Implement use-cases in `internal/service/<mod>/` taking ports in the
   constructor (`New(repo, tx, audit, ...)`). Wrap multi-write flows in
   `tx.WithinTx`. Return `*apperr.Error` (`apperr.NotFound`, `apperr.Validation`
   with `FieldError` details, `apperr.Conflict`, ...).
4. Validate DTOs with `platform/validate.Validator.Struct(dto)` → it already
   produces the VALIDATION apperr with per-field details.
5. Add the handler in `internal/transport/http/<mod>_handler.go` with a
   `Register(r fiber.Router)` method; the composition root mounts it.
6. Add missing mocks ONLY to `internal/ports/mocks/` named `Mock<Interface>`
   (struct with `<Method>Fn` fields; methods delegate, nil-safe zero returns).
7. Jobs: constants + payload structs already in `internal/jobs`; enqueue via
   `ports.Enqueuer.Enqueue(ctx, jobs.TypeX, payload, ports.WithQueue("critical"))`.

## Auth / middleware wiring (foundation-provided)

- `internal/service/authtoken`: `IssueAccess(userID, role, clientID)` (HS256,
  15m, claims sub/role/cid/jti), `ParseAccess`, and Redis refresh tokens
  (`CreateRefresh`, `ConsumeRefresh`, `RotateRefresh`, `RevokeRefresh`;
  `whmcs:refresh:<token>`, TTL 30d, single-use).
- `internal/transport/http.Middleware` needs `{Tokens, Perms, Limiter, Log}`:
  `RequireAuth()`, `RequireRole("admin","staff")`, `RequirePermission("billing")`
  (admin bypass, staff permissions JSONB), `RequireClient()`,
  `RateLimit("public", 60, time.Minute)` (per-IP fixed window, fails open).
- Handlers read identity: `id := httpx.MustIdentity(c)` → `{UserID, Role, ClientID, JTI}`.
- Envelope helpers: `httpx.OK(c, data, page.Meta(total))`, `httpx.Created`,
  `httpx.ParsePage(c)` (defaults 1/10, max 1000). Errors: just `return err`
  from handlers — the global ErrorHandler maps apperr → status + envelope.

## Platform quick reference

| Need | Package | Notes |
|---|---|---|
| Config | `platform/config.Load()` | env only (§11); `APP_ENCRYPTION_KEY` = base64 32B |
| Logging | `platform/logger.New(env)` | slog JSON; request_id auto-attached from ctx |
| DB | `platform/db.Connect`, `db.Migrate`, `db.NewTxManager` | |
| Redis | `platform/redisx.Connect`, `redisx.Key("part", ...)` | ALL keys `whmcs:`-prefixed via `redisx.Key` |
| S3 | `platform/storage.New(ctx, Options{...})` | ensures bucket; `Healthy(ctx)` for /readyz |
| Secrets | `platform/crypto.NewEncryptor(cfg.EncryptionKey)` | AES-256-GCM base64 |
| Passwords | `platform/crypto.NewPasswordHasher()` | Argon2id PHC (m=65536,t=3,p=2) |
| TOTP | `platform/crypto.GenerateTOTPSecret/ValidateTOTP` | pquerna/otp |
| One-time tokens | `platform/tokenstore.New(rdb)` | kinds `verify_email`, `reset_password` |
| Queue | `platform/queue.NewClient/NewEnqueuer/NewServer/NewScheduler` | queues: critical/default/low |
| Cron lock | `platform/lock.New(rdb).WithLock(ctx, key, ttl, fn)` | CONFLICT apperr when held |
| Rate limit | `platform/ratelimit.New(rdb)` | fixed window |
| Cache | `platform/cache.New(rdb)` | GetJSON/SetJSON/Delete |
| Validation | `platform/validate.New().Struct(dto)` | json-tag field names |
| Clock | `platform/clock.New()` / `clock.Fixed{T: ...}` | inject everywhere time matters |
| Mail | `platform/mailer.New(cfg, log)` | drivers smtp/http/log by MAIL_DRIVER |
| PDF | `platform/pdf.New()` | `InvoicePDF(ctx, ports.InvoicePDFData)` |

## Testing conventions

- Unit gate: `go test -race -covermode=atomic ./...` (the `make cover` run includes it).
  Redis/PG/S3-backed platform tests auto-skip when the service is down
  (they use Redis DB 15/14, never DB 0).
- Repo integration tests: `//go:build integration`, run as part of `make cover`
  (database `whmcs_e2e`, auto-created and migrated by `make cover`; your local `whmcs` DB is
  never touched, see docs/E2E.md §3).
- Handler tests: Fiber `app.Test(...)` + mocked service (see settings tests).
- Coverage: `make cover` (internal/... + pkg/...) prints per-package coverage and the
  total, and enforces the 90% gate. Note: `ports` (interfaces) and `ports/mocks` count in
  the denominator until module tests exercise them.
- One package while iterating: `cd backend && ../scripts/with-env.sh .env.testing go test ./internal/modules/<mod>/...`
  (the integration-tagged repo tests need `-tags integration` and the migrated test DB that `make cover` prepares).

## Running locally

```bash
cp .env.example .env   # fill JWT_SECRET + APP_ENCRYPTION_KEY (openssl rand -base64 32), or skip it and use /install
make up                # api (migrates on start; GET /healthz, /readyz) + worker (asynq server + cron schedules,
                       # internal/worker) + frontend — logs in .stack/
make down
```
