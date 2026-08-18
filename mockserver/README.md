# whcms-mock

Standalone mock server for every external service WHCMS integrates with:
**Duitku** (payment gateway), **WHM/cPanel**, **DirectAdmin**, **RDash** (domain
registrar) and a **mail capture** sink. Single Go module, stdlib `net/http`
only, all state in memory (mutex-guarded) — nothing persists across restarts.

## Run

```sh
cd mockserver
go run .            # listens on :9090
```

| Env var                | Default     | Purpose                                        |
|------------------------|-------------|------------------------------------------------|
| `MOCK_PORT`            | `9090`      | Listen port; also used in generated URLs        |
| `DUITKU_MERCHANT_CODE` | `DEMO`      | Merchant code used for signature validation     |
| `DUITKU_API_KEY`       | `secretkey` | API key used for signature validation           |

The backend dev/E2E env must use the **same** Duitku credentials, otherwise
every signature check fails with `400 {"statusCode":"XX","statusMessage":"invalid signature"}`.

## Ops endpoints

| Endpoint             | Behavior                                              |
|----------------------|-------------------------------------------------------|
| `GET /healthz`       | `200 ok`                                              |
| `POST /mock/reset`   | Clears ALL state (Duitku txs, WHM/DA accounts, RDash domains, captured mail) |

Every request is logged as one line to stdout: `METHOD /path -> status (latency)`.

---

## 1. Duitku V2 (`DUITKU_BASE_URL=http://localhost:9090`)

Signature formulas (PRD §8.1). Hex comparison is case-insensitive; amounts are
whole-IDR integers rendered without decimals; JSON amounts may be numbers or
numeric strings.

| Endpoint | Signature |
|---|---|
| `POST /webapi/api/merchant/paymentmethod/getpaymentmethod` | `SHA256(merchantcode + amount + datetime + apiKey)` |
| `POST /webapi/api/merchant/v2/inquiry` | `MD5(merchantCode + merchantOrderId + paymentAmount + apiKey)` |
| `POST /webapi/api/merchant/transactionStatus` | `MD5(merchantCode + merchantOrderId + apiKey)` |
| Callback (sent by mock) | `MD5(merchantCode + amount + merchantOrderId + apiKey)` |

Any signature/merchant-code mismatch → `400 {"statusCode":"XX","statusMessage":"invalid signature"}`.

### Get payment methods

Request `{merchantcode, amount, datetime, signature}` → response
`{paymentFee:[{paymentMethod, paymentName, paymentImage, totalFee}], responseCode:"00", responseMessage:"SUCCESS"}`
with 8 fixed methods: `VC, BC, M2, OV, SA, SP, FT, DN` (`totalFee` is a string,
matching the real API).

### Inquiry (create transaction)

Request (subset used): `{merchantCode, paymentAmount, paymentMethod, merchantOrderId,
productDetails, additionalParam, callbackUrl, returnUrl, signature, expiryPeriod}` →

```json
{"merchantCode":"DEMO","reference":"MOCKREF-1","paymentUrl":"http://localhost:9090/payment/MOCKREF-1",
 "vaNumber":"7007000000000001","qrString":"MOCK-QR-MOCKREF-1","amount":"150000",
 "statusCode":"00","statusMessage":"SUCCESS"}
```

References are sequential per process (`MOCKREF-<n>`, reset by `/mock/reset`).
`callbackUrl`/`returnUrl` are captured per transaction. Each inquiry (payment
attempt) creates a new transaction; `transactionStatus` reports the latest
attempt for a given `merchantOrderId`.

### Check transaction

Request `{merchantCode, merchantOrderId, signature}` → `{merchantOrderId, reference,
amount, fee, statusCode, statusMessage}` where `statusCode` is `00` paid,
`01` pending, `02` cancelled/expired. Unknown order → `404`.

### Payment simulation (E2E drives these)

| Endpoint | Behavior |
|---|---|
| `GET /payment/{reference}` | HTML page (title "Duitku Mock Payment") showing order id + amount with a `#pay-now` "Bayar Sekarang" button that POSTs the pay endpoint. |
| `POST /mock/duitku/pay/{reference}` | Marks paid, then delivers a **real signed callback** (form-urlencoded POST) to the stored `callbackUrl` with fields `merchantCode, amount, merchantOrderId, productDetail, additionalParam, paymentCode, resultCode=00, reference, signature`. Non-200 responses are retried up to 3 times (4 attempts total, linear backoff). Then: `303` redirect to `returnUrl?merchantOrderId=…&resultCode=00&reference=…` if a returnUrl was given, else `200` JSON. Callback failure does NOT fail the payment (reconciliation via Check Transaction covers it). |
| `POST /mock/duitku/expire/{reference}` | Marks the tx expired → Check Transaction reports `02`. Paying an expired tx (or expiring a paid one) → `409`. |

