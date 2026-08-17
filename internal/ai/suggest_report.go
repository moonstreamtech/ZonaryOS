// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/reports"
)

const suggestReportAuditAction = "suggest_report"

const reportSuggestionSystemPrompt = `You translate a plain-language business question into a structured report query for an ERP system. Respond with ONLY a single JSON object (no markdown code fences, no commentary) matching exactly this shape:

{
  "entity": "one_of_the_allowed_entities_below",
  "filters": [{"field": "field_name", "op": "eq", "value": "some value"}],
  "group_by": "field_name",
  "metrics": [{"name": "field_name_or_*", "aggregation": "count"}],
  "date_range": {"field": "timestamp_field_name", "from": "2026-01-01T00:00:00Z", "to": "2026-01-31T23:59:59Z"}
}

Allowed entities and their fields (use ONLY these - any other entity or field name is invalid):
- workflow_instances: id, workflow_definition_id, created_by_user_id, created_at, updated_at
- journal_entries: id, description, source_type, source_id, created_by, posted_at, created_at
- invoices: id, invoice_number, customer_id, status, subtotal, tax_amount, total, currency, issued_date, due_date, created_at
- deliveries: id, reference, carrier, status, source_type, source_id, estimated_date, actual_date, created_at
- people: id, full_name, type, email, status, start_date, end_date, created_at
- products: id, sku, name, unit, unit_price, cost_price, category, is_active, created_at

Rules:
- "op" must be one of: eq, neq, lt, gt, lte, gte, contains.
- "metrics" aggregation must be "count", or "sum(field)"/"avg(field)"/"min(field)"/"max(field)" naming one of the entity's own numeric fields.
- "filters", "group_by", and "date_range" are all optional; "metrics" must have at least one entry.
- Respond with ONLY the JSON object, nothing else.

Example, for "How many invoices did we issue last month?":
{"entity":"invoices","metrics":[{"name":"*","aggregation":"count"}],"date_range":{"field":"issued_date","from":"2026-07-01T00:00:00Z","to":"2026-07-31T23:59:59Z"}}`

// SuggestReport calls the firm's active AI provider with question and
// returns a reports.QuerySpec the caller can review, edit, then run
// through the parametric report engine. Owner-gated, same shape as
// SuggestWorkflow - see that function's own doc comment for the
// membership/ownership/active-config-check ordering and the "no
// auto-retry" contract this mirrors exactly.
func SuggestReport(ctx context.Context, pool *pgxpool.Pool, encryptor *Encryptor, firmID, userID uuid.UUID, question string) (reports.QuerySpec, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return reports.QuerySpec{}, fmt.Errorf("%w: question must not be empty", ErrInvalidConfig)
	}

	var (
		cfg       Configuration
		plaintext string
	)
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotOwner
		}

		cfg, plaintext, err = getActiveConfigTx(ctx, tx, encryptor, firmID)
		return err
	})
	if err != nil {
		return reports.QuerySpec{}, err
	}

	provider, err := ProviderFactory(cfg.Provider, cfg.Model, plaintext)
	if err != nil {
		return reports.QuerySpec{}, err
	}

	raw, err := provider.Complete(ctx, reportSuggestionSystemPrompt, question)
	if err != nil {
		return reports.QuerySpec{}, err
	}

	var spec reports.QuerySpec
	parseErr := json.Unmarshal([]byte(extractJSONObject(raw)), &spec)
	var validateErr error
	if parseErr == nil {
		_, validateErr = reports.BuildQuery(uuid.Nil, spec)
	}

	auditErr := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return auditlog.Write(ctx, tx, firmID, userID, cfg.ID, configAuditEntityType, suggestReportAuditAction, map[string]any{
			"source":   "ai",
			"provider": string(cfg.Provider),
			"model":    cfg.Model,
			"valid":    parseErr == nil && validateErr == nil,
		})
	})
	if auditErr != nil {
		return reports.QuerySpec{}, fmt.Errorf("audit AI report suggestion: %w", auditErr)
	}
	if parseErr != nil || validateErr != nil {
		return reports.QuerySpec{}, fmt.Errorf("%w: %s", ErrInvalidAISuggestion, raw)
	}

	return spec, nil
}
