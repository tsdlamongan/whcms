## What & why

<!-- What does this PR change, and what problem does it solve? Link issues. -->

## How it was tested

- [ ] `make lint` and `make cover` pass (coverage ≥ 90%)
- [ ] `cd frontend && npm run check` and `npm run build` are clean
- [ ] `make e2e` passes (new/changed flows have their own Playwright spec)
- [ ] Drove the change end-to-end against the local stack (`make up`)

## Checklist

- [ ] Commits follow Conventional Commits (`type(scope): subject`)
- [ ] Envelope / enums / error codes / `int64` money rules honored (`docs/CONTRACTS.md`)
- [ ] Migrations are forward-only; `.env.example` updated if a variable was added
- [ ] `data-testid`s, form `action`s, and field `name`s preserved (or specs updated here)
- [ ] Admin UI uses `hp-*` (HostPanel); client/public uses `.ca` (Twenty-One) + `t()` i18n
- [ ] No secrets in code, config defaults, or logs

## Screenshots (UI changes)

<!-- Before/after, or a short clip. -->
