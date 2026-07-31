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
make dev-up      # starts Postgres (:5433 - see "Running the full stack in containers" below for why not :5432) and Keycloak (:8081) via docker-compose
make dev-down     # stops them
```

### Fallback: no Docker registry access

Some sandboxed CI/agent environments allow general internet/GitHub access but block Docker registries (Docker Hub, `quay.io`) outright - `make dev-up` will fail there with an image pull error, even though nothing is actually wrong with the compose file or the realm config. In that specific situation only, use:

```
make dev-up-standalone      # starts Postgres natively + Keycloak as a standalone JVM process
make dev-down-standalone    # stops the Keycloak process (Postgres is left running, as a shared system service)
```

`scripts/dev-up-standalone.sh` starts Postgres via the local `postgresql` service (a real, natively-running Postgres, on its OS default port `5432` - **not** `5433`, which is specific to `make dev-up`'s containerized Postgres, see "Running the full stack in containers" below for why they differ) and downloads Keycloak's standalone distribution from **GitHub Releases** (`github.com/keycloak/keycloak`, not a Docker registry - a genuinely different network path) into `.zonaryos/keycloak-standalone/` (gitignored), then runs it with `bin/kc.sh start-dev --import-realm`, importing the exact same `deploy/keycloak/zonaryos-realm.json` the Docker path uses. Both paths produce the same realm/client/user, verified identically by `internal/identity`'s tests - which path started Keycloak doesn't matter to the application. **If you're running the "Running the X tests" sections' `ZONARYOS_TEST_*_DATABASE_URL` exports below against `make dev-up-standalone` instead of `make dev-up`, use `5432` in place of the `5433` shown there.**

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
export ZONARYOS_TEST_ADMIN_DATABASE_URL=postgres://zonaryos:zonaryos@localhost:5433/zonaryos?sslmode=disable
export ZONARYOS_TEST_APP_DATABASE_URL=postgres://zonaryos_app:zonaryos_app@localhost:5433/zonaryos?sslmode=disable
export ZONARYOS_TEST_KEYCLOAK_ISSUER_URL=http://localhost:8081/realms/zonaryos
export ZONARYOS_TEST_KEYCLOAK_CLIENT_ID=zonaryos-web
export ZONARYOS_TEST_KEYCLOAK_USERNAME=dev@zonaryos.local
export ZONARYOS_TEST_KEYCLOAK_PASSWORD=zonaryos-dev
make dev-up
make migrate
go test ./internal/identity/... -v
```

## Running the full stack in containers

See `docs/OPEN_POINTS.md` item 34 for the deployment-target investigation this came out of. This section covers what actually exists today: a containerized backend + frontend, a local docker-compose stack that runs the whole thing (Postgres, Keycloak, backend, frontend) together, and (if/when the Developer provisions real Oracle access) a near-term dev/test deployment target. **None of this is a production commitment** - see the caveats below.

### Building the images

`Dockerfile` (backend, builds both `cmd/server` and `cmd/migrate`) and `web/Dockerfile` (frontend, Next.js standalone output) are both multi-stage and use only portable, multi-arch base images (`golang`, `node:22-alpine`, `gcr.io/distroless/static-debian12`) - nothing Oracle- or any other cloud-specific. Build for the current machine's architecture with:

```
docker compose build
```

or cross-build for the Oracle Ampere A1 target (arm64) explicitly:

```
docker buildx build --platform linux/arm64 -t zonaryos-backend:arm64 .
docker buildx build --platform linux/arm64 -t zonaryos-frontend:arm64 -f web/Dockerfile .
```

