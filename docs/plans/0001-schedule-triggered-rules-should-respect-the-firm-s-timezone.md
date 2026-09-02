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
- `npm run check` (or frontend type-check / linting)
- License header check

## Steps

- [ ] Create database migration `migrations/0047_add_firm_timezone.up.sql` that adds a nullable column `timezone` (type `text`) with a comment to the `firms` table, and create `migrations/0047_add_firm_timezone.down.sql` to drop the column `timezone` from the `firms` table. Ensure all SQL syntax conforms to standard PostgreSQL and includes the standard license header.
- [ ] Update firm domain models and SQL queries in `internal/firm/firm.go` and `internal/firm/handlers.go` to include the `Timezone *string` (or `string`) field on firm structs and in `Get`, `List`, `Create`, and `Update` repository queries and handlers. Ensure English-only error messages, validation of IANA timezone names (e.g., using `time.LoadLocation`), and retention of existing RLS context boundaries.
- [ ] Update `internal/workflow/scheduler.go` and `internal/workflow/rules.go` to resolve the firm's configured timezone (falling back to UTC if unset or invalid) when computing `next_run_at` for schedule-triggered rules (`workflow.RunScheduler`). Ensure calendar/time math respects the firm's local timezone rather than evaluating strictly in UTC, without introducing cron expressions or external ML dependencies.
- [ ] Add unit and integration tests in `internal/workflow/scheduler_test.go` and `internal/workflow/rules_integration_test.go` verifying that scheduled rule runs for firms configured with non-UTC timezones (such as `Europe/Istanbul`) compute the correct `next_run_at` in the firm's local timezone across day boundaries and DST transitions.
- [ ] Update frontend types and API clients in `web/src/lib/firm.ts` and `web/src/lib/workflow.ts` (and any timezone metadata editor components) to include the `timezone` property on firm responses and requests, adhering to the standard permission tags (`data-permission-key`) and using i18n translation keys with no hardcoded strings.
