// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Part 4 of the AI integration batch: a daily background check, per firm
// with an active AI configuration, that asks the configured AI provider
// whether last week's account-level financial totals look unusual
// compared to the week before. This is explicitly an AI-ASSISTANT CALL,
// NOT a real ML anomaly-detection model - no model is trained on this
// installation's (or any installation's) data, there is no statistical
// baseline computed anywhere in this file, and the "detection" is
// entirely the configured LLM's own judgment on a single text prompt. See
// docs/DEVELOPMENT.md's own section on this batch for the same
// disclosure aimed at a human reader.
//
// PII firewall: the prompt built by buildAnomalyPrompt contains ONLY
// per-account aggregated totals (account code, name, type, and two
// summed numbers) - never a journal_entries.description, never a
// journal_lines row, never any instance/customer/product payload value.
// See buildAnomalyPrompt's own doc comment for the exact SQL this is
// built from.
package ai

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/notification"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// AnomalySchedulerPollInterval is the background scheduler's production
// poll cadence - "daily" per Part 4's own brief. Tests pass a much
// shorter interval directly to RunAnomalyScheduler, the same
// RunX(ctx, pool, pollInterval)/ProcessX(ctx, pool) split
// internal/asset/scheduler.go's own RunMaintenanceScheduler establishes.
const AnomalySchedulerPollInterval = 24 * time.Hour

const notificationTypeAIAnomaly = "ai_anomaly_flagged"

const anomalyAuditAction = "anomaly_check"

// accountPeriodTotal is one account's signed net total (credits-minus-
// debits for revenue/liability/equity types, debits-minus-credits for
// asset/expense types - the same signed-CASE convention
// internal/costcenter/report.go's own cost-center P&L query uses) over
// one 7-day window.
type accountPeriodTotal struct {
	Code string
	Name string
	Type string
	Net  string
}

// RunAnomalyScheduler runs ProcessAnomalyChecks every pollInterval until
// ctx is cancelled - cmd/server/main.go starts this with
// AnomalySchedulerPollInterval.
func RunAnomalyScheduler(ctx context.Context, pool *pgxpool.Pool, encryptor *Encryptor, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ProcessAnomalyChecks(ctx, pool, encryptor)
		}
	}
}

// ProcessAnomalyChecks is RunAnomalyScheduler's testable core: for every
// firm with an active AI configuration, aggregate last week's vs. the
// week before's per-account journal totals, ask the configured provider
// whether anything looks unusual, and notify the firm's owners if it says
// yes. A firm with no active AI configuration is skipped entirely - Part
// 5's opt-in contract applies to the background check exactly as it does
// to every HTTP /ai/* endpoint.
func ProcessAnomalyChecks(ctx context.Context, pool *pgxpool.Pool, encryptor *Encryptor) {
	firmIDs, err := listAllFirmIDs(ctx, pool)
	if err != nil {
		slog.Error("ai anomaly scheduler: list firms", "err", err)
		return
	}
	for _, firmID := range firmIDs {
		processAnomalyCheckForFirm(ctx, pool, encryptor, firmID)
	}
}

// listAllFirmIDs mirrors internal/asset/scheduler.go's own function of
// the same name (firms carries no RLS at all - it IS the tenant boundary,
// not tenant-scoped data - so a plain unscoped read is correct here too).
// Redeclared per-package rather than shared, the same way
// internal/contracts' own scheduler redeclares listOwnerUserIDsTx instead
// of importing internal/asset for it.
func listAllFirmIDs(ctx context.Context, pool *pgxpool.Pool) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `SELECT id FROM firms`)
	if err != nil {
		return nil, fmt.Errorf("list firms: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan firm id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ciaudit:ignore-firmid-check: called only by ProcessAnomalyChecks, with
// firmID drawn from listAllFirmIDs (an internal, unscoped read of the
// firms table itself) - never from an HTTP caller.
func processAnomalyCheckForFirm(ctx context.Context, pool *pgxpool.Pool, encryptor *Encryptor, firmID uuid.UUID) {
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		cfg, plaintext, err := getActiveConfigTx(ctx, tx, encryptor, firmID)
		if err != nil {
			if err == ErrNoActiveConfig {
				return nil
			}
			return err
		}

		if alreadyChecked, err := alreadyCheckedTodayTx(ctx, tx, firmID); err != nil {
			return err
		} else if alreadyChecked {
			return nil
		}

		current, err := accountTotalsForWindowTx(ctx, tx, firmID, 7, 0)
		if err != nil {
			return err
		}
		previous, err := accountTotalsForWindowTx(ctx, tx, firmID, 14, 7)
		if err != nil {
			return err
		}
		if len(current) == 0 && len(previous) == 0 {
			return nil
		}

		provider, err := ProviderFactory(cfg.Provider, cfg.Model, plaintext)
		if err != nil {
			return err
		}

		prompt := buildAnomalyPrompt(current, previous)
		response, err := provider.Complete(ctx, anomalySystemPrompt, prompt)
		flagged := err == nil && anomalyResponseFlagsSomething(response)

		if auditErr := writeAnomalyAuditTx(ctx, tx, firmID, cfg, err == nil); auditErr != nil {
			return auditErr
		}
		if err != nil {
			slog.Warn("ai anomaly scheduler: provider call failed", "firmId", firmID, "err", err)
			return nil
		}
		if !flagged {
			return nil
		}

		recipients, err := listOwnerUserIDsTx(ctx, tx, firmID)
		if err != nil {
			return err
		}
		if len(recipients) == 0 {
			return nil
		}

		title := "AI flagged unusual financial activity"
		payload := map[string]any{"checkedAt": time.Now().UTC().Format("2006-01-02")}
		return notification.CreateForRecipientsTx(ctx, tx, firmID, recipients, notificationTypeAIAnomaly, title, response, payload)
	})
	if err != nil {
		slog.Error("ai anomaly scheduler: process firm", "firmId", firmID, "err", err)
	}
}

