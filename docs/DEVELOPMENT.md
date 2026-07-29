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

### Fallback: no Docker registry access

Some sandboxed CI/agent environments allow general internet/GitHub access but block Docker registries (Docker Hub, `quay.io`) outright - `make dev-up` will fail there with an image pull error, even though nothing is actually wrong with the compose file or the realm config. In that specific situation only, use:

```
make dev-up-standalone      # starts Postgres natively + Keycloak as a standalone JVM process
make dev-down-standalone    # stops the Keycloak process (Postgres is left running, as a shared system service)
```

`scripts/dev-up-standalone.sh` starts Postgres via the local `postgresql` service (same as this repo's RLS integration tests already assume - see below) and downloads Keycloak's standalone distribution from **GitHub Releases** (`github.com/keycloak/keycloak`, not a Docker registry - a genuinely different network path) into `.zonaryos/keycloak-standalone/` (gitignored), then runs it with `bin/kc.sh start-dev --import-realm`, importing the exact same `deploy/keycloak/zonaryos-realm.json` the Docker path uses. Both paths produce the same realm/client/user, verified identically by `internal/identity`'s tests - which path started Keycloak doesn't matter to the application.

Prefer `make dev-up` whenever Docker registries are reachable; this is a narrow fallback for one specific failure mode, not a general Docker replacement.

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

`internal/identity`'s token-verification tests (`verifier_test.go`) run against a fake in-process OIDC provider and need nothing external. `user_membership_integration_test.go` needs a real Postgres, same convention as the RLS tests below. `keycloak_integration_test.go` additionally needs a **live Keycloak** (`make dev-up`, or `make dev-up-standalone` where Docker registries are blocked - see above; both were verified to produce an identical, working realm):

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

## Workflow engine

`internal/workflow` implements Vision §3's "graph-based state machine": a business process is a set of rows (`workflow_definitions`, `workflow_states`, `workflow_transitions`) rather than Go code written per workflow - a 7-step manufacturing flow is just a bigger `DefinitionSpec` value passed to `DefineWorkflow`, not a new code path.

- **`DefineWorkflow`** validates a `DefinitionSpec` (exactly one initial state, no dangling state references, unique (from-state, action) pairs) and writes it as rows in one `WithFirmContext` transaction, upserting any permission keys it references into the global `permissions` catalog from `migrations/0001_core_schema.up.sql` along the way - reusing that catalog is the Never-Violate Rule 7 "permission tag" mechanism, not a new one. It's a thin wrapper around **`DefineWorkflowTx`**, the same logic against an already-open transaction, for callers that need workflow provisioning to be one step inside a larger atomic operation.
- **Self-action auto-grant**: `DefineWorkflowTx` takes a `granteeRoleID` - when a firm's own action provisions a new permission key (here, by defining a workflow), that permission is granted to the role that triggered the action, in the same transaction, instead of being left as a separate step every caller has to remember. Pass `uuid.Nil` to skip granting - what the pool-based `DefineWorkflow`/`SeedStockToSaleWorkflow` do, since those exist for fixtures/tests that only want a workflow's shape to exist, not a real grant against a real acting role (several workflow-engine tests deliberately seed a role with nothing granted, to test permission enforcement - auto-granting through those entry points would break that). There is no other kind of permission bypass anywhere in this system: an "owner" role (see the wizard section below) holds what it holds because of real, auditable `role_permissions` rows this mechanism wrote, never an implicit superuser check.
- **`internal/permission.Has`** is the one place a permission check happens: does the caller hold `permissionKey` through any role assigned to them in this firm. Any future module gating an action behind a permission should use this, not a bespoke check.
- **`CreateInstance`** starts a new instance in a definition's initial state, gated by the definition's `create_permission_key`. This is "add stock" in this PR's concrete workflow - modeled as instance creation, not a transition (creation has no from-state), but enforced and audited identically to one.
- **`ExecuteTransition`** moves an instance along an edge in the graph (e.g. "record a sale"), gated by that transition's own `permission_key`, all inside one `WithFirmContext` transaction so the permission check and the state mutation can't race.
- Every `CreateInstance`/`ExecuteTransition` call writes exactly one row to `audit_log` - a general-purpose, not workflow-specific, data-change audit trail (Vision §3; view/read-level logging is future work). The workflow engine is this table's first writer; later modules (inventory, sales, ...) are expected to write to the same table, not invent their own.
- **`stock_to_sale.go`** is the concrete first instance: `in_stock` (initial) → `sold` (terminal) via the one transition `record_sale`. `SeedStockToSaleWorkflow(ctx, pool, firmID)` instantiates it for a firm - a fixture/provisioning-level stand-in until the wizard (a later PR) can define workflows like this interactively. There is no HTTP endpoint or automatic hook that calls this yet; it's called directly by tests and would be called by firm-provisioning code once that exists.

HTTP surface (mirrors `internal/identity`'s `/api/me/firms/{firmID}/...` path-scoping - see `docs/api/openapi.yaml` for full request/response shapes):

- `POST /api/firms/{firmID}/workflow-definitions/{definitionID}/instances` — start an instance ("add stock")
- `GET /api/firms/{firmID}/workflow-instances/{instanceID}` — read current state and structurally available next actions (not filtered by the caller's own permissions - enforcement happens when a transition is actually executed)
- `POST /api/firms/{firmID}/workflow-instances/{instanceID}/transitions/{actionKey}` — execute a transition ("record a sale")

## Firm-creation wizard

`internal/wizard` implements Vision §3's "Self-Configuring Infrastructure": a newly-authenticated Keycloak user with zero firm memberships (detected via `internal/identity.Memberships`, PR 3's firm-discovery mechanism) is routed into this wizard instead of the normal app.

- **`tree.go`** models the wizard as an actual decision tree, not a hardcoded form: a `Node` is either a question (with `Answers`, each pointing at the next `Node`), a terminal action, or a terminal placeholder. This slice populates exactly one root question, `"do you manufacture?"` - `"no"` reaches the `create_default_firm` action; `"yes"` reaches a placeholder (`manufacturingComingSoon`) rather than a built-out manufacturing flow, which is explicitly out of scope here. A second question later nests into this same structure; nothing about `Lookup`/`Answer` needs to change. `Node` fields carry i18n message keys (not literal text) for anything user-facing, matching Never-Violate Rule 4.
- **`firm.go`**'s `CreateDefaultFirm` is the wizard's one implemented action: it creates the firm, a default `owner` role, the caller's membership in it, and seeds the firm with the Stock In -> Sale workflow via `workflow.SeedStockToSaleWorkflowTx` (reused as-is, not duplicated) - all inside one hand-managed transaction. `internal/platform/db.WithFirmContext` can't be used here since it needs a `firmID` up front and the firm doesn't exist yet; this function sets `app.current_firm_id` itself, once the new firm's ID is known, before touching any RLS-protected table - the write-side counterpart to the read-side bootstrap `migrations/0002_user_scoped_discovery.up.sql` solved for firm discovery. One `audit_log` row is written for the firm's creation, same table/shape the workflow engine already writes to.
- **Default role's permissions**: `CreateDefaultFirm` passes its new `owner` role's ID into `SeedStockToSaleWorkflowTx`, so `DefineWorkflowTx`'s self-action auto-grant (see the workflow engine section above) grants that role every permission the seeded workflow introduces - `CreateDefaultFirm` itself has no bespoke grant step. There is nobody else yet to grant permissions to the founder, so "everything this firm's own provisioning has introduced so far" is the deliberate default, not a restricted starter role - and it's not a superuser bypass either: see `migrations/0004_role_owner_flag.up.sql`'s `is_owner` column, which marks the role for Permission Audit Mode's future UI (Vision §3: "roles at the highest permission tier ... don't appear in these lists") but is never consulted by `permission.Has` or any other check.
- HTTP surface (not scoped under `/api/firms/{firmID}/...` - no firm exists yet, same bootstrap category as `/api/me`): `GET /api/wizard/nodes/{nodeKey}` reads a node; `POST /api/wizard/nodes/{nodeKey}/answer` submits an answer and, if it resolves to `create_default_firm`, creates the firm inline and returns it as `result` - see `docs/api/openapi.yaml`.

### Running the wizard tests

`internal/wizard/tree_test.go` is a pure unit test (tree traversal/lookup, no database). `internal/wizard/firm_integration_test.go` needs a real Postgres, same convention as the workflow engine tests above:

```
export ZONARYOS_TEST_ADMIN_DATABASE_URL=postgres://zonaryos:zonaryos@localhost:5432/zonaryos?sslmode=disable
export ZONARYOS_TEST_APP_DATABASE_URL=postgres://zonaryos_app:zonaryos_app@localhost:5432/zonaryos?sslmode=disable
make migrate
go test ./internal/wizard/... -v
```

### Running the workflow engine tests

`internal/workflow/spec_test.go` is a pure unit test (spec validation, no database). `internal/workflow/workflow_integration_test.go` needs a real Postgres, same convention as the RLS tests above:

```
export ZONARYOS_TEST_ADMIN_DATABASE_URL=postgres://zonaryos:zonaryos@localhost:5432/zonaryos?sslmode=disable
export ZONARYOS_TEST_APP_DATABASE_URL=postgres://zonaryos_app:zonaryos_app@localhost:5432/zonaryos?sslmode=disable
make migrate
go test ./internal/workflow/... -v
```

### Running the RLS integration tests

These require a real Postgres instance (e.g. `make dev-up`) and are skipped otherwise:

```
export ZONARYOS_TEST_ADMIN_DATABASE_URL=postgres://zonaryos:zonaryos@localhost:5432/zonaryos?sslmode=disable
export ZONARYOS_TEST_APP_DATABASE_URL=postgres://zonaryos_app:zonaryos_app@localhost:5432/zonaryos?sslmode=disable
make migrate
go test ./internal/platform/db/... -v
```

### Why `make test` passes `-p 1`

Several packages' integration tests (`internal/platform/db`, `internal/identity`, `internal/workflow`, ...) share the same real Postgres database and each `TRUNCATE`s the tables it needs a clean slate for at the start of every test. `go test ./...` runs different packages' test binaries concurrently by default - fine for packages that don't share state, but two packages truncating the same tables (`firms`, `users`, `roles`, ...) at the same moment can wipe out data a sibling package's test just relied on, causing spurious failures that have nothing to do with the code under test. `go test -p 1 ./...` (what `make test` runs) forces one package at a time, which is what actually matters here - not test speed.

If you ever see a failure in one integration test package only when running the full suite, and it passes cleanly in isolation (`go test ./internal/xyz/... -v`), this is almost certainly why - not a real regression.
