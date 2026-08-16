// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Contract-expiry notification scheduler: a background goroutine, same
// shape and same reasoning as internal/asset/scheduler.go's own
// maintenance-due scheduler - a new, dedicated goroutine rather than
// reusing internal/workflow's scheduled_rule_runs (contract_registry
// rows aren't workflow_rules rows, and forcing them through that
// mechanism would mean synthesizing a fake rule per contract for no
// benefit). Checks daily for active, non-auto-renewing contracts whose
// end_date falls within their own renewal_notice_days (a per-contract
// value, not a fixed lookahead the way asset maintenance's 7-day window
// is - a supplier contract with a 90-day notice clause and one with a
// 15-day clause each need their own window). Recipient resolution is the
// same judgment call internal/asset/scheduler.go's own doc comment
// explains: contract_registry has no single reliable assignee (signed_by
// is nullable and only set once a contract is actually signed), so this
// scheduler notifies every user holding an owner role in the firm,
// same recipient set internal/permission.IsOwner itself checks against.
package contracts

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/notification"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// ExpirySchedulerPollInterval is the background scheduler's production
// poll cadence - "checks daily" per this batch's own brief, same
// cadence internal/asset.MaintenanceSchedulerPollInterval uses. Tests
// pass a much shorter interval directly to RunExpiryScheduler so a real
// end-to-end firing can be observed without waiting a full day.
const ExpirySchedulerPollInterval = 24 * time.Hour

// defaultRenewalNoticeDays is used when a contract's own
// renewal_notice_days is NULL - contract_registry.renewal_notice_days
// DEFAULT is 30 at the DB level, but existing rows written before that
// default existed, or an explicit NULL, still need a fallback window
// here rather than never surfacing as "coming due" at all.
const defaultRenewalNoticeDays = 30

const notificationTypeContractExpiring = "contract_expiring"

// RunExpiryScheduler runs ProcessExpiringContracts every pollInterval
// until ctx is cancelled - cmd/server/main.go starts this with
// ExpirySchedulerPollInterval; tests pass a short interval (e.g. 100ms)
// to observe a real firing without waiting on real-world days.
func RunExpiryScheduler(ctx context.Context, pool *pgxpool.Pool, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ProcessExpiringContracts(ctx, pool)
		}
	}
}

// ProcessExpiringContracts is RunExpiryScheduler's testable core: find
// every active, non-auto-renewing contract whose end_date is within its
// own renewal_notice_days, across every firm, and notify that firm's
// owners - at most once per contract per calendar day (see
// alreadyNotifiedTodayTx below).
func ProcessExpiringContracts(ctx context.Context, pool *pgxpool.Pool) {
	firmIDs, err := listAllFirmIDs(ctx, pool)
	if err != nil {
		slog.Error("contracts scheduler: list firms", "err", err)
		return
	}
	for _, firmID := range firmIDs {
		processExpiringContractsForFirm(ctx, pool, firmID)
	}
}

// listAllFirmIDs reads every firms.id - firms carries no RLS at all (it
// IS the tenant boundary, not tenant-scoped data), the same
// justification internal/workflow/scheduler.go's own listAllFirmIDs
// gives for the identical query (internal/asset/scheduler.go
// independently redeclares the same helper for the same reason).
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

type expiringContract struct {
	id              uuid.UUID
	referenceNumber string
	title           string
	endDate         time.Time
}

// ciaudit:ignore-firmid-check: called only by ProcessExpiringContracts,
// with firmID drawn from listAllFirmIDs (an internal, unscoped read of
// the firms table itself) - never from an HTTP caller.
func processExpiringContractsForFirm(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) {
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, reference_number, title, end_date
			FROM contract_registry
			WHERE firm_id = $1 AND status = 'active' AND NOT auto_renewal AND end_date IS NOT NULL
				AND end_date <= (now() + (COALESCE(renewal_notice_days, $2) * INTERVAL '1 day'))
		`, firmID, defaultRenewalNoticeDays)
		if err != nil {
			return fmt.Errorf("list expiring contracts: %w", err)
		}
		var expiring []expiringContract
		for rows.Next() {
			var c expiringContract
			if err := rows.Scan(&c.id, &c.referenceNumber, &c.title, &c.endDate); err != nil {
				rows.Close()
				return fmt.Errorf("scan expiring contract: %w", err)
			}
			expiring = append(expiring, c)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()

		if len(expiring) == 0 {
			return nil
		}

		recipients, err := listOwnerUserIDsTx(ctx, tx, firmID)
		if err != nil {
			return err
		}
		if len(recipients) == 0 {
			return nil
		}

		for _, c := range expiring {
			alreadyNotified, err := alreadyNotifiedTodayTx(ctx, tx, firmID, c.id)
			if err != nil {
				return err
			}
			if alreadyNotified {
				continue
			}

			title := fmt.Sprintf("Contract expiring: %s", c.title)
			body := fmt.Sprintf("Contract %q (%s) expires %s.", c.title, c.referenceNumber, c.endDate.Format("2006-01-02"))
			payload := map[string]any{
				"contractId": c.id.String(),
				"endDate":    c.endDate.Format("2006-01-02"),
			}
			if err := notification.CreateForRecipientsTx(ctx, tx, firmID, recipients, notificationTypeContractExpiring, title, body, payload); err != nil {
				return fmt.Errorf("notify contract expiring for contract %s: %w", c.id, err)
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("contracts scheduler: process expiring contracts", "firmId", firmID, "err", err)
	}
}

// alreadyNotifiedTodayTx reports whether a contract_expiring
// notification for contractID has already been created today - keeps a
// poll interval shorter than a day (as tests use) from creating
// duplicate notifications for the same contract on the same calendar
// day, same "notifications.payload->>'X' + created_at::date" dedup
// internal/asset/scheduler.go's own alreadyNotifiedTodayTx uses.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// processExpiringContractsForFirm, itself only reachable from the
// background scheduler with a firmID drawn from listAllFirmIDs - never
// from an HTTP caller, so there is no caller-supplied firmID to validate
// membership against here.
func alreadyNotifiedTodayTx(ctx context.Context, tx pgx.Tx, firmID, contractID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM notifications
			WHERE firm_id = $1 AND type = $2 AND payload->>'contractId' = $3
				AND created_at::date = now()::date
		)
	`, firmID, notificationTypeContractExpiring, contractID.String()).Scan(&exists); err != nil {
		return false, fmt.Errorf("check existing contract expiring notification: %w", err)
	}
	return exists, nil
}

// listOwnerUserIDsTx returns every user holding an owner role in firmID -
// the same recipient set internal/permission.IsOwner itself checks
// against, and this scheduler's own doc comment explains why.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// processExpiringContractsForFirm - see alreadyNotifiedTodayTx's own
// comment above for why firmID here is never caller-supplied.
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
