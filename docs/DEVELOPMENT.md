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

### Authorization checklist for new endpoints

**`WithFirmContext`'s RLS scoping is not a membership check.** It confines a query to rows whose `firm_id` matches whatever `firmID` value the caller passed in — nothing more. If that `firmID` came from a URL path, query parameter, or request body (i.e. from the caller, not derived from something already proven to belong to them), RLS alone lets any authenticated user read or write that firm's data just by supplying its real ID. This was a real, exploitable bug (found via PR #6's own end-to-end verification, not by design) in `CurrentState`, `ListInstances`, and `LookupDefinitionByKey`, and a milder existence-oracle variant in `CreateInstance`/`ExecuteTransition` — all now fixed. See Open Points item 37 for the full audit.

Before adding any function or HTTP handler that accepts a firm-scoping ID (`firmID` today; watch for the same shape on any future tenant-scoping identifier) from the caller:

1. **Does it read or write a firm-scoped table at all?** If not (e.g. it only touches the global `firms`/`users`/`permissions` tables), this checklist doesn't apply.
2. **Is the membership check already implied?** `internal/permission.Has(ctx, tx, firmID, userID, permissionKey)` requires a real `user_firm_roles` row for `(userID, firmID)` to return `true` — a function that only proceeds when `Has` succeeds is already protected, no separate check needed (`CreateInstance`'s and `ExecuteTransition`'s actual state mutations are covered this way).
3. **If not, call `internal/permission.IsMember(ctx, tx, firmID, userID)` first** — before the first `SELECT`/`INSERT`/`UPDATE` against any firm-scoped table, inside the same `WithFirmContext` transaction, not after. `CurrentState`/`ListInstances`/`LookupDefinitionByKey` are the reference examples.
4. **Prefer the same error for "doesn't exist" and "you're not a member."** `writeEngineError`'s convention (404 either way) means a non-member can't distinguish a firm/instance/definition that's genuinely missing from one that's real but not theirs — don't leak that distinction with a different status code or message.
5. **Write the regression test as a real cross-tenant attempt**, not just a mismatched-context one: seed two firms, a real user who is a member of firm B only, and assert that supplying firm A's genuine ID gets rejected — a test that only checks "wrong context, made-up ID" would not have caught the original bug (see `TestRLS_InstanceIsNotVisibleFromAnotherFirm`'s `firmA`-with-`userB` case, and `TestCreateInstance_NonMemberGetsNotFoundNotPermissionDenied`/`TestExecuteTransition_NonMemberGetsNotFoundNotPermissionDenied`, for the pattern).
6. **Verify live, not just via the Go test suite**, before calling it done: start the real server (a stale `go run` background process can otherwise make you think a fix didn't take, or did, when it's testing old code — rebuild explicitly and confirm the PID/port), create two real firms through the actual wizard flow, and `curl` the cross-tenant attempt with a real bearer token.

## Workflow engine

`internal/workflow` implements Vision §3's "graph-based state machine": a business process is a set of rows (`workflow_definitions`, `workflow_states`, `workflow_transitions`) rather than Go code written per workflow - a 7-step manufacturing flow is just a bigger `DefinitionSpec` value passed to `DefineWorkflow`, not a new code path.

