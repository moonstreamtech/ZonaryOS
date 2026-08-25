// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package notificationprefs lets a firm configure, per event type (invoice
// sent, payment received, ...), whether that event should notify anyone at
// all - a firm-wide setting, not a per-user one: the table has no user_id
// column, same tier as internal/firm's own settings, not
// internal/notification's per-recipient inbox rows. Reading a firm's
// preferences is ordinary member access; changing them is gated behind the
// manage_notification_preferences permission key since it affects every
// member of the firm, not just the caller.
package notificationprefs

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// ManagePermissionKey is the permission_key that gates writing a firm's
// notification preferences (see Update).
const ManagePermissionKey = "manage_notification_preferences"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm.
	ErrFirmNotFound = errors.New("firm not found")
	// ErrForbidden means the caller is a member but lacks ManagePermissionKey.
	ErrForbidden = errors.New("forbidden")
)

// Preference is one firm_notification_preferences row.
type Preference struct {
	FirmID           uuid.UUID
	NotificationType string
	Enabled          bool
}

const preferenceColumns = "firm_id, notification_type, enabled"

func scanPreference(row pgx.Row) (Preference, error) {
	var p Preference
	if err := row.Scan(&p.FirmID, &p.NotificationType, &p.Enabled); err != nil {
		return Preference{}, err
	}
	return p, nil
}

// List returns firmID's notification preferences - member-gated, the same
// tier as reading any other firm setting.
func List(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Preference, error) {
	var prefs []Preference
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT `+preferenceColumns+` FROM firm_notification_preferences
			WHERE firm_id = $1 ORDER BY notification_type
		`, firmID)
		if err != nil {
			return fmt.Errorf("list notification preferences: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			p, err := scanPreference(rows)
			if err != nil {
				return fmt.Errorf("scan notification preference: %w", err)
			}
			prefs = append(prefs, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return prefs, nil
}

// Update upserts each of prefs (matched on notification_type) for firmID -
// gated behind ManagePermissionKey, since it changes behavior for every
// member of the firm, not just the caller.
func Update(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, prefs []Preference) ([]Preference, error) {
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		hasPermission, err := permission.Has(ctx, tx, firmID, userID, ManagePermissionKey)
		if err != nil {
			return err
		}
		if !hasPermission {
			return ErrForbidden
		}

		for _, p := range prefs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO firm_notification_preferences (firm_id, notification_type, enabled)
				VALUES ($1, $2, $3)
				ON CONFLICT (firm_id, notification_type) DO UPDATE SET enabled = $3, updated_at = now()
			`, firmID, p.NotificationType, p.Enabled); err != nil {
				return fmt.Errorf("upsert notification preference %q: %w", p.NotificationType, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return List(ctx, pool, firmID, userID)
}
