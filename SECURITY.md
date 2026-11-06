# Security Policy

WHCMS handles billing data, payment-gateway callbacks, and hosting-panel
credentials, so we take security reports seriously.

## Supported versions

Security fixes land on the `main` branch. Until versioned releases exist,
always deploy from the latest `main`.

## Reporting a vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Instead, use one of these private channels:

1. **GitHub private vulnerability reporting** (preferred): go to the
   repository's *Security* tab → *Report a vulnerability*. This opens a
   private advisory only the maintainers can see.
2. If that is unavailable, open a regular issue that only asks for a private
   contact channel — do not include any vulnerability details in it.

Please include:

- A description of the issue and its impact (what an attacker can do).
- Steps to reproduce, ideally against a local `make up` stack.
- Affected component (API, worker, frontend BFF, mockserver, deploy files).

We aim to acknowledge reports within 72 hours and to publish a fix advisory
once a patch is available. Please give us reasonable time to remediate before
public disclosure.

## Scope notes

- The **mockserver** (`mockserver/`) is a development/test-only component and
  must never be exposed in production; findings against it are out of scope
  unless they affect the real integrations.
- Dev defaults in `deploy/docker-compose.yml`, `.env.testing.example`, and the
  seeded `admin@e2e.test` account are intentionally public and for local
  development only — reports that boil down to "the dev defaults are known"
  are out of scope. Production deployments must set their own secrets
  (`JWT_SECRET`, `APP_ENCRYPTION_KEY`, database/S3 credentials, gateway and
  registrar keys).

## Hardening expectations for deployers

- Set strong values for `JWT_SECRET` and `APP_ENCRYPTION_KEY` (32 random
  bytes, base64) — the application refuses weak/dev values only where it can
  detect them, and cannot protect data encrypted under a leaked key.
- Terminate TLS in front of both the frontend (`:3000`) and the API (`:8080`)
  and keep Postgres/Redis/RustFS unreachable from the public internet.
- Rotate the Duitku and RDash credentials if you suspect a leak; both can be
  updated live from the admin settings UI.
