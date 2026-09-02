## Packages

- `internal/firm`
- `internal/workflow`
- `web`

## Migration

- `migrations/0047_add_firm_timezone.up.sql`
- `migrations/0047_add_firm_timezone.down.sql`

## CI Checks

- `go build ./...`
- `go vet ./...`
- `go test ./...`
- `go run ./cmd/ciaudit` (RLS / permission drift audit)
- `python3 scripts/license_headers.py --check`
- `python3 scripts/check_doc_sync.py origin/main`
- `cd web && npm run lint && npm test && npm run check:i18n && npm run check:permission-tags`

## Steps

- [ ] Create database migration `migrations/0047_add_firm_timezone.up.sql` adding a `timezone text NOT NULL DEFAULT 'UTC'` column to the `firms` table, with a leading comment explaining what it is and why, following the style of the other files in `migrations/`. Create `migrations/0047_add_firm_timezone.down.sql` dropping that column. Migrations do not carry a license header - only `.go`, `.ts` and `.tsx` files do.
- [ ] Update firm domain models and SQL queries in `internal/firm/firm.go` and `internal/firm/handlers.go` to include the `Timezone string` field on firm structs and in `Get`, `List`, `Create`, and `Update` repository queries and handlers. Ensure English-only error messages, validation of IANA timezone names (e.g., using `time.LoadLocation`), and retention of existing RLS context boundaries.
- [ ] Update `internal/workflow/scheduler.go` and `internal/workflow/rules.go` to resolve the firm's configured timezone (falling back to UTC if unset or invalid) when computing `next_run_at` for schedule-triggered rules (`workflow.RunScheduler`). Ensure calendar/time math respects the firm's local timezone rather than evaluating strictly in UTC, without introducing cron expressions or external ML dependencies.
- [ ] Add unit and integration tests in `internal/workflow/scheduler_test.go` and `internal/workflow/rules_integration_test.go` verifying that scheduled rule runs for firms configured with non-UTC timezones (such as `Europe/Istanbul`) compute the correct `next_run_at` in the firm's local timezone across day boundaries and DST transitions.
- [ ] Update frontend types and API clients in `web/src/lib/firm.ts` and `web/src/lib/workflow.ts` (web/src/app/[locale]/settings/FirmMetadataEditor.tsx ) to include the `timezone` property on firm responses and requests, adhering to the standard permission tags (`data-permission-key`) and using i18n translation keys with no hardcoded strings.
- [ ] Update `docs/DEVELOPMENT.md`: add the firm timezone field and the scheduler's timezone-aware next-run computation to the relevant module sections. The Doc Sync CI check requires this file to change whenever `internal/`, `cmd/` or `web/src/` files change, so this step is mandatory, not optional.