## 2. WHM API 1 (cPanel) — server module config `hostname=localhost port=9090 use_ssl=false`

`GET|POST /json-api/<fn>` — params via query string or form body.
Auth header required: `Authorization: whm <user>:<token>` — any non-empty token
is accepted **except** the literal token `badtoken` (→ `403`).

Response envelope: `{"metadata":{"version":1,"reason":"OK","result":1,"command":"<fn>"},"data":{...}}`
(`result:0` + reason on failure; HTTP stays 200 for API-level errors, like real WHM).

| Function | Params | Notes |
|---|---|---|
| `version` | — | read-only connectivity/credential probe (Test Connection); `data.version = "11.126.0.5"` |
| `gethostname` | — | `data.hostname = "mock.whm.local"` (auto-fill metadata) |
| `get_nameserver_config` | — | `data.nameservers = ["ns1.mock.local","ns2.mock.local"]` (nameserver auto-population) |
| `createacct` | `username, domain, plan, password, contactemail` | duplicate → `result:0 "account exists"`; username `failme` → `result:0 "forced failure"` (for retry tests) |
| `suspendacct` | `user, reason` | reason stored, visible in `accountsummary` |
| `unsuspendacct` | `user` | |
| `removeacct` | `user` | |
| `changepackage` | `user, pkg` | |
| `passwd` | `user, password` | |
| `accountsummary` | `user` | `data.acct[0]` has `user, domain, plan, email, suspended (0/1), suspendreason` |
| `listaccts` | `searchtype?, search?` | all accounts, sorted by username; `search` is a regex filtered against the field `searchtype` selects (`package` → plan, `domain` → domain, else username) — the package variant backs the backend's PackageInUse guard; an invalid regex → `result:0` |
| `create_user_session` | `user` | `data.url = http://localhost:9090/cpanel-sso/<user>` (that URL serves a stub page) |

Unknown account → `result:0 "account does not exist"`. `user`/`username`
(and `pkg`/`plan`/`package`) are accepted interchangeably.

## 3. DirectAdmin — server module config `hostname=localhost port=9090 use_ssl=false`

`GET|POST /CMD_API_*` with HTTP **Basic auth** — any credentials accepted
**except** username `bad` (→ `401`). Responses are legacy URL-encoded:
`error=0&text=Success&details=...` (`error=1` on failure).

| Command | Params |
|---|---|
| `CMD_API_ACCOUNT_USER` | `action=create, username, email, passwd, domain, package, ip` — `failme` → `error=1`, duplicate → `error=1` |
| `CMD_API_SELECT_USERS` | `select0=<user>[&select1=…]` + `suspend=Suspend\|Unsuspend` or `delete=yes` |
| `CMD_API_MODIFY_USER` | `action=package, user, package` |
| `CMD_API_USER_PASSWD` | `username, passwd[, passwd2]` |
| `CMD_API_SHOW_USERS` | — → `list[]=<user>` array; read-only Test Connection probe (validates auth) |
| `CMD_API_SHOW_USER_CONFIG` | `user` → URL-encoded config dump incl. `suspended=yes\|no, package, domain, email` |

## 4. RDash v1 (`RDASH_BASE_URL=http://localhost:9090/v1`)

Emulates the real "Domain Reseller Open API" shape used by the RDash
integration (not a placeholder contract):
HTTP **Basic auth** `reseller_id:api_key` — any non-empty pair accepted
**except** `bad:bad` (→ `401`). Requests are
`application/x-www-form-urlencoded` (GETs use query params). Envelope:
`{"success":true,"data":{...},"message":"..."}`; validation failures are
`422` with an additional `errors:{field:[...]}` map. `404` unknown
domain/customer, `409` taken/duplicate. Dates are `YYYY-MM-DD`.

Domains are a resource graph (`customer` → `contact` → `domain`), keyed by a
numeric `id` for every per-domain mutation — not the domain name.

