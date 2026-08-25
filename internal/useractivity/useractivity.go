// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package useractivity is Issue #59's dashboard widget backend: a
// lightweight, firm-scoped feed of recent user actions
// (`user_activity_log`, migrations/0045_user_activity_log.up.sql),
// read-only from this package's own HTTP surface (see handlers.go) -
// member-gated, the same tier as internal/notification's own inbox
// reads, not internal/auditlog's compliance-grade, permission-gated
// trail. Record is provided as the write primitive future call sites
// (login, profile update, ...) can build on, the same "infrastructure
// first, callers wired up incrementally" precedent
// internal/auditlog.LogView's own doc comment already establishes.
package useractivity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// ErrFirmNotFound means the caller isn't a member of the given firm.
var ErrFirmNotFound = errors.New("firm not found")

// Record inserts one user_activity_log row within an already-open,
// firm-scoped transaction - the primitive any future caller (login,
// profile update, ...) builds on, the same "*Tx, no authorization of its
// own, caller has already checked" pattern
// internal/notification.CreateTx/internal/auditlog.Write already
// establish.
//
// ciaudit:ignore-firmid-check: *Tx primitive with no authorization of its
// own by design - every caller has already run its own
// permission.IsMember/Has check before opening the transaction this runs
// inside.
func Record(ctx context.Context, tx pgx.Tx, firmID, userID uuid.UUID, eventType string, eventData map[string]any) error {
	if eventData == nil {
		eventData = map[string]any{}
	}
	eventDataJSON, err := json.Marshal(eventData)
	if err != nil {
		return fmt.Errorf("marshal user activity event data: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_activity_log (firm_id, user_id, event_type, event_data)
		VALUES ($1, $2, $3, $4)
	`, firmID, userID, eventType, eventDataJSON); err != nil {
		return fmt.Errorf("record user activity: %w", err)
	}
	return nil
}

// Entry is one user_activity_log row as returned by List, with the
// acting user's email/display name joined in (from the global `users`
// table), same convention as internal/auditlog.Entry.
type Entry struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	UserEmail  string
	UserName   string
	EventType  string
	EventData  map[string]any
	OccurredAt time.Time
}

// maxListLimit caps ListOptions.Limit, same numeric cap and reasoning as
// internal/auditlog's own maxListLimit.
const maxListLimit = 200

// defaultListLimit is what List returns when the caller doesn't specify
// a limit at all - the widget's own "last 50 events" spec (Issue #59).
const defaultListLimit = 50

// ListOptions controls paging/filtering for List - same limit/offset
// paging shape as internal/auditlog.ListOptions.
type ListOptions struct {
	// Limit caps how many entries are returned. 0 means defaultListLimit.
	Limit int
	// Offset skips this many entries (after filtering).
	Offset int
	// UserID, when non-nil, keeps only entries recorded for that user.
	UserID *uuid.UUID
	// EventType, when non-empty, keeps only entries with this exact
	// event_type.
	EventType string
}

// ListResult is List's return shape: the (possibly paged/filtered)
// entries, plus Total - the count that would match the filters with no
// Limit/Offset applied, mirroring internal/auditlog.ListResult.
type ListResult struct {
	Entries []Entry
	Total   int
}

// List returns firmID's user_activity_log entries, most recent first -
// member-gated (unprivileged, unlike internal/auditlog.List's additional
// ReadPermission check: this is a lightweight activity feed, not the
// compliance audit trail). opts controls paging/filtering; the zero
// value returns the most recent defaultListLimit entries, unfiltered.
func List(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListOptions) (ListResult, error) {
	var result ListResult

	limit := opts.Limit
	if limit <= 0 || limit > maxListLimit {
		limit = defaultListLimit
	}

	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT ual.id, ual.user_id, u.email, u.display_name, ual.event_type,
				ual.event_data, ual.created_at, COUNT(*) OVER()
			FROM user_activity_log ual
			JOIN users u ON u.id = ual.user_id
			WHERE ual.firm_id = $1
			  AND ($2::uuid IS NULL OR ual.user_id = $2)
			  AND ($3 = '' OR ual.event_type = $3)
			ORDER BY ual.created_at DESC
			LIMIT $4 OFFSET $5
		`, firmID, opts.UserID, opts.EventType, limit, opts.Offset)
		if err != nil {
			return fmt.Errorf("list user activity: %w", err)
		}
		defer rows.Close()

		var entries []Entry
		for rows.Next() {
			var e Entry
			var eventDataJSON []byte
			var total int
			if err := rows.Scan(
				&e.ID, &e.UserID, &e.UserEmail, &e.UserName, &e.EventType,
				&eventDataJSON, &e.OccurredAt, &total,
			); err != nil {
				return err
			}
			if err := json.Unmarshal(eventDataJSON, &e.EventData); err != nil {
				return fmt.Errorf("unmarshal user activity event data: %w", err)
			}
			entries = append(entries, e)
			result.Total = total
		}
		result.Entries = entries
		return rows.Err()
	})
	if err != nil {
		return ListResult{}, err
	}
	return result, nil
}
