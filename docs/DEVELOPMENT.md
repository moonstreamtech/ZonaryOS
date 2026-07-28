# Development Setup

This document covers local setup for the ZonaryOS application (backend + frontend). For product scope and architecture decisions, see `docs/VISION.md`. For working rules, see `CLAUDE.md`.

## Prerequisites

- Go 1.24+
- Node.js 22+
- Docker (for Postgres and Keycloak)

## Backend

```
cp .env.example .env   # adjust as needed
make build
make test
make run                # starts the server on :8080
```

`GET /healthz` returns `{"status":"ok"}` once the server is up.

## Frontend

```
cd web
cp .env.example .env.local
npm install
npm run dev              # starts the frontend on :3000
```

The frontend serves locale-prefixed routes (`/en`, `/tr`) via `next-intl`; `/` redirects to the default locale. All UI copy comes from `web/messages/{locale}.json` — no hardcoded strings (see `CLAUDE.md`, Never-Violate Rule 4).

## Local infrastructure (Postgres, Keycloak)

```
make dev-up      # starts Postgres (:5432) and Keycloak (:8081) via docker-compose
make dev-down     # stops them
```

Keycloak wiring (realm configuration, JWT validation) lands in a subsequent PR of the vertical slice.

## Database schema and Row-Level Security

Migrations live in `migrations/*.sql` (embedded into the binary via `migrations/embed.go`) and are applied with:

```
make migrate    # runs `cmd/migrate`, using ZONARYOS_MIGRATE_DATABASE_URL
```

`ZONARYOS_MIGRATE_DATABASE_URL` must point at a privileged/owner role (the docker-compose Postgres superuser). The migration also creates an unprivileged `zonaryos_app` login role — this is the role the *application* connects as, and the only role RLS policies actually restrict (the owner/superuser bypasses RLS by design, so migrations must never run as the same role the server uses).

Every tenant-scoped table (`roles`, `role_permissions`, `user_firm_roles`) has Row-Level Security enabled, keyed off the `app.current_firm_id` Postgres session setting. Application code must only touch these tables through `internal/platform/db.WithFirmContext`, which sets that context per transaction — never via a manual `firm_id = ?` filter instead (Never-Violate Rule 3). `firms` and `users` are global (not firm-scoped) tables: a user can belong to several firms via `user_firm_roles`.

### Running the RLS integration tests

These require a real Postgres instance (e.g. `make dev-up`) and are skipped otherwise:

```
export ZONARYOS_TEST_ADMIN_DATABASE_URL=postgres://zonaryos:zonaryos@localhost:5432/zonaryos?sslmode=disable
export ZONARYOS_TEST_APP_DATABASE_URL=postgres://zonaryos_app:zonaryos_app@localhost:5432/zonaryos?sslmode=disable
make migrate
go test ./internal/platform/db/... -v
```