| Endpoint | Behavior |
|---|---|
| `GET /v1/domains/availability?domain=` | `data[]` of `{name, available:0\|1, message}`. Names containing `taken` (and already-registered names) are unavailable. One name per call, matching the real API. |
| `GET /v1/domains/details?domain_name=` | Full domain payload (`id, name, nameserver_1..5, status, status_label, expired_at, customer{id}, registrant_contact{...}`) — the only name→id lookup; `404` if unknown. |
| `POST /v1/customers` | Form fields `name,email,organization,street_1,city,state,country_code,postal_code,voice,password,password_confirmation` (all required) → `{id,...}`. `422` on any missing field or password mismatch. |
| `POST /v1/customers/{customer_id}/contacts` | Form fields `label,name,email,organization,street_1,city,state,country_code,postal_code,voice` → `{id,...}`. `404` unknown customer. |
| `POST /v1/domains` | Form `name,period,customer_id,nameserver[0..4],registrant_contact_id?` → registers, `status:1/"Active"`, `expired_at = today + period`. Auto-creates a "Default" contact from the customer when `registrant_contact_id` is omitted (matches the real API). `taken`/duplicate → `409`; unknown `customer_id` → `422`. |
| `POST /v1/domains/transfer` | Form `name,auth_code,period,customer_id,nameserver[0..4]` → `auth_code:"WRONG"` → `422`; already-registered `name` → `409`; else registers active, same shape as register. |
| `POST /v1/domains/{id}/renew` | Form `period,current_date` → extends `expired_at` by `period` years; `current_date` must equal the domain's current `expired_at` (`422 "Expiry date is not correct."` otherwise, same as production). |
| `PUT /v1/domains/{id}/ns` | Form `nameserver[0..4]` (0/1 required) → replaces nameservers. |
| `PUT /v1/domains/{id}/contacts` | Form `admin_contact_id,tech_contact_id,billing_contact_id,registrant_contact_id` → all must reference existing contacts. |
| `GET /v1/domains/{id}/auth_code` | `data:"MOCK-EPP-<name>"` |
| `GET|POST /v1/domains/{id}/dns` | GET → `data[]` of `{prefix,type,content,ttl}` (new domains get 2 default records: `A @` + `CNAME www`). POST `records[N][name\|type\|content\|ttl]` replaces the full set — at least one record required (matches production). |
| `GET /v1/account/profile` | `{id:<basic user as int>, name, email, currency:"IDR"}` |
| `GET /v1/account/balance` | `{currency:"IDR", balance:"10000000.00"}` |

## 5. Mail capture (`MAIL_DRIVER=http`, `MAIL_HTTP_URL=http://localhost:9090/mail/send`)

| Endpoint | Behavior |
|---|---|
| `POST /mail/send` | JSON `{to, from, subject, html, text}` → stored with `receivedAt` (requires `to`) |
| `GET /mail/messages?to=<email>` | JSON array, newest first; optional case-insensitive recipient filter |
| `DELETE /mail/messages` | Clears the mailbox |

---

## How E2E uses this server

1. **Boot** `go run ./mockserver` (or `MOCK_PORT=9090 ./whcms-mock`) alongside PG/Redis/RustFS.
2. **Point the backend at it** via env:
   `DUITKU_BASE_URL=http://localhost:9090`, `DUITKU_MERCHANT_CODE=DEMO`, `DUITKU_API_KEY=secretkey`,
   `RDASH_BASE_URL=http://localhost:9090/v1`, `MAIL_DRIVER=http`,
   `MAIL_HTTP_URL=http://localhost:9090/mail/send`; seed cPanel/DA server rows
   with `hostname localhost`, `port 9090`, `use_ssl false`.
3. **Reset between specs**: `POST /mock/reset` gives every test a clean slate.
4. **Pay an invoice**: after checkout, the backend's `paymentUrl` points at
   `GET /payment/<reference>` — Playwright opens it and clicks `#pay-now`.
   The mock then POSTs the signed callback to the backend webhook
   (`/api/v1/webhooks/duitku`) and redirects the browser to the `returnUrl`,
   exactly like real Duitku. Idempotency can be tested by re-POSTing
   `/mock/duitku/pay/<reference>` (each POST re-sends the callback).
5. **Assert provisioning** by querying WHM/DA state (`accountsummary`,
   `CMD_API_SHOW_USER_CONFIG`) or exercise failure paths with username `failme`.
6. **Assert email** (verification links, invoices, receipts) via
   `GET /mail/messages?to=<address>` — extract tokens/links from `html`.
7. **Expired-payment / reconciliation flows**: `POST /mock/duitku/expire/<ref>`
   then let the backend's reconcile cron call Check Transaction (`02`).

## Development

```sh
go build ./... && go vet ./... && go test ./...   # quality gate
go test -race -cover ./...                        # 91%+ statement coverage
```