// alreadyCheckedTodayTx reports whether this firm's anomaly check has
// already run today - keeps a poll interval shorter than a day (as tests
// use) from calling the AI provider (and potentially creating duplicate
// notifications) more than once per calendar day. Checked via the audit
// log (every check, flagged or not, is audited - see
// writeAnomalyAuditTx), not the notifications table, since an
// unflagged/failed check leaves no notification behind at all.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// processAnomalyCheckForFirm - see listAllFirmIDs's own comment above for
// why firmID here is never caller-supplied.
func alreadyCheckedTodayTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM audit_log
			WHERE firm_id = $1 AND entity_type = $2 AND action = $3
				AND occurred_at::date = now()::date
		)
	`, firmID, configAuditEntityType, anomalyAuditAction).Scan(&exists); err != nil {
		return false, fmt.Errorf("check existing anomaly check audit entry: %w", err)
	}
	return exists, nil
}

// ciaudit:ignore-firmid-check: internal helper called only by
// processAnomalyCheckForFirm - see listAllFirmIDs's own comment above for
// why firmID here is never caller-supplied.
func writeAnomalyAuditTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, cfg Configuration, providerCallSucceeded bool) error {
	// auditlog.Write requires a real, NOT NULL users.id (this schema has
	// no synthetic "AI actor" or "system actor" concept - see this
	// package's own design notes) - the background scheduler has no
	// human caller to attribute to, so it attributes to the firm's own
	// AI configuration row's id instead of a user id... which would
	// violate the NOT NULL FK. Attribute to the configuration owner
	// instead: the same user who created/holds the active AI
	// configuration is a real firm owner, and using their id here is
	// consistent with "the firm's own AI configuration acted, on behalf
	// of whichever owner enabled it."
	var ownerID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT ufr.user_id
		FROM user_firm_roles ufr
		JOIN roles r ON r.id = ufr.role_id AND r.firm_id = ufr.firm_id
		WHERE ufr.firm_id = $1 AND r.is_owner
		ORDER BY ufr.user_id
		LIMIT 1
	`, firmID).Scan(&ownerID); err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return fmt.Errorf("resolve firm owner for anomaly audit entry: %w", err)
	}

	return auditlog.Write(ctx, tx, firmID, ownerID, cfg.ID, configAuditEntityType, anomalyAuditAction, map[string]any{
		"source":                "ai",
		"provider":              string(cfg.Provider),
		"model":                 cfg.Model,
		"providerCallSucceeded": providerCallSucceeded,
	})
}

