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

`make dev-up` automatically imports `deploy/keycloak/zonaryos-realm.json` into Keycloak on startup (via `start-dev --import-realm`, mounted read-only into the container's `data/import` directory). This is the "realm as code" mechanism for this repo - a plain realm export JSON, not Terraform: it needs no extra tool or apply step, and Keycloak applies it automatically the moment the container starts, matching the zero-extra-steps `make dev-up` workflow the rest of local dev already uses.

The imported realm (`zonaryos`) contains:
- **Client `zonaryos-web`**: public client, Authorization Code + PKCE (S256), redirect URI `http://localhost:3000/*` - used by the frontend's login flow (see below). Direct Access Grants (the OAuth2 password grant) is also enabled on this client, but **only as a local dev/test convenience** so integration tests and quick `curl` checks can obtain a token without a browser - this is not a production security posture and this realm is never used outside local dev (production Keycloak/realm configuration is managed separately, outside this repo).
- **Dev user** `dev@zonaryos.local` / `zonaryos-dev` - for manually exercising the login flow at `http://localhost:3000/api/auth/login`.

**Keycloak's role in this system is identity only.** It authenticates a user and issues a token asserting who they are (`sub`, `email`, `name`); it holds no ZonaryOS authorization data. Which firms a user belongs to and what role they hold in each lives entirely in ZonaryOS's own tables (`users`, `firms`, `roles`, `user_firm_roles` - see below) - a firm's dynamically-created custom roles (Vision §3's parametric permission system) are never synced into Keycloak.

### Backend: verifying tokens and resolving identity

`internal/identity`:
- `Verifier` (`verifier.go`) discovers the realm's OIDC configuration and verifies a bearer token's signature, issuer, expiry, and `azp` (authorized party) claim against the configured client ID.
- `Middleware` (`middleware.go`) enforces this on any route it wraps, attaching the resulting `Identity` to the request context.
- `ResolveOrCreateUser` (`user.go`) upserts the verified identity into the global `users` table by `keycloak_subject`.
- `Memberships` (`membership.go`) lists every firm a user belongs to, and `RoleInFirm` resolves their role within one chosen firm by opening a normal `db.WithFirmContext` transaction - proving the full chain from a verified token to an RLS-scoped database session.

Two HTTP endpoints exercise this end to end: `GET /api/me` (identity + firm memberships) and `GET /api/me/firms/{firmID}/role` (role within one firm - 403s if the caller isn't actually a member, enforced by RLS, not application logic).

Running the backend now requires `ZONARYOS_DATABASE_URL` (the `zonaryos_app` DSN), `ZONARYOS_OIDC_ISSUER_URL` (e.g. `http://localhost:8081/realms/zonaryos`), and `ZONARYOS_OIDC_CLIENT_ID` (default `zonaryos-web`) - see `.env.example`.

### Frontend: login flow

`web/src/app/api/auth/{login,callback,logout}` implement Authorization Code + PKCE directly against Keycloak (no auth framework dependency, to avoid pulling in one of uncertain compatibility with this Next.js version for what's still a minimal proof of the flow): `/api/auth/login` redirects to Keycloak with a PKCE challenge and `state`, stored in short-lived httpOnly cookies; `/api/auth/callback` validates `state`, exchanges the code for a token, and stores the access token in an httpOnly `zonaryos_session` cookie (never readable by client-side JS). `web/src/app/api/me/route.ts` and the homepage forward that token as a Bearer token to the Go backend's `/api/me`. Requires `ZONARYOS_KEYCLOAK_ISSUER_URL` and `ZONARYOS_KEYCLOAK_CLIENT_ID` in `web/.env.local` (see `web/.env.example`).

Session storage here is a single httpOnly cookie holding the raw access token, sized for proving the flow end-to-end - refresh-token rotation and a more durable session strategy are follow-up work, not in this slice.

### Running the identity/Keycloak tests

`internal/identity`'s token-verification tests (`verifier_test.go`) run against a fake in-process OIDC provider and need nothing external. `user_membership_integration_test.go` needs a real Postgres, same convention as the RLS tests below. `keycloak_integration_test.go` additionally needs a **live Keycloak** (`make dev-up`):

```
export ZONARYOS_TEST_ADMIN_DATABASE_URL=postgres://zonaryos:zonaryos@localhost:5432/zonaryos?sslmode=disable
export ZONARYOS_TEST_APP_DATABASE_URL=postgres://zonaryos_app:zonaryos_app@localhost:5432/zonaryos?sslmode=disable
export ZONARYOS_TEST_KEYCLOAK_ISSUER_URL=http://localhost:8081/realms/zonaryos
export ZONARYOS_TEST_KEYCLOAK_CLIENT_ID=zonaryos-web
export ZONARYOS_TEST_KEYCLOAK_USERNAME=dev@zonaryos.local
export ZONARYOS_TEST_KEYCLOAK_PASSWORD=zonaryos-dev
make dev-up
make migrate
go test ./internal/identity/... -v
```

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
