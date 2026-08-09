// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package notification is the minimal internal notification/inbox system
// (Never-Violate Rule 8's approval workflow, Open Points item 41, needed
// somewhere for a "pending approval" match to actually surface to the
// people who can act on it). It is deliberately generic - a firm-scoped
// row addressed to one recipient_user_id, a type/title/body, and a free-
// form jsonb payload the frontend uses to deep-link back to whatever
// triggered it - not workflow-specific, so any future firm-facing event
// (not just internal/workflow's rule engine, the only caller today) can
// reuse it without a new table.
//
// Scope boundaries (explicit, per this batch's own brief): no email/push
// delivery (Open Points item 17, still deferred) - this is in-app only,
// read via GET .../notifications. No WebSocket/real-time push - the
// frontend polls the unread-count endpoint; that is an accepted,
// documented simplification for a single-instance deployment (item 34),
// not an oversight.
package notification

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

// Notification is one notifications row.
type Notification struct {
	ID              uuid.UUID
	FirmID          uuid.UUID
	RecipientUserID uuid.UUID
	Type            string
	Title           string
	Body            string
	Payload         map[string]any
	ReadAt          *time.Time
	CreatedAt       time.Time
}

// CreateTx inserts one notification within an already-open, firm-scoped
// transaction - the primitive internal/workflow's rule engine (and any
// future caller) builds on: a firm-scoped write is naturally one step
// inside whatever larger operation is creating it (e.g. EvaluateRules'
// own pending-approval flow), the same "*Tx, no authorization of its
// own, caller has already checked" pattern
// internal/workflow.CreateRuleTx/auditlog.Write already establish. There
// is no separate authorization check here: any code running inside an
// already-open WithFirmContext(firmID) transaction is, by construction,
// already past whatever gate let it get there.
//
// ciaudit:ignore-firmid-check: *Tx primitive with no authorization of its
// own by design - every caller (e.g. internal/workflow.createPendingApprovalAndNotify/
// Reject) has already run its own permission.IsMember/Has check before
// opening the transaction this runs inside.
func CreateTx(ctx context.Context, tx pgx.Tx, firmID, recipientUserID uuid.UUID, notifType, title, body string, payload map[string]any) (Notification, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return Notification{}, fmt.Errorf("marshal notification payload: %w", err)
	}

	n := Notification{FirmID: firmID, RecipientUserID: recipientUserID, Type: notifType, Title: title, Body: body, Payload: payload}
	if err := tx.QueryRow(ctx, `
		INSERT INTO notifications (firm_id, recipient_user_id, type, title, body, payload)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at
	`, firmID, recipientUserID, notifType, title, body, payloadJSON).Scan(&n.ID, &n.CreatedAt); err != nil {
		return Notification{}, fmt.Errorf("insert notification: %w", err)
	}
	return n, nil
}

// CreateForRecipientsTx is CreateTx fanned out to every user in
// recipientUserIDs, within the same already-open transaction - what
// EvaluateRules' non-autonomous-rule match uses to notify every firm
// member who holds the pending action's required permission key in one
// atomic step.
//
// ciaudit:ignore-firmid-check: *Tx primitive, same reasoning as CreateTx
// above - no authorization of its own, every caller has already checked.
func CreateForRecipientsTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, recipientUserIDs []uuid.UUID, notifType, title, body string, payload map[string]any) error {
	for _, recipientID := range recipientUserIDs {
		if _, err := CreateTx(ctx, tx, firmID, recipientID, notifType, title, body, payload); err != nil {
			return err
		}
	}
	return nil
}

func scanNotification(row pgx.Row) (Notification, error) {
	var n Notification
	var payloadRaw []byte
	if err := row.Scan(&n.ID, &n.FirmID, &n.RecipientUserID, &n.Type, &n.Title, &n.Body, &payloadRaw, &n.ReadAt, &n.CreatedAt); err != nil {
		return Notification{}, err
	}
	if err := json.Unmarshal(payloadRaw, &n.Payload); err != nil {
		return Notification{}, fmt.Errorf("unmarshal notification payload: %w", err)
	}
	return n, nil
}

const notificationColumns = "id, firm_id, recipient_user_id, type, title, body, payload, read_at, created_at"

// ListOptions controls paging/filtering for ListForUser - same limit/
// offset paging shape as internal/auditlog.ListOptions/
// internal/workflow.ListInstancesOptions.
type ListOptions struct {
	// UnreadOnly, when true, keeps only notifications with read_at IS NULL.
	UnreadOnly bool
	// Limit caps how many notifications are returned. 0 means unlimited.
	Limit int
	// Offset skips this many notifications (after filtering). Ignored
	// when Limit is 0.
	Offset int
}

// ListResult is ListForUser's return shape: the (possibly paged/filtered)
// notifications, plus Total - the count that would match with no Limit/
// Offset applied, mirroring internal/auditlog.ListResult.
type ListResult struct {
	Notifications []Notification
	Total         int
}

// ListForUser returns userID's own notifications within firmID, most
// recent first - member-gated, then additionally filtered to
// recipient_user_id = userID: RLS's own app.current_firm_id scoping only
// confines the query to this firm, it has no notion of "this caller's
// own rows" (see migrations/0019_notifications_approvals_scheduling.up.sql's
// own comment on this), so that narrower scoping is this function's job,
// the same way IsMember/Has already gate every other read beyond RLS's
// firm-only guarantee.
func ListForUser(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListOptions) (ListResult, error) {
	var result ListResult
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var total int
		countQuery := `SELECT count(*) FROM notifications WHERE firm_id = $1 AND recipient_user_id = $2`
		if opts.UnreadOnly {
			countQuery += ` AND read_at IS NULL`
		}
		if err := tx.QueryRow(ctx, countQuery, firmID, userID).Scan(&total); err != nil {
			return fmt.Errorf("count notifications: %w", err)
		}
		result.Total = total

		query := `SELECT ` + notificationColumns + ` FROM notifications WHERE firm_id = $1 AND recipient_user_id = $2`
		if opts.UnreadOnly {
			query += ` AND read_at IS NULL`
		}
		query += ` ORDER BY created_at DESC`
		args := []any{firmID, userID}
		if opts.Limit > 0 {
			query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
			args = append(args, opts.Limit, opts.Offset)
		}

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("list notifications: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			n, err := scanNotification(rows)
			if err != nil {
				return fmt.Errorf("scan notification: %w", err)
			}
			result.Notifications = append(result.Notifications, n)
		}
		return rows.Err()
	})
	if err != nil {
		return ListResult{}, err
	}
	return result, nil
}

// UnreadCount is the lightweight nav-badge query: how many of userID's
// own notifications within firmID are unread. Same member-gating and
// recipient-scoping as ListForUser.
func UnreadCount(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) (int, error) {
	var count int
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM notifications WHERE firm_id = $1 AND recipient_user_id = $2 AND read_at IS NULL
		`, firmID, userID).Scan(&count)
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// MarkRead sets notificationID's read_at to now(), scoped to (firmID,
// recipient_user_id = userID) so a caller can only ever mark their own
// notification read, never another recipient's within the same firm -
// idempotent (marking an already-read notification read again is a
// no-op, not an error).
func MarkRead(ctx context.Context, pool *pgxpool.Pool, firmID, userID, notificationID uuid.UUID) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		tag, err := tx.Exec(ctx, `
			UPDATE notifications SET read_at = COALESCE(read_at, now())
			WHERE id = $1 AND firm_id = $2 AND recipient_user_id = $3
		`, notificationID, firmID, userID)
		if err != nil {
			return fmt.Errorf("mark notification read: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotificationNotFound
		}
		return nil
	})
}