// accountTotalsForWindowTx returns every account's signed net total
// (COALESCE'd to 0 if the account had no activity - only accounts WITH at
// least one journal_lines row in the window are actually returned, via
// the INNER JOINs below) over the window
// [now() - fromDaysAgo, now() - toDaysAgo) - e.g. (7, 0) is "the last 7
// days," (14, 7) is "the 7 days before that."
//
// PII firewall: this SELECT list is exactly {accounts.code, accounts.name,
// accounts.type, one summed numeric} - no journal_entries.description, no
// journal_entries.source_id, no journal_lines.id, nothing from any other
// table. accounts.name is a firm-chosen chart-of-accounts label (e.g.
// "Sales Revenue," "Office Supplies Expense"), not a customer/person name -
// the "no customer names or PII" safety constraint (Part 5) is about
// business-record content, which this aggregation query structurally
// cannot reach: it only ever touches journal_lines.amount/side and the
// account it posted to.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// processAnomalyCheckForFirm - see listAllFirmIDs's own comment above for
// why firmID here is never caller-supplied.
func accountTotalsForWindowTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, fromDaysAgo, toDaysAgo int) ([]accountPeriodTotal, error) {
	rows, err := tx.Query(ctx, `
		SELECT a.code, a.name, a.type,
			COALESCE(SUM(CASE
				WHEN a.type IN ('revenue', 'liability', 'equity') AND jl.side = 'credit' THEN jl.amount
				WHEN a.type IN ('revenue', 'liability', 'equity') AND jl.side = 'debit' THEN -jl.amount
				WHEN a.type IN ('asset', 'expense') AND jl.side = 'debit' THEN jl.amount
				WHEN a.type IN ('asset', 'expense') AND jl.side = 'credit' THEN -jl.amount
				ELSE 0
			END), 0)::numeric(30,4)::text
		FROM journal_lines jl
		JOIN journal_entries je ON je.id = jl.entry_id
		JOIN accounts a ON a.id = jl.account_id
		WHERE jl.firm_id = $1
			AND je.posted_at >= (now() - ($2 * INTERVAL '1 day'))
			AND je.posted_at < (now() - ($3 * INTERVAL '1 day'))
		GROUP BY a.code, a.name, a.type
		ORDER BY a.code
	`, firmID, fromDaysAgo, toDaysAgo)
	if err != nil {
		return nil, fmt.Errorf("aggregate account totals: %w", err)
	}
	defer rows.Close()

	var totals []accountPeriodTotal
	for rows.Next() {
		var t accountPeriodTotal
		if err := rows.Scan(&t.Code, &t.Name, &t.Type, &t.Net); err != nil {
			return nil, fmt.Errorf("scan account total: %w", err)
		}
		totals = append(totals, t)
	}
	return totals, rows.Err()
}

// listOwnerUserIDsTx returns every user holding an owner role in firmID -
// redeclared per-package, the same way internal/contracts' own scheduler
// redeclares it instead of importing internal/asset for it (see that
// package's own listOwnerUserIDsTx doc comment for the same reasoning).
//
// ciaudit:ignore-firmid-check: internal helper called only by
// processAnomalyCheckForFirm - see listAllFirmIDs's own comment above for
// why firmID here is never caller-supplied.
func listOwnerUserIDsTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT ufr.user_id
		FROM user_firm_roles ufr
		JOIN roles r ON r.id = ufr.role_id AND r.firm_id = ufr.firm_id
		WHERE ufr.firm_id = $1 AND r.is_owner
	`, firmID)
	if err != nil {
		return nil, fmt.Errorf("list owner user ids: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan owner user id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

const anomalySystemPrompt = `You are a financial-review assistant for an ERP system. You will be given a list of account-level totals for the last 7-day period and the 7-day period before that. Each line is "code | name | type | previous-period net | current-period net" in the firm's own currency units.

Look only at whether the CURRENT period's totals look unusual compared to the PREVIOUS period for the SAME account (a large swing, a sign flip, an account suddenly active or inactive) - do not assume any particular trend is good or bad on its own, business volume naturally varies.

If nothing looks unusual, respond with exactly: NOTHING_UNUSUAL
If something looks unusual, respond with a short (2-4 sentence) plain-language summary of what looks unusual and which account(s) it concerns. Do not speculate about causes you cannot know from the numbers alone. Respond with ONLY that summary, no preamble.`

// buildAnomalyPrompt renders current/previous into the "code | name |
// type | previous | current" line format anomalySystemPrompt describes -
// see accountTotalsForWindowTx's own doc comment for the PII-firewall
// guarantee on what these two slices can ever contain.
func buildAnomalyPrompt(current, previous []accountPeriodTotal) string {
	previousByCode := make(map[string]accountPeriodTotal, len(previous))
	for _, p := range previous {
		previousByCode[p.Code] = p
	}
	seen := make(map[string]bool, len(current))

	var b strings.Builder
	b.WriteString("code | name | type | previous-period net | current-period net\n")
	for _, c := range current {
		prev := "0"
		if p, ok := previousByCode[c.Code]; ok {
			prev = p.Net
		}
		seen[c.Code] = true
		fmt.Fprintf(&b, "%s | %s | %s | %s | %s\n", c.Code, c.Name, c.Type, prev, c.Net)
	}
	for _, p := range previous {
		if seen[p.Code] {
			continue
		}
		fmt.Fprintf(&b, "%s | %s | %s | %s | %s\n", p.Code, p.Name, p.Type, p.Net, "0")
	}
	return b.String()
}

// anomalyResponseFlagsSomething reports whether response indicates the AI
// found something unusual - anomalySystemPrompt's own contract is an
// exact "NOTHING_UNUSUAL" sentinel for the negative case, so anything else
// (a real summary) counts as flagged.
func anomalyResponseFlagsSomething(response string) bool {
	return strings.TrimSpace(response) != "NOTHING_UNUSUAL"
}
