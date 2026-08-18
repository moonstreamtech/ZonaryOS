// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// SyncStatus is integration_sync_logs.status's closed vocabulary - a
// real DB CHECK constraint (migrations/0038).
type SyncStatus string

const (
	SyncStatusRunning SyncStatus = "running"
	SyncStatusSuccess SyncStatus = "success"
	SyncStatusPartial SyncStatus = "partial"
	SyncStatusFailed  SyncStatus = "failed"
)

// SyncLog is one integration_sync_logs row.
type SyncLog struct {
	ID               uuid.UUID
	FirmID           uuid.UUID
	ConnectorID      uuid.UUID
	MappingID        *uuid.UUID
	StartedAt        time.Time
	FinishedAt       *time.Time
	RecordsProcessed int
	RecordsFailed    int
	Status           SyncStatus
	ErrorLog         []string
}

const syncLogColumns = "id, firm_id, connector_id, mapping_id, started_at, finished_at, records_processed, records_failed, status, error_log"

func scanSyncLog(row pgx.Row) (SyncLog, error) {
	var l SyncLog
	var statusRaw string
	var errorLogRaw []byte
	if err := row.Scan(&l.ID, &l.FirmID, &l.ConnectorID, &l.MappingID, &l.StartedAt, &l.FinishedAt,
		&l.RecordsProcessed, &l.RecordsFailed, &statusRaw, &errorLogRaw); err != nil {
		return SyncLog{}, err
	}
	l.Status = SyncStatus(statusRaw)
	if len(errorLogRaw) > 0 {
		if err := json.Unmarshal(errorLogRaw, &l.ErrorLog); err != nil {
			return SyncLog{}, fmt.Errorf("unmarshal error log: %w", err)
		}
	}
	return l, nil
}

// ListSyncLogs returns firmID's sync history, most recent first,
// optionally filtered to one connectorID (uuid.Nil means "every
// connector") - member-gated read, same tier as ListConnectors.
func ListSyncLogs(ctx context.Context, pool *pgxpool.Pool, firmID, userID, connectorID uuid.UUID) ([]SyncLog, error) {
	var logs []SyncLog
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		query := `SELECT ` + syncLogColumns + ` FROM integration_sync_logs WHERE firm_id = $1`
		args := []any{firmID}
		if connectorID != uuid.Nil {
			query += ` AND connector_id = $2`
			args = append(args, connectorID)
		}
		query += ` ORDER BY started_at DESC LIMIT 200`

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("list sync logs: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			l, err := scanSyncLog(rows)
			if err != nil {
				return fmt.Errorf("scan sync log: %w", err)
			}
			logs = append(logs, l)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return logs, nil
}

// startSyncLogTx inserts a new 'running' integration_sync_logs row -
// called by DispatchOutbound/ProcessInboundSyncs at the start of one
// sync attempt; finishSyncLogTx closes it out.
//
// ciaudit:ignore-firmid-check: internal helper, called only by this
// package's own outbound.go/inbound.go, both system-triggered (a
// completed entity mutation, or the background scheduler) rather than a
// caller-facing endpoint with its own principal to check - the same
// reasoning internal/webhook.recordDelivery's own doc comment gives for
// its identical shape.
func startSyncLogTx(ctx context.Context, tx pgx.Tx, firmID, connectorID uuid.UUID, mappingID *uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO integration_sync_logs (firm_id, connector_id, mapping_id, status)
		VALUES ($1, $2, $3, 'running')
		RETURNING id
	`, firmID, connectorID, mappingID).Scan(&id); err != nil {
		return uuid.UUID{}, fmt.Errorf("insert sync log: %w", err)
	}
	return id, nil
}

// finishSyncLogTx closes out syncLogID - status is 'success' if
// recordsFailed is 0, 'partial' if some records succeeded and some
// failed, 'failed' if recordsProcessed is 0 and recordsFailed > 0 (or an
// errorMessages entry describes a failure before any record was even
// attempted, e.g. "connector type not yet supported"). Also updates the
// owning connector's own last_sync_at/last_sync_status - so the
// connector list (Part 1's own frontend) can show "last synced" without
// a second query per connector.
//
// ciaudit:ignore-firmid-check: same reasoning as startSyncLogTx above.
func finishSyncLogTx(ctx context.Context, tx pgx.Tx, firmID, connectorID, syncLogID uuid.UUID, recordsProcessed, recordsFailed int, errorMessages []string) error {
	status := SyncStatusSuccess
	switch {
	case recordsFailed > 0 && recordsProcessed > 0:
		status = SyncStatusPartial
	case recordsFailed > 0 || (recordsProcessed == 0 && len(errorMessages) > 0):
		status = SyncStatusFailed
	}

	var errorLogJSON []byte
	if len(errorMessages) > 0 {
		var err error
		errorLogJSON, err = json.Marshal(errorMessages)
		if err != nil {
			return fmt.Errorf("marshal error log: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE integration_sync_logs
		SET finished_at = now(), records_processed = $1, records_failed = $2, status = $3, error_log = $4
		WHERE id = $5
	`, recordsProcessed, recordsFailed, string(status), errorLogJSON, syncLogID); err != nil {
		return fmt.Errorf("finish sync log: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE integration_connectors SET last_sync_at = now(), last_sync_status = $1 WHERE id = $2 AND firm_id = $3
	`, string(status), connectorID, firmID); err != nil {
		return fmt.Errorf("update connector last sync status: %w", err)
	}
	return nil
}