**Verify the architecture, don't assume it.** The backend `Dockerfile` cross-compiles Go (`CGO_ENABLED=0`, `GOOS`/`GOARCH` from Docker's `TARGETOS`/`TARGETARCH` build args) rather than needing to execute arm64 code during the build - but this only works correctly if `buildx` actually populates `TARGETARCH` for the requested `--platform`. Verified during this work: the plain default `docker` buildx driver silently produced an image whose manifest said `linux/arm64` but whose binary inside was still `x86-64` (`TARGETARCH` wasn't propagated) - a real, not hypothetical, failure mode. Use a `docker-container` driver builder (`docker buildx create --driver docker-container --use`) and confirm the output before trusting it:

```
docker create --name check <image> && docker cp check:/usr/local/bin/server /tmp/server-check && docker rm check
file /tmp/server-check   # must say "ELF 64-bit LSB executable, ARM aarch64" for the arm64 build
```

### Running the full stack locally

```
docker compose up -d
```

This builds and starts Postgres, Keycloak, a one-shot `migrate` service (applies migrations, then exits - `backend` waits for it via `depends_on: condition: service_completed_successfully`), the backend, and the frontend. Every service uses `network_mode: host` **deliberately**, not as a shortcut: the frontend's OIDC login flow (`web/src/app/api/auth/login` and `.../callback`) needs one issuer URL reachable both by the end user's real browser and by the frontend container's own server-side token exchange, and Keycloak's `start-dev` mode derives each issued token's `iss` claim from whichever host:port actually requested it - bridge-network service names (e.g. `keycloak:8080`) would be unreachable from a real browser. Host networking keeps every service reachable at `localhost:<port>` from every other service *and* the browser, matching the exact assumption every existing manual/CI E2E verification already made (`scripts/e2e_smoke_test.sh`, the CI E2E job) - so no env var values differ from the non-containerized `.env`/`web/.env.local` setup already documented above, **except Postgres's port**: this compose file's `postgres` service listens on `5433`, not Postgres's default `5432` - a real deploy target turned out to already run its own native (non-Docker) PostgreSQL on `5432`, and since host networking means Docker's `ports:` publishing doesn't apply (there's no bridge network to remap - the server process itself has to be told to listen elsewhere, which `command: ["postgres", "-p", "5433"]` on the `postgres` service does), the exact same collision could just as easily happen on a developer's own machine if it already runs a local Postgres. Because `make dev-up` (`docker compose up -d`, i.e. this file) is this repo's default/primary way to get "a real Postgres" for the test suites below, **every `ZONARYOS_TEST_*_DATABASE_URL`/`.env.example` connection string in this document now points at `5433` to match** - the one exception is `make dev-up-standalone`'s native system Postgres (see "Fallback: no Docker registry access" below), which is unaffected by any of this and stays on the OS's actual `5432`.

First boot: the backend's `identity.NewVerifier` discovers Keycloak's OIDC config at startup and fails fast if Keycloak isn't ready yet (`cmd/server/main.go` doesn't retry) - `backend` is configured `restart: on-failure:10` to ride out that race rather than needing a bespoke wait-for-it script. A few restarts on first `docker compose up` are expected, not a bug.

**A real, non-hypothetical bug found and fixed while proving this end-to-end**: Next.js's standalone-mode `server.js` always builds `request.url` from its own bind hostname (`HOSTNAME` env, defaulting to `0.0.0.0`), never from the incoming request's actual `Host` header - a known Next.js standalone-server limitation (`experimental.trustHostHeader` does not override it, since `server.js` unconditionally passes its own hostname to the server constructor). This broke the OAuth `redirect_uri` the login/callback/logout routes built via `new URL(path, request.url)` - it came out as `http://0.0.0.0:3000/...`, which Keycloak's registered client (`http://localhost:3000/*`) rejects. Fixed in `web/src/lib/requestOrigin.ts`, used by all three `web/src/app/api/auth/*` routes: builds the origin from the request's `Host`/`X-Forwarded-Host` header instead of `request.url`. Verified with a full interactive PKCE round-trip (not just the Direct Access Grant `scripts/e2e_smoke_test.sh` normally uses) against the containerized stack: real Keycloak login form submission -> real authorization code -> real token exchange -> correct `redirect_uri` -> session cookie set -> homepage rendered.

Verifying the stack works: `./scripts/e2e_smoke_test.sh` against the containerized services (same script CI already runs against the non-containerized ones):

```
BACKEND_URL=http://localhost:8080 FRONTEND_URL=http://localhost:3000 \
KEYCLOAK_ISSUER_URL=http://localhost:8081/realms/zonaryos ./scripts/e2e_smoke_test.sh
```

`docker compose down` stops and removes the containers (add `-v` to also drop the Postgres volume).

### Deploying to Oracle Cloud Always Free (near-term dev/test target only)

Per the item 34 interim decision: an Oracle Always Free Ampere A1 instance (currently 2 OCPU/12GB, arm64) is a **near-term dev/test deployment target, not a production commitment**. Nothing in `Dockerfile`, `web/Dockerfile`, or `docker-compose.yml` is Oracle-specific - the same artifacts run on any plain Linux host with Docker (a DigitalOcean droplet, a bare-metal box, etc.) with no changes.

**Provisioning is done**: the instance exists (`zonaryos.duckdns.org`, a shared box also running unrelated live services behind nginx), and a dedicated deploy SSH key is in place. **The real reverse-proxy deployment (making the stack reachable at `https://zonaryos.duckdns.org` instead of `localhost`, alongside those other sites) is a separate, more involved concern than just `docker compose up -d` - see `docs/DEPLOYMENT.md` for the full command-by-command sequence, the `docker-compose.prod.yml` overlay, and `deploy/nginx/zonaryos.conf`.** That document also covers the Oracle Cloud Security List (firewall) rules needed - only 443/80 open publicly, Postgres/backend/Keycloak's raw port never exposed.

**Caveats - so this is never mistaken for a production environment (see `docs/OPEN_POINTS.md` item 34):**
- **No SLA.** Always Free is a best-effort tier with no uptime guarantee.
- **No automatic backup.** The Postgres data volume lives only on that one instance's disk; nothing here schedules snapshots or off-instance backups.
- **Single region, single instance.** This does not implement Vision §4's active-passive/multi-region topology - it is one Postgres, one Keycloak, one backend, one frontend, all on one host.
- **Oracle can reclaim Always Free resources under capacity pressure.** The instance is not guaranteed to stay up or keep its allocation indefinitely.

None of this blocks using the instance for dev/test purposes - it just means nothing here should be pointed to by real customer data or treated as the answer to item 34's actual production deployment-target question, which remains open pending the Developer's decision (see item 34's open questions).

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
- **`LookupDefinitionByKey`** resolves a well-known key (e.g. `stock_to_sale`, see `StockToSaleKey`) to its firm-scoped `workflow_definitions` row, for callers that only know the key, not the UUID - also `IsMember`-checked.
- **`ListDefinitions`** lists every `workflow_definitions` row for a firm, ordered by name - the data source for the frontend's firm-level "Workflows" view (see the Application Frontend section below): a firm's second, third, ... workflow definition shows up there automatically, without a hardcoded frontend card per known workflow. Same `IsMember`-first treatment as every other read here.
- **`AvailableAction.PermissionKey`**: `CurrentState` and `ListInstances` both include the permission key each listed action is actually gated by, so a UI rendering an action button can carry the real Never-Violate Rule 7 permission tag instead of a hardcoded duplicate of the constant `ExecuteTransition` checks. `AvailableAction.Name` and `DefinitionInfo.CreatePermissionKey` (workflow_transitions.name / workflow_definitions.create_permission_key) exist for the same reason on the frontend's generic action buttons and "create instance" form - see the Application Frontend section.
- **`DefineWorkflowForFirm`** is `DefineWorkflow`'s HTTP-reachable counterpart - the actual point of this batch of work: until now, defining a new workflow was only reachable from Go fixtures (`SeedStockToSaleWorkflow`) or the firm-creation wizard, never by a firm itself. Owner-gated (`permission.IsMember` then `IsOwner`, same order every owner-gated function in this codebase checks) - defining a new workflow type is a structural firm decision, the same tier as firm creation itself. On success, the acting owner's own owner-flagged role is looked up and passed as `DefineWorkflowTx`'s `granteeRoleID` (the self-action auto-grant above), so the firm isn't left holding a workflow nobody can use yet. `spec.Validate()` failures map to `ErrInvalidSpec` (400); a `(firm_id, key)` collision (Postgres `23505`, caught via `pgconn.PgError`) maps to `ErrDefinitionKeyExists` (409) - both real 4xx responses, not a generic 500. This is strictly the state-machine definition mechanism (states/transitions/permission keys) - it has no conditional-logic concept; the Rule Engine (Open Points item 12) remains undesigned and untouched.

HTTP surface (mirrors `internal/identity`'s `/api/me/firms/{firmID}/...` path-scoping - see `docs/api/openapi.yaml` for full request/response shapes):

- `GET /api/firms/{firmID}/workflow-definitions` — with `?key={key}`, resolve a well-known workflow key to its definition ID; without it, list every workflow definition the firm has (`ListDefinitions`) - two modes on one path, dispatched by the query parameter's presence rather than a second route, since a path-segment variant collides with the sibling `{definitionID}/instances` pattern below at `net/http` `ServeMux` startup
- `POST /api/firms/{firmID}/workflow-definitions` — define a new workflow for the firm (`DefineWorkflowForFirm`, owner-only)
- `POST /api/firms/{firmID}/workflow-definitions/{definitionID}/instances` — start an instance ("add stock", or the equivalent "create" action for any other workflow definition)
- `GET /api/firms/{firmID}/workflow-definitions/{definitionID}/instances` — list every instance of a definition (the stock list, or any other workflow's instance list)
- `GET /api/firms/{firmID}/workflow-instances/{instanceID}` — read current state and structurally available next actions (membership-checked; not filtered by the caller's own permissions - enforcement happens when a transition is actually executed)
- `POST /api/firms/{firmID}/workflow-instances/{instanceID}/transitions/{actionKey}` — execute a transition ("record a sale", or any other transition on any workflow)

## Firm-creation wizard

`internal/wizard` implements Vision §3's "Self-Configuring Infrastructure": a newly-authenticated Keycloak user with zero firm memberships (detected via `internal/identity.Memberships`, PR 3's firm-discovery mechanism) is routed into this wizard instead of the normal app.

- **`tree.go`** models the wizard as an actual decision tree, not a hardcoded form: a `Node` is either a question (with `Answers`, each pointing at the next `Node`), a terminal action, or a terminal placeholder. This slice populates exactly one root question, `"do you manufacture?"` - `"no"` reaches the `create_default_firm` action; `"yes"` reaches a placeholder (`manufacturingComingSoon`) rather than a built-out manufacturing flow, which is explicitly out of scope here. A second question later nests into this same structure; nothing about `Lookup`/`Answer` needs to change. `Node` fields carry i18n message keys (not literal text) for anything user-facing, matching Never-Violate Rule 4.
- **`firm.go`**'s `CreateDefaultFirm` is the wizard's one implemented action: it creates the firm, a default `owner` role, the caller's membership in it, and seeds the firm with the Stock In -> Sale workflow via `workflow.SeedStockToSaleWorkflowTx` (reused as-is, not duplicated) - all inside one hand-managed transaction. `internal/platform/db.WithFirmContext` can't be used here since it needs a `firmID` up front and the firm doesn't exist yet; this function sets `app.current_firm_id` itself, once the new firm's ID is known, before touching any RLS-protected table - the write-side counterpart to the read-side bootstrap `migrations/0002_user_scoped_discovery.up.sql` solved for firm discovery. One `audit_log` row is written for the firm's creation, same table/shape the workflow engine already writes to.
- **Default role's permissions**: `CreateDefaultFirm` passes its new `owner` role's ID into `SeedStockToSaleWorkflowTx`, so `DefineWorkflowTx`'s self-action auto-grant (see the workflow engine section above) grants that role every permission the seeded workflow introduces - `CreateDefaultFirm` itself has no bespoke grant step. There is nobody else yet to grant permissions to the founder, so "everything this firm's own provisioning has introduced so far" is the deliberate default, not a restricted starter role - and it's not a superuser bypass either: see `migrations/0004_role_owner_flag.up.sql`'s `is_owner` column, which marks the role for Permission Audit Mode's future UI (Vision §3: "roles at the highest permission tier ... don't appear in these lists") but is never consulted by `permission.Has` or any other check.
- HTTP surface (not scoped under `/api/firms/{firmID}/...` - no firm exists yet, same bootstrap category as `/api/me`): `GET /api/wizard/nodes/{nodeKey}` reads a node; `POST /api/wizard/nodes/{nodeKey}/answer` submits an answer and, if it resolves to `create_default_firm`, creates the firm inline and returns it as `result` - see `docs/api/openapi.yaml`.

### Running the wizard tests

`internal/wizard/tree_test.go` is a pure unit test (tree traversal/lookup, no database). `internal/wizard/firm_integration_test.go` needs a real Postgres, same convention as the workflow engine tests above:

```
export ZONARYOS_TEST_ADMIN_DATABASE_URL=postgres://zonaryos:zonaryos@localhost:5433/zonaryos?sslmode=disable
export ZONARYOS_TEST_APP_DATABASE_URL=postgres://zonaryos_app:zonaryos_app@localhost:5433/zonaryos?sslmode=disable
make migrate
go test ./internal/wizard/... -v
```

## Firm renaming

`internal/firm` is deliberately the narrowest possible package: one function, `UpdateName`, renaming a firm - not a general firm-settings module. The `firms` table (`migrations/0001_core_schema.up.sql`) also has a jsonb `attributes` column with no mutation path anywhere; broader firm-metadata editing is real, undesigned future work (see the Open Points entry below), not something this package speculatively supports.

- **`UpdateName`** trims and rejects an empty name (`ErrInvalidName`) before touching the database - same "validate before opening a transaction" shape `wizard.CreateDefaultFirm` already uses for `firmName`. Owner-gated (`IsMember` then `IsOwner`), same tier as defining a new workflow. Writes one `audit_log` row (`entity_type` `"firm"`, `action` `"update"`) - same table/shape `CreateDefaultFirm`'s own firm-creation entry uses, just a different action value.
- HTTP surface: `PATCH /api/firms/{firmID}` — rename the firm (owner-only), body `{"name": "..."}`.

## Application Frontend

The frontend has gone through three shapes since the original stock vertical slice: (1) a bare homepage with one hardcoded link to a stock-specific page (`StockList.tsx`, `AddStockForm.tsx`, `StockHistory.tsx` - all since removed), (2) a firm dashboard/nav shell/audit-log-viewer/firm-switcher pass that kept the stock UI stock-specific, and (3) a genericization pass that replaced every stock-specific component with a generic one driven purely by what the backend returns for *any* workflow definition. This section describes the current (third) shape, plus this batch's additions (the workflow definition builder and firm-name editing); see git history for (1)/(2) if archaeology is ever needed.

**Navigation shell and firm context** (`web/src/components/Nav`, `web/src/lib/activeFirm.ts`, `web/src/lib/firmContext.ts`):

- **`NavShell.tsx`** is mounted once in the root layout (`app/[locale]/layout.tsx`) and renders on every authenticated page: brand, a firm switcher, links to Stock/Workflows/Settings/(owner-only) Audit Log, and Logout. It also hosts the Permission Audit Mode toggle (`AuditModeClient`), rather than that being invoked ad hoc from the layout directly.
- **`lib/activeFirm.ts`** replaces every hardcoded `me.firms[0]` assumption the original stock page and root layout carried. `pickActiveFirm` (pure, unit-tested in `activeFirm.test.ts`) resolves a requested firm ID against `me.firms`, falling back to the first membership for a missing/stale/non-member ID. `resolveActiveFirm` wraps it with the actual cookie read (`zonaryos_active_firm`, `next/headers`). The cookie is set by `POST /api/firm/switch` after validating the requested firm against the caller's own `fetchMe` result - a UI preference, not an authorization boundary; every backend call the resolved firm ID feeds into is independently membership-checked regardless of what the cookie says.
- **`lib/firmContext.ts`**'s `requireFirmContext(locale)` is the shared "who is this, which firm are they working in" resolution every firm-scoped page needs: reads the session cookie, calls `fetchMe`, redirects to `/` (unauthenticated) or `/wizard` (zero firm memberships), then resolves the active firm. Used by the stock page, the workflows list, the generic workflow-definition page, and the settings page.

**Firm dashboard** (`app/[locale]/page.tsx`): firm name, the caller's role (`fetchRoleInFirm`), an in-stock item count (from `stock_to_sale`'s own instances - a purpose-built stock summary, not the generic view), a workflow-definition count linking to `/workflows`, and (owner-only) a card linking to the audit log.

**Generic workflow UI** (`web/src/components/Workflow`, `app/[locale]/workflows`, `app/[locale]/stock`): the frontend expression of the workflow engine actually being generic (see the Workflow Engine section above) rather than hardcoded to `stock_to_sale`.

- **`app/[locale]/workflows/page.tsx`** lists every workflow definition the firm has (`fetchDefinitions`, i.e. `GET .../workflow-definitions` with no `?key=`), linking each to `/workflows/{key}`.
- **`app/[locale]/workflows/[key]/page.tsx`** and **`app/[locale]/stock/page.tsx`** are both thin wrappers around **`components/Workflow/WorkflowDefinitionView.tsx`** - the one generic page body (resolve the definition by key, list its instances, offer the create-instance form, show its history) - fixed to `STOCK_TO_SALE_KEY` for the `/stock` route (kept working at its existing, already-bookmarked URL) or to whatever key the dynamic route segment carries for `/workflows/[key]`. A firm's second workflow definition renders through the exact same component tree with zero new frontend code - proven in `internal/workflow/workflow_integration_test.go` (a synthetic `purchase_order` spec exercising `ListDefinitions`) and in this directory's own `format.test.ts`/`history.test.ts` (both run against two structurally different mock payload shapes, not just `stock_to_sale`'s).
- **`WorkflowInstanceList.tsx`** renders each instance's payload generically (`format.ts`'s `formatPayload`, a `key: value, key: value` rendering - there is no per-definition payload schema, see the Open Points entry below) and its state, and one button per `AvailableAction` labeled with the backend's own `action.name` and tagged `data-permission-key={action.permissionKey}` - never a hardcoded "Sell"/"Add Stock" label or column. Clicking posts to the generic proxy route below; no client-side permission check gates it, same "the backend's `permission.Has` is the only place that decision is made" convention as the original stock UI.
- **`CreateInstanceForm.tsx`** is a free-form key/value field editor (add a field, type a name and a value, submit) rather than a fixed field list - the most reasonable generic fallback given `CreateInstance`'s payload has no schema today. Its submit button carries `data-permission-key={definition.createPermissionKey}` (`DefinitionInfo.CreatePermissionKey`, added specifically so this button can carry a real tag instead of a hardcoded duplicate of `workflow.stock_to_sale.add_stock`).
- **`WorkflowHistory.tsx`** (backed by `history.ts`'s `buildHistoryRows`, pure/unit-tested) reuses the audit log rather than a parallel history mechanism: each row's payload is the audit entry's own delta when non-empty, falling back to the instance's current full payload (e.g. `record_sale`'s transitions, which the frontend always calls with an empty payload) - genuinely generic, not an "item" field lookup.
- **Generic proxy routes**: `POST /api/workflow/instances` (create), `POST /api/workflow/instances/{instanceId}/transitions/{actionKey}` (any transition), and `POST /api/workflow/definitions` (define a new workflow) - the last replaces nothing (it's new this batch); the first two replaced the old stock-specific `/api/stock/create` and `/api/stock/[instanceId]/sell` routes in the prior genericization pass. Same cookie-to-Bearer-token forwarding pattern throughout, no authorization decision made in any route itself.
- **`DefinitionBuilder.tsx`** is the frontend half of `DefineWorkflowForFirm`: a mechanical form (not a visual/drag-and-drop graph editor - out of scope, see the top of this section) for a definition's key/name/create-permission, a list of states (key, name, initial/terminal checkboxes), and a list of transitions (from-state key, to-state key, action key, name, permission key) - free-text fields throughout, matching `CreateInstanceForm.tsx`'s "structural form" style rather than dropdowns constrained to already-typed state keys. **`builderValidation.ts`**'s `validateBuilderSpec` mirrors `DefinitionSpec.Validate` client-side (exactly one initial state, every transition referencing a real state, no duplicate (from-state, action) pairs, ...) so a caller sees the same structural problems immediately - unit-tested in `builderValidation.test.ts` - but the backend's own `Validate` call remains the actual source of truth; a spec that somehow passes the client check and fails server-side still surfaces as a real 400, not a swallowed error. Rendered only for owners (checked by `app/[locale]/workflows/page.tsx`, the server component that mounts it) and tagged `data-permission-public` throughout, not `data-permission-key` - same category as Permission Audit Mode's own owner-only toggle: this is gated by the structural `is_owner` flag, not a permission-catalog entry, so there is no `permission.Has` key to tag it with.

**Audit log viewer** (`app/[locale]/audit-log/page.tsx`, `lib/auditlog.ts`, `GET /api/audit-log/[firmId]`): owner-gated (mirrors the backend's `audit.log.read`, held by the owner role by default), renders `auditlog.Entry`'s fields as-is (who, what, when, changes) rather than reshaping them.

**Settings page** (`app/[locale]/settings/page.tsx`, `lib/firm.ts`, `POST /api/firm/update-name`): firm name (now editable for owners via **`FirmNameEditor.tsx`**, backed by `internal/firm.UpdateName` - see the Firm renaming section above), the caller's role, and the firm's active workflow definitions. Everything except the name stays read-only: the `firms` table's jsonb `attributes` column still has no HTTP handler touching it, so this page still shows only what a real request can back, not more.

## Permission Audit Mode

Vision §3's "Yetki Denetim Modu": the UI layer that makes the permission keys already tagged on interactive elements (`AvailableAction.PermissionKey`, the stock page's `data-permission-key`) visible and editable to a firm's owner, on the actual live screens rather than a separate admin panel.

- **Who can use it**: Vision §3 scopes it to "an authorized user" without defining the term further. This codebase's working assumption - **not derived from an explicit Vision decision, flagged here rather than silently picked**: "authorized" means holding an owner-flagged role (`roles.is_owner`, `migrations/0004_role_owner_flag.up.sql`), checked via `internal/permission.IsOwner`. No separate `manage_permissions`-style permission key exists yet. If a firm later wants to delegate Permission Audit Mode to a non-owner role, that needs its own decision (and probably its own permission key) - out of scope here.
- **`internal/permission.GetFirmPermissionAudit`** is the one read call Audit Mode needs: the caller's own permission keys (across every role they hold, so their own badges are accurate even though owner roles are excluded below) plus every **non-owner** role's grants - Vision §3: "roles at the highest permission tier ... don't appear in these lists." The exclusion happens in the SQL (`WHERE NOT r.is_owner`), not just in what the frontend chooses to render - never trust the client for that.
- **`GrantPermission`/`RevokePermission`** are owner-gated (`IsMember` then `IsOwner`, same order as every firm-scoped write in this codebase) and independently re-check that the target role isn't owner-flagged (`ErrOwnerRoleImmutable`) even though the frontend never offers one as a target - the server-side check is what actually matters. Both are idempotent (`ON CONFLICT DO NOTHING` / a `DELETE` that's a no-op if nothing matched) and write one `audit_log` row each (`entity_type` `"role_permission"`).
- **Live-session propagation** (Vision §3: "active sessions for the affected role are reconnected... other admin screens are notified of the change live"): implemented as an in-process Server-Sent Events broadcaster, `internal/permission.Broadcaster` - `GET /api/firms/{firmID}/permission-events` holds one connection open per browser tab and receives a `permissions-changed` event every time `Broadcaster.Publish(firmID)` runs (called by the grant/revoke handlers after a successful commit). Open to **any firm member**, not owner-only like the audit/grant/revoke endpoints - the sessions that need to "reconnect" are the affected role's own, not just an owner watching Audit Mode.
  - **Why in-process instead of a message broker**: ZonaryOS is a single deployable binary (Never-Violate Rule 5) with no existing pub/sub infrastructure - the NATS JetStream mentioned in Vision §4 is earmarked for the Edge Agent's offline buffering, a different concern, not general-purpose pub/sub. Keycloak-backed bearer-token auth also carries no server-side session store this could hook into. A `map[firmID]map[chan struct{}]struct{}` guarded by one mutex is the least invasive mechanism that satisfies the requirement today.
  - **The tradeoff**: this stops working the moment ZonaryOS runs as more than one server process (each process would have its own, disconnected set of subscribers) - fine for now, since nothing about the current deployment model runs multiple instances, but worth revisiting explicitly if/when horizontal scaling becomes real. Noted here rather than silently designed around, since it's exactly the kind of thing that should become an Open Points entry if it starts to matter.
  - Events carry no payload beyond their name - subscribers always re-fetch full current state (`GetFirmPermissionAudit` again, or a page's own data) on wakeup rather than trying to apply a diff, so a client that missed an intermediate event because the channel coalesced two `Publish` calls into one wakeup still ends up correct.
  - **What "reconnected... immediately" does *not* require here**: the backend was never caching permissions - `permission.Has`/`IsMember`/`IsOwner` query the database fresh on every request, so a revoked permission is already enforced on the very next API call with zero propagation delay, before this SSE mechanism exists at all. What SSE actually fixes is the **UI** reflecting a change without a manual reload - Audit Mode badges updating, and (via `router.refresh()`) any server-rendered data on the page.
- **`identity.RoleInFirm`/`GET /api/me/firms/{firmID}/role`** gained an `isOwner` field so the frontend can decide whether to show the Audit Mode toggle at all, without a new endpoint - this is exactly the "once a firm has been chosen" partner endpoint `Memberships`' doc comment already pointed to for firm-scoped role detail.
- **Distinguishing "untagged" from "intentionally public"** (frontend, `web/src/components/AuditMode`): an interactive element with neither `data-permission-key` nor `data-permission-public="true"` is flagged with the Vision §3 warning badge ("this button isn't wired to any permission check - it may have been forgotten"). `data-permission-public` is the explicit opt-out for elements that are interactive but never meant to be permission-gated - sign-in/sign-out links, the wizard's pre-firm answer buttons, navigation links, and Audit Mode's own overlay controls (to avoid the mechanism flagging itself). Every existing interactive element outside the stock page's "Sell" button was retrofitted with one or the other as part of this feature - see the component for the full list.

HTTP surface (mirrors the existing `/api/firms/{firmID}/...` convention):

- `GET /api/firms/{firmID}/permission-audit` — the role-permission matrix (owner-only)
- `PUT /api/firms/{firmID}/roles/{roleID}/permissions/{key}` — grant (owner-only, idempotent)
- `DELETE /api/firms/{firmID}/roles/{roleID}/permissions/{key}` — revoke (owner-only, idempotent)
- `GET /api/firms/{firmID}/permission-events` — SSE stream (any member)

### Running the permission tests

`internal/permission/permission_integration_test.go` covers `Has`/`IsMember` directly; `internal/permission/audit_integration_test.go` covers Permission Audit Mode's business logic (the is_owner exclusion, the owner-only gate, idempotent grant/revoke, audit_log writes) - all real Postgres, same convention as everywhere else:

```
export ZONARYOS_TEST_ADMIN_DATABASE_URL=postgres://zonaryos:zonaryos@localhost:5433/zonaryos?sslmode=disable
export ZONARYOS_TEST_APP_DATABASE_URL=postgres://zonaryos_app:zonaryos_app@localhost:5433/zonaryos?sslmode=disable
make migrate
go test ./internal/permission/... -v
```

## Audit Trail Infrastructure

`internal/auditlog` is Vision §3's Audit Trail Infrastructure: every operation should be traceable "at the most detailed level" - both data changes and view/read access - readable by the firm owner (and, later, an "Auditor Role") and nobody else.

- **Data-change coverage**: the write-side pattern already existed before this package (`internal/workflow.CreateInstance`/`ExecuteTransition`, `internal/wizard.CreateDefaultFirm`, `internal/permission.GrantPermission`/`RevokePermission` each write one explicit `audit_log` row) and is left as-is - this codebase's modular-monolith style favors an explicit "log this write" call at each mutation point over a generic diffing framework (see `CLAUDE.md`). `auditlog.Write` exists so *new* write call sites share one `INSERT` instead of duplicating it; the existing four call sites above predate it and were not retrofitted, since `internal/permission` can't import `internal/auditlog` without an import cycle (`auditlog` itself depends on `internal/permission` for read-gating, see below) and touching the other three wasn't necessary to close any actual gap - firm creation, workflow transitions, and permission grant/revoke already covered every data-changing operation this slice has.
- **View/read logging** (`auditlog.LogView`): Vision §3 explicitly calls for logging reads too, not just writes - and explicitly flags this as the part with unresolved legal exposure (see docs/OPEN_POINTS.md item 33: whether retaining detailed view/read records, particularly employee access records, creates a KVKK issue is an open question requiring legal counsel). Rather than wiring every read endpoint in the system into this mechanism, only one representative path does: `internal/workflow.ListInstances` (the stock list's data source, PR #8) writes one `action: "view"` entry per call. Every other read path (`CurrentState`, `LookupDefinitionByKey`, Permission Audit Mode's own reads, the audit log read itself) is deliberately left unwired - broader rollout is future work pending that legal-review decision, not an oversight.
- **`auditlog.ReadPermission`** (`audit.log.read`) gates `GET /api/firms/{firmID}/audit-log` through the ordinary `permission.IsMember` + `Has` check (docs/DEVELOPMENT.md's own Authorization checklist above), not a hardcoded `IsOwner` check like Permission Audit Mode uses. The Auditor Role Vision §3 describes isn't designed or built in this PR (no external-auditor-invitation flow exists) - gating on a permission key instead of ownership means a firm can extend audit log access to any future Auditor Role later, purely by granting it this key via Permission Audit Mode, with no code change here. `internal/wizard.CreateDefaultFirm` grants it to every new firm's own owner role at creation time (`auditlog.RegisterReadPermissionTx`, the same self-action-auto-grant shape `workflow.DefineWorkflowTx` already established for workflow-introduced permissions).
- **Retention**: docs/OPEN_POINTS.md item 33 leaves the actual retention period - and even whether automatic deletion is appropriate at all, given the KVKK question above - unresolved; Vision §3's own "at least 10 years" figure is explicitly marked not finalized. Nothing in this codebase hardcodes a retention duration. `auditlog.PurgeOlderThan` is a plain, firm-by-firm (RLS-respecting) deletion function that takes an explicit cutoff; the only caller is `cmd/auditpurge` (`make audit-purge`), a manually-invoked maintenance command that refuses to run at all unless an operator has explicitly set `ZONARYOS_AUDIT_RETENTION_DAYS` to a positive integer - there is no default to fall back to, and `cmd/server` never invokes it automatically.

HTTP surface:

- `GET /api/firms/{firmID}/audit-log` — the firm's audit trail, most recent first (gated by `audit.log.read`)

### Running the audit log tests

`internal/auditlog/auditlog_integration_test.go` needs a real Postgres, same convention as everywhere else above:

```
export ZONARYOS_TEST_ADMIN_DATABASE_URL=postgres://zonaryos:zonaryos@localhost:5433/zonaryos?sslmode=disable
export ZONARYOS_TEST_APP_DATABASE_URL=postgres://zonaryos_app:zonaryos_app@localhost:5433/zonaryos?sslmode=disable
make migrate
go test ./internal/auditlog/... -v
```

### Running the workflow engine tests

`internal/workflow/spec_test.go` is a pure unit test (spec validation, no database). `internal/workflow/workflow_integration_test.go` needs a real Postgres, same convention as the RLS tests above:

```
export ZONARYOS_TEST_ADMIN_DATABASE_URL=postgres://zonaryos:zonaryos@localhost:5433/zonaryos?sslmode=disable
export ZONARYOS_TEST_APP_DATABASE_URL=postgres://zonaryos_app:zonaryos_app@localhost:5433/zonaryos?sslmode=disable
make migrate
go test ./internal/workflow/... -v
```

### Running the RLS integration tests

These require a real Postgres instance (e.g. `make dev-up`) and are skipped otherwise:

```
export ZONARYOS_TEST_ADMIN_DATABASE_URL=postgres://zonaryos:zonaryos@localhost:5433/zonaryos?sslmode=disable
export ZONARYOS_TEST_APP_DATABASE_URL=postgres://zonaryos_app:zonaryos_app@localhost:5433/zonaryos?sslmode=disable
make migrate
go test ./internal/platform/db/... -v
```

### Why `make test` passes `-p 1`

Several packages' integration tests (`internal/platform/db`, `internal/identity`, `internal/workflow`, ...) share the same real Postgres database and each `TRUNCATE`s the tables it needs a clean slate for at the start of every test. `go test ./...` runs different packages' test binaries concurrently by default - fine for packages that don't share state, but two packages truncating the same tables (`firms`, `users`, `roles`, ...) at the same moment can wipe out data a sibling package's test just relied on, causing spurious failures that have nothing to do with the code under test. `go test -p 1 ./...` (what `make test` runs) forces one package at a time, which is what actually matters here - not test speed.

If you ever see a failure in one integration test package only when running the full suite, and it passes cleanly in isolation (`go test ./internal/xyz/... -v`), this is almost certainly why - not a real regression.

## Continuous Integration

`.github/workflows/ci.yml` turns most of the CI Checklist categories (CLAUDE.md's "How to Verify a Change") from manual PR-by-PR discipline into automated checks on every PR (and on push to `main`). Every job here existed as a manual step some earlier PR ran by hand - this file doesn't introduce new verification steps, it just stops trusting a human to remember to run them. **Canary/Rollback Trigger** remains the one item intentionally "Not Set Up": there is no ZonaryOS deployment target or infrastructure decided yet (see `docs/OPEN_POINTS.md` item 34) for a rollback trigger to hook into.

- **Build** (`build-backend`, `build-frontend`): `go build ./...` and `cd web && npm run build`.
- **Unit Tests** (`unit-tests-backend`, `unit-tests-frontend`): `go vet ./... && go test ./...` with no database env vars set, so every `*_integration_test.go` file self-skips via the same `t.Skip(...)` convention already documented throughout this file - not a new detection mechanism, the existing one just runs unattended now. `cd web && npm run lint && npm test` for the frontend side.
- **Integration Tests** (`integration-tests`): a real `postgres:16` GitHub Actions service container, configured identically to `docker-compose.yml`'s `postgres` service (same image, same `POSTGRES_USER`/`PASSWORD`/`DB`), so the exact `ZONARYOS_TEST_ADMIN_DATABASE_URL`/`ZONARYOS_TEST_APP_DATABASE_URL` convention this file already documents for local dev applies unchanged - `go run ./cmd/migrate` then `go test -p 1 ./...`. This is what now actually runs the RLS/tenant-isolation tests (`internal/platform/db`, and every package's own `*_integration_test.go`) on every PR - previously they only ran when a human remembered to `make dev-up` and set the env vars, the gap that let Open Points item 37 ship once before a later manual audit caught it. `internal/identity/keycloak_integration_test.go` is deliberately left out of this job's scope (no live Keycloak service container here) - it self-skips since its `ZONARYOS_TEST_KEYCLOAK_*` env vars aren't set, same as any other CI environment without a live Keycloak; still a manual (`make dev-up`/`make dev-up-standalone`) verification step for changes that touch `internal/identity`.
- **RLS / Permission Drift Audit** (`rls-permission-audit`, `cmd/ciaudit`): a codified version of the manual trace that closed Open Points item 37. Two checks: (1) every table with a `firm_id` column across `migrations/*.up.sql` must have a matching `ALTER TABLE ... ENABLE ROW LEVEL SECURITY` somewhere in the migrations directory (Never-Violate Rule 3); (2) every Go function/method under `internal/` with a parameter literally named `firmID` must call `permission.IsMember`/`Has`/`IsOwner` somewhere in its body (including nested closures, e.g. the `WithFirmContext` callback pattern every handler uses) - or carry a `ciaudit:ignore-firmid-check: <reason>` line in its doc comment, for the legitimate exceptions (a low-level primitive like `WithFirmContext` itself, an internal helper only reachable after its caller already checked, a provisioning-only function never reachable with a caller-supplied `firmID`). This is a static, grep/AST-level heuristic, not full data-flow analysis - it is exactly the "grep for firmID-taking functions, trace to permission.IsMember" methodology the item 37 fix already used, now run automatically instead of by hand.
- **i18n Hardcoded-String Check** (`i18n-check`, `web/scripts/check-i18n-strings.mjs`): walks every `.ts`/`.tsx` file under `web/src` with the TypeScript compiler API's own AST (not a fresh regex heuristic - `typescript` is already a devDependency), flagging any JSX text node containing letters (an expression container like `{t("key")}` is a different AST node entirely, so it's invisible to this walk - only literal text rendered directly is caught) and any string-literal `placeholder`/`aria-label`/`title`/`alt` attribute containing letters. A line can opt out with a trailing `// i18n-ignore` comment, for genuinely non-UI text (see `StockList.tsx`'s own note on why workflow state/action names rendered as-is are out of the i18n layer's scope). Plain ESM (not `.cjs`) - this repo's ESLint config flags `require()`-style imports repo-wide, this script included.
- **Doc Sync Check** (`doc-sync-check`, `scripts/check_doc_sync.py`, PR-only): if the diff touches `internal/`, `cmd/`, or `web/src/`, `docs/DEVELOPMENT.md` must be part of the same diff - this file's own per-module section convention (one `## <Module>` section per feature, established across PRs #3-#10) only stays accurate if every PR that changes a module also updates its section.
- **License Header Check** (`license-header-check`, `scripts/license_headers.py`): every tracked `.go` file under `cmd`/`internal` and every `.ts`/`.tsx` file under `web/src` must start with the header in `scripts/license-header.txt` (as a `//` comment block) - see `LICENSE` (a draft, pending legal review - `docs/OPEN_POINTS.md` item 20). For a Go file, the header is its own separate comment block at the very top, blank-line-separated from whatever follows, specifically so it never attaches as that file's package doc comment (Go only treats the comment block immediately touching the `package` clause, with no blank line, as the doc comment). For a `.tsx` file opening with a `"use client"`/`"use server"` directive, the header goes immediately after it instead of before, since that directive must stay the file's literal first line. Run `python3 scripts/license_headers.py --fix` to add a missing header mechanically.
- **API Contract Diff** (`api-contract-diff`, `scripts/check_api_contract.py`, PR-only, Never-Violate Rule 6): diffs `docs/api/openapi.yaml` against the same file's content on the PR's base branch (`git show origin/<base>:docs/api/openapi.yaml` - the base branch's own history is the "previous version" baseline, no separate versioned-snapshot file needed) and fails if any documented path or HTTP method present in the base version is missing from the head version. This is a path/method-level diff only, not a full schema diff - it will not catch a removed required field, for example - a known limitation of this first version, not a silent gap left undocumented.
- **Migration Safety Check** (`migration-safety-check`, `scripts/check_migration_safety.py`, PR-only): Vision §4's active-passive multi-region architecture means a rolling deploy window can have old and new application code running against the same schema at once - a migration that isn't safely forward/backward compatible during that window is exactly the failure mode this catches. Flags, in any `migrations/*.up.sql` file *added* in the PR diff: `DROP TABLE`, `DROP COLUMN`, `TRUNCATE`, any `ALTER COLUMN ... TYPE` (can't statically tell narrowing from widening, so any type change is flagged for manual review), and non-additive `RENAME COLUMN`/`RENAME TO`. Separately, any *existing* migration file *modified* (not added) in the diff is always flagged - migrations must never be edited after merging (a rolling deploy may have already applied the old version elsewhere). A migration can acknowledge an intentional destructive change with a `-- migration-safety:acknowledge: <reason>` comment anywhere in the file - a documented exception, still printed, not silently dropped. `.down.sql` files are never scanned: they're rollback-only by golang-migrate's own convention and legitimately contain the inverse destructive statement (see `migrations/0004_role_owner_flag.down.sql`'s `DROP COLUMN`).
- **Dependency Vulnerability Scan** (`dependency-scan-backend`/`dependency-scan-frontend`): `govulncheck` (`scripts/check_govulncheck.py`) for Go, restricted to its default "source" scan mode - only vulnerabilities actually reachable from this module's own call graph, not merely-imported-but-unused ones - failing on any finding not listed in `scripts/govulncheck-allowlist.json`. `npm audit` (`web/scripts/check-npm-audit.mjs`) for the frontend, failing on any high/critical finding not listed in `web/audit-allowlist.json`; moderate/low findings are reported but don't fail the build. Both allowlists require a `reason` and a `reviewBy` date per entry - a documented exception, not a silent ignore - and the frontend one currently has 4 real entries (postcss/sharp/brace-expansion advisories pulled in transitively by `next`/`eslint`, where npm's own proposed fix is a multi-major-version downgrade, not a safe one - see the file for the full reasoning). Both scripts fail loudly (distinct exit code) rather than silently reporting "clean" if the underlying tool itself couldn't run (e.g. couldn't reach its vulnerability database).
- **SAST Security Scan** (`sast-scan`): [gosec](https://github.com/securego/gosec) (`scripts/check_gosec.py`) for Go - purely local static analysis, no external service/login/API token - failing on any finding; a genuine false positive is suppressed with gosec's own native `// #nosec <RULE> -- <reason>` comment (two exist today, both documented inline) rather than a second, parallel allowlist file. [gitleaks](https://github.com/zricethezav/gitleaks) (`scripts/check_gitleaks.sh`, `.gitleaks.toml`) for secret scanning across git history, with a documented allowlist for one known false positive (`*_integration_test.go`'s `seedUser` helpers building a fake `keycloakSubject+"@example.com"` identifier, which the `generic-api-key` rule's entropy heuristic misreads as a key).
- **E2E Smoke Test** (`e2e-smoke-test`, `scripts/e2e_smoke_test.sh`): brings up a real stack - Postgres + Keycloak via `make dev-up-standalone` (the exact same standalone bring-up every prior PR's manual E2E verification already used, not a new sequence invented for CI), the real backend (`go run ./cmd/server`), and the real frontend (`npm run build && npm run start`) - then exercises login (a real Keycloak-issued bearer token via Direct Access Grant, verified by the real `internal/identity.Verifier`; also confirms the frontend's own `/api/auth/login` redirects to that same real Keycloak issuer) and the core transaction: wizard → firm creation → add stock → sell, asserting on real HTTP responses at every step, plus a bonus check that the audit trail (`GET .../audit-log`) recorded the transaction. **Disclosed scope boundary**: this does not drive a headless browser through Keycloak's interactive login form (that needs Playwright + a browser download in CI, a further infra addition) - "login" here means obtaining a real token from the real Keycloak server directly, not simulating a user clicking through the browser-based PKCE redirect flow. This test found and helped fix a real bug during development (see `internal/workflow.CreateInstance`/`ExecuteTransition`'s nil-payload guard, and `TestExecuteTransition_NilPayloadDoesNotCorruptStoredPayload`): a nil payload map marshaled to JSON `null`, and Postgres's `jsonb ||` operator silently corrupted the stored payload into an array instead of leaving it untouched.

### Running the CI checks locally

```
go build ./...
go vet ./... && go test ./...
go run ./cmd/ciaudit
python3 scripts/license_headers.py --check      # or --fix to add missing headers
python3 scripts/check_gosec.py
python3 scripts/check_govulncheck.py
cd web && npm ci && npm run build && npm run lint && npm test && npm run check:i18n && npm run check:audit
```

The Postgres-backed integration test job, Doc Sync Check, API Contract Diff, and Migration Safety Check need either a real Postgres (see "Running the RLS integration tests" above) or a base ref to diff against (`python3 scripts/check_doc_sync.py origin/main` / `python3 scripts/check_api_contract.py origin/main` / `python3 scripts/check_migration_safety.py origin/main`) - all fine to run locally against a local clone with `main` fetched. `scripts/check_gitleaks.sh` needs `gitleaks` installed (`go install github.com/zricethezav/gitleaks/v8@latest`) and on `PATH`. `scripts/e2e_smoke_test.sh` needs the full standalone stack up (`make dev-up-standalone && make migrate`, the backend running, and the frontend built and started) - see the job definition in `.github/workflows/ci.yml` for the exact sequence.

### Context-Loss Tactics: PR review-comment catch-up

(No prior "Context-Loss Tactics" section existed in this repo before this note - this is the first one, added here per the Notion Development Process page §8. If a broader context-loss-mitigation section gets written later, merge this into it rather than keeping two.)

**Every session starts by checking the immediately preceding PR for unaddressed review comments before starting new work** - the same discipline already applied to `docs/VISION.md`/`docs/OPEN_POINTS.md` at the start of a new task. A CI run going green does not mean a PR is done: a human reviewer, GitHub's own bots (CodeQL, Dependabot, secret-scanning alerts), or any other automated commenter can leave feedback after the fact, and nothing in this repo's process otherwise re-surfaces it.

Checklist for the PR(s) in scope:
- Pull all three comment surfaces, not just one: issue-level comments, inline review comments, and formal reviews (`pull_request_read` with `get_comments`/`get_review_comments`/`get_reviews`, or the equivalent for whatever tool is available) - a comment can land in any of the three.
- For each comment found: address it with a real code change if it's actionable, note explicitly if it's already stale/resolved by later work, or reply substantively if it's a question - never leave it silently unread.
- If the PR in question has already merged, don't rewrite merged history - open a new small follow-up PR/commit referencing the original instead.
- Report per PR explicitly, including "none found" - don't assume a PR has nothing just because it's old or because CI was green.
