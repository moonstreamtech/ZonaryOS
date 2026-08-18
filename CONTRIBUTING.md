# Contributing to ZonaryOS

This file covers two things: how to add a new plugin extension (the plugin/extension architecture batch's own three extension points), and how to get a local development environment running. For everything else — architecture decisions, module scope, naming/privacy conventions — start at `CLAUDE.md` and `docs/DEVELOPMENT.md`.

## Development setup

```
make dev-up-standalone   # starts Postgres + Keycloak in Docker
make migrate             # applies every migrations/*.sql file
make run                 # go run ./cmd/server
make web-dev             # cd web && npm run dev
```

`make dev-down-standalone` tears the stack back down. See `docs/DEVELOPMENT.md`'s "Continuous Integration" section for the exact env vars each CI check (and each package's own integration test suite) expects — most need `ZONARYOS_TEST_ADMIN_DATABASE_URL`/`ZONARYOS_TEST_APP_DATABASE_URL` pointed at the same `dev-up-standalone` Postgres instance.

## Extending ZonaryOS with a plugin

`internal/plugin` defines three extension points a compiled-in Go plugin can implement. See that package's own doc comment (`internal/plugin/hooks.go`) for the full interface definitions, and `internal/plugins/activitylog` for a complete, real example implementing two of the three.

**Scope, up front**: every plugin in this codebase is a Go package registered at compile time in `cmd/server/main.go` — there is no dynamic loading (no `.so` files, no WASM), no marketplace, no sandboxing. A plugin is trusted, in-process code with the same privileges as any other package in this binary. If you're looking for a plugin *marketplace* or *runtime-installable* extension, that's out of scope for what exists today.

### How to add a new workflow hook plugin

1. Create a new package under `internal/plugins/<yourplugin>`.
2. Define a type implementing `plugin.WorkflowHook`:
   ```go
   type Plugin struct{ pool *pgxpool.Pool }

   func (p *Plugin) OnTransition(ctx context.Context, event plugin.TransitionEvent) error {
       // event.FirmID, event.InstanceID, event.DefinitionKey, event.FromState,
       // event.ToState, event.ActionKey, event.Payload are all available here.
       // This runs AFTER the transition has already committed - see
       // internal/workflow/plugin_hooks.go's dispatchTransitionHooks for the
       // exact timing and error-handling contract (sync dispatch, one hook's
       // error is logged and does not stop the others, never fails the
       // already-committed transition).
       return nil
   }
   ```
3. Add a `Register(registry *plugin.Registry, pool *pgxpool.Pool)` function that constructs your plugin and calls `registry.RegisterWorkflowHook(p)` — see `internal/plugins/activitylog/activitylog.go`'s own `Register` for the exact shape.
4. Wire it into `cmd/server/main.go`, next to `activitylog.Register(pluginRegistry, pool)`: add `yourplugin.Register(pluginRegistry, pool)`.
5. Optionally, add a row to the `plugins` catalog table (`migrations/0037`, via `POST /api/plugins`, platform-admin-gated) so it's discoverable/configurable per firm through `firm_plugin_configs` — this is metadata/documentation only in this batch, it does not itself gate whether your `Register` call above actually dispatches (see `internal/plugin/catalog.go`'s own doc comment).

`TransitionEvent` currently carries no acting-user identity (see `internal/plugins/activitylog`'s own `OnTransition` doc comment for why its example description can't name a person) — if your plugin genuinely needs that, it requires a core `internal/plugin`/`internal/workflow` change, not something you can work around in your own plugin package.

### How to add a new KPI provider

1. Implement `plugin.KPIProvider`:
   ```go
   func (p *Plugin) GetKPIs(ctx context.Context, firmID uuid.UUID, pool *pgxpool.Pool) ([]plugin.KPIValue, error) {
       // Query your plugin's own table(s), scoped to firmID via
       // zdb.WithFirmContext (see internal/plugins/activitylog's own GetKPIs
       // for the exact pattern) - this is called AFTER
       // internal/reports.GetDashboardKPIs' own permission.IsMember check has
       // already passed, so you don't re-check membership yourself.
       return []plugin.KPIValue{{Key: "your_metric_key", Unit: "count", Value: "3"}}, nil
   }
   ```
2. Register it: `registry.RegisterKPIProvider(p)` inside your `Register` function.
3. Pick a `Key` that won't collide with a built-in `kpiDescriptors` key (`internal/reports/kpi.go`) or another plugin's own key — there's no runtime collision check, a colliding key just means one tile silently shadows another in the dashboard response.
4. Your tiles appear in the `GET .../reports/dashboard`-style response (`internal/reports.GetDashboardKPIs`) automatically, right after the built-in ones, once `cmd/server/main.go` bridges your registry into `reports.RegisterExternalKPIProvider` (already done once, generically, for every registered `KPIProvider` — you don't touch `main.go`'s bridging loop itself, only the one `Register` call that adds your plugin to the registry).

### How to add a new report source

1. Implement `plugin.ReportSource`:
   ```go
   func (p *Plugin) Entity() string { return "your_entity_name" }

   func (p *Plugin) AllowedFields() map[string]queryfilter.FieldDef {
       return map[string]queryfilter.FieldDef{
           "name": {Column: "name", Kind: queryfilter.KindString},
       }
   }

   func (p *Plugin) Query(ctx context.Context, spec reports.QuerySpec, firmID uuid.UUID, pool *pgxpool.Pool) ([]map[string]any, int, error) {
       // Run whatever query you like against your own table(s) - spec has
       // already been validated against AllowedFields by the time this is
       // called (internal/reports' own validateExternalQuerySpec), so you
       // don't need to re-validate field names yourself.
   }
   ```
2. Register it: `registry.RegisterReportSource(p)`.
3. Pick an `Entity()` name that doesn't collide with a built-in `entityRegistry` key (`workflow_instances`, `journal_entries`, `invoices`, `deliveries`, `people`, `products`) — a built-in entity always wins a name collision (checked first), so a colliding plugin entity would simply never be reachable.
4. Your entity becomes queryable through the exact same `POST .../reports/definitions/{firmID}/{id}/run` path as any built-in one, with zero frontend changes needed.

## Developer API documentation

`GET /api/docs` serves this installation's OpenAPI 3.0 spec (`docs/api/openapi.yaml`) as raw YAML — no authentication, no rendered UI. Point an external tool (Swagger UI, Postman, an SDK generator) at it directly. The spec itself is kept honest by `scripts/check_api_contract.py`, which runs in CI on every PR — see `docs/DEVELOPMENT.md`'s "Continuous Integration" section.
