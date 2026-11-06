# WHCMS — Frontend

SvelteKit 2 + Svelte 5 (runes) + TypeScript strict + Tailwind CSS v4 + `@sveltejs/adapter-node`.
The BFF per `docs/CONTRACTS.md §13`: the browser never calls the Go API
directly — every `load`/form action talks to it server-side via `apiFetch`.
All feature areas are implemented: public portal + store, client area
(Twenty-One `.ca` theme), and admin area (HostPanel `hp-*` theme) — see
`docs/FRONTEND.md` and `CLAUDE.md` §4.

## Layout

- `src/hooks.server.ts` — session bootstrap: reads `access_token`/`refresh_token` httpOnly cookies,
  populates `event.locals.user` via `GET /api/v1/auth/me`, auto-refreshes once on 401 (rotated cookies).
- `src/lib/server/api.ts` — `apiFetch(event, path, opts)` typed wrapper (Bearer + envelope unwrap
  into `{ data, meta, error }`), `ApiError`, `unwrap()`. Pass paths with the `/api/v1` prefix.
- `src/lib/server/session.ts` — `setSessionCookies` / `clearSessionCookies`.
- `src/lib/i18n/` — `id` (default) + `en` dictionaries, rune-based `t(key, params)` helper,
  `locale` cookie persistence.
- `src/lib/components/` — WHMCS-like design system: `DataTable`, `StatCard`, `StatusBadge`, `Modal`,
  `ConfirmDialog`, `FormField`, `Tabs`, `Toast` (+ store in `src/lib/stores/toast.svelte.ts`),
  `Breadcrumb`, `EmptyState`, `MoneyText`, `DateText`, `Pagination`, `LoadingButton`, `Alert`;
  theme shells in `hp/` (admin HostPanel) and `ca/` (client Twenty-One). All use Svelte 5 runes
  (`$props()`, `$state`, `$derived`).
- Route groups: `(public)` Twenty-One shell (`/login` works against the auth contract),
  `(client)` guarded by `locals.user`, `(admin)/admin` HostPanel shell guarded by role
  `admin|staff`. `/logout` clears the session.

## Environment

The frontend reads no env file of its own: `make up` (repo root) loads the root `.env` and
passes the frontend only these two variables — never the backend's secrets:

- `API_URL` (server-side, default `http://localhost:8080`)
- `PUBLIC_APP_NAME` (default `WHCMS`)

(`.env.example` at the repo root lists them; `.env.testing` sets them for the e2e stack.)

## Commands

```sh
npm install
npm run dev        # http://localhost:5173
npm run check      # svelte-check
npm run build      # production build (adapter-node → build/)
npm run preview
npm run test:e2e   # Playwright (tests/e2e), boots the dev server itself
```