- **`DefineWorkflow`** validates a `DefinitionSpec` (exactly one initial state, no dangling state references, unique (from-state, action) pairs) and writes it as rows in one `WithFirmContext` transaction, upserting any permission keys it references into the global `permissions` catalog from `migrations/0001_core_schema.up.sql` along the way - reusing that catalog is the Never-Violate Rule 7 "permission tag" mechanism, not a new one. It's a thin wrapper around **`DefineWorkflowTx`**, the same logic against an already-open transaction, for callers that need workflow provisioning to be one step inside a larger atomic operation.
- **Self-action auto-grant**: `DefineWorkflowTx` takes a `granteeRoleID` - when a firm's own action provisions a new permission key (here, by defining a workflow), that permission is granted to the role that triggered the action, in the same transaction, instead of being left as a separate step every caller has to remember. Pass `uuid.Nil` to skip granting - what the pool-based `DefineWorkflow`/`SeedStockToSaleWorkflow` do, since those exist for fixtures/tests that only want a workflow's shape to exist, not a real grant against a real acting role (several workflow-engine tests deliberately seed a role with nothing granted, to test permission enforcement - auto-granting through those entry points would break that). There is no other kind of permission bypass anywhere in this system: an "owner" role (see the wizard section below) holds what it holds because of real, auditable `role_permissions` rows this mechanism wrote, never an implicit superuser check.
- **`internal/permission.Has`** is the one place a permission check happens: does the caller hold `permissionKey` through any role assigned to them in this firm. Any future module gating an action behind a permission should use this, not a bespoke check.
- **`internal/permission.IsMember`** reports whether the caller belongs to `firmID` *at all*, through any role. This exists because `WithFirmContext`'s RLS scoping alone is not a membership check: it confines a query to rows whose `firm_id` matches whatever ID the caller passed in, which is trivially satisfiable by any authenticated user who simply supplies a real firm ID that isn't their own. `Has` already carries this guarantee implicitly (its `user_firm_roles` join returns nothing for a non-member), but the read-only functions below originally had no equivalent check - a real cross-firm data leak, found and closed while building the stock list feature (see `TestRLS_InstanceIsNotVisibleFromAnotherFirm` and the sibling `_RLS` tests for `ListInstances`/`LookupDefinitionByKey`, which include the exact non-member-supplies-a-real-ID regression). Every function below now checks it before returning anything.
- **`CreateInstance`** starts a new instance in a definition's initial state, gated by the definition's `create_permission_key`. This is "add stock" in this PR's concrete workflow - modeled as instance creation, not a transition (creation has no from-state), but enforced and audited identically to one. `permission.Has` already implies membership for the actual write, but `CreateInstance` also checks `IsMember` first, before its own `workflow_definitions` lookup - the Open Points item 37 audit found that lookup let a non-member learn a definition existed (via a 403 instead of a 404) before being denied; a minor metadata oracle, not a data leak, but closed for consistency with every other function here.
- **`ExecuteTransition`** moves an instance along an edge in the graph (e.g. "record a sale"), gated by that transition's own `permission_key`, all inside one `WithFirmContext` transaction so the permission check and the state mutation can't race. Same `IsMember`-first treatment as `CreateInstance`, for the same reason (its `workflow_instances`/`workflow_transitions` lookups otherwise ran before the implied-by-`Has` membership check).
- Every `CreateInstance`/`ExecuteTransition` call writes exactly one row to `audit_log` - a general-purpose, not workflow-specific, data-change audit trail (Vision §3; view/read-level logging is future work). The workflow engine is this table's first writer; later modules (inventory, sales, ...) are expected to write to the same table, not invent their own.
- **`stock_to_sale.go`** is the concrete first instance: `in_stock` (initial) → `sold` (terminal) via the one transition `record_sale`. `SeedStockToSaleWorkflow(ctx, pool, firmID)` instantiates it for a firm - called by the firm-creation wizard (see below) for every new firm. Its instance payload convention (not an engine-level concept - `payload` is opaque `jsonb` to the engine) is `{"item": "<name>", "quantity": <number>}` at creation time, established by the stock UI (`web/src/app/[locale]/stock`) and its tests; a future workflow could use an entirely different payload shape without touching the engine.
- **`CurrentState`** and **`ListInstances`** (the latter lists every instance of one definition within a firm - the stock list page's data source) both now take `userID` and check `permission.IsMember` before reading anything. Neither is filtered by the caller's own *permissions* though - available actions are listed regardless of whether the caller could actually execute them; that enforcement happens at `ExecuteTransition` time, same as before.
- **`LookupDefinitionByKey`** resolves a well-known key (e.g. `stock_to_sale`, see `StockToSaleKey`) to its firm-scoped `workflow_definitions` row, for callers (the stock page) that only know the key, not the UUID - also `IsMember`-checked.
- **`AvailableAction.PermissionKey`**: `CurrentState` and `ListInstances` both include the permission key each listed action is actually gated by, so a UI rendering an action button can carry the real Never-Violate Rule 7 permission tag instead of a hardcoded duplicate of the constant `ExecuteTransition` checks.

HTTP surface (mirrors `internal/identity`'s `/api/me/firms/{firmID}/...` path-scoping - see `docs/api/openapi.yaml` for full request/response shapes):

- `GET /api/firms/{firmID}/workflow-definitions?key={key}` — resolve a well-known workflow key to its definition ID
- `POST /api/firms/{firmID}/workflow-definitions/{definitionID}/instances` — start an instance ("add stock")
- `GET /api/firms/{firmID}/workflow-definitions/{definitionID}/instances` — list every instance of a definition (the stock list)
- `GET /api/firms/{firmID}/workflow-instances/{instanceID}` — read current state and structurally available next actions (membership-checked; not filtered by the caller's own permissions - enforcement happens when a transition is actually executed)
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

## Stock + Sale UI

`web/src/app/[locale]/stock` is the last piece of the first vertical slice (auth → firm creation → stock/sale operations): a screen for viewing a firm's Stock In -> Sale instances and recording a sale, built entirely on the existing workflow engine and firm-creation wizard - no new backend concepts, only the two read endpoints (`ListInstances`, `LookupDefinitionByKey`) and the `AvailableAction.PermissionKey` field described above.

- **`page.tsx`** is a server component: resolves the caller via `lib/me.ts`'s `fetchMe` (same cookie-to-Bearer pattern as every other page), redirects to `/` if unauthenticated or to `/wizard` if the caller has zero firm memberships (same rule the homepage applies), then resolves the `stock_to_sale` definition and its instances server-side via `lib/workflow.ts`. There is no firm-switcher UI yet, so it uses the caller's first firm membership - a simplifying assumption the homepage's firm list already implicitly carries; a real switcher is future work, not in this slice.
- **`StockList.tsx`** is the client component that renders the table and the "Sell" button. Each button carries `data-permission-key={action.permissionKey}` - the frontend half of the Never-Violate Rule 7 "permission tag" mechanism (Vision §3 Permission Audit Mode expects every permission-gated control to carry one, even before the audit-mode UI itself exists to read it). No client-side permission check gates the button's availability or the click itself: per this feature's explicit instruction not to add a bespoke auth check, the backend's `permission.Has` inside `ExecuteTransition` is the only place that decision is made - a caller without `record_sale` gets a real 403 back, surfaced as an inline error, not silently prevented from clicking.
- **`web/src/app/api/stock/[instanceId]/sell/route.ts`** proxies the sell action through to the backend's existing transition endpoint (`POST .../workflow-instances/{instanceId}/transitions/record_sale`) - same cookie-to-Bearer-token pattern as `api/me` and `api/wizard/nodes/*`. It forwards whatever `firmId` the client sends without validating that the caller actually belongs to it; that's safe, not a hole, because the backend endpoint it calls is itself membership- and permission-checked (see `internal/permission.IsMember`/`Has` above) - trusting a path/body parameter and letting the database enforce isolation is the same pattern every other firm-scoped endpoint in this codebase already uses (e.g. `/api/me/firms/{firmID}/role`).
- Item name/quantity are read from the instance `payload` under the keys `item`/`quantity` - a convention this UI establishes for the Stock In -> Sale workflow specifically (payload is opaque `jsonb` to the engine itself, see `stock_to_sale.go`'s note above). "Add stock" UI (creating new instances) is out of scope for this slice, same as the wizard's manufacturing branch was for PR 6/7 - instances for manual testing are created directly against the existing `CreateInstance` endpoint.
- `state.name`/`action.name` (e.g. "In Stock", "Sold", "Record Sale") are rendered as-is from the backend, the same way the wizard renders a firm's role name as-is: they're workflow data (`workflow_states.name`/`workflow_transitions.name`), not UI copy baked into this component, so they sit outside the `Stock` i18n namespace - which does cover every actual piece of UI chrome (headers, button label, empty/error states).

### Running the permission tests

`internal/permission/permission_integration_test.go` covers `Has` and `IsMember` directly - real Postgres, same convention as everywhere else:

```
export ZONARYOS_TEST_ADMIN_DATABASE_URL=postgres://zonaryos:zonaryos@localhost:5432/zonaryos?sslmode=disable
export ZONARYOS_TEST_APP_DATABASE_URL=postgres://zonaryos_app:zonaryos_app@localhost:5432/zonaryos?sslmode=disable
make migrate
go test ./internal/permission/... -v
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
