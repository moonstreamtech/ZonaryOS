// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package timetracking is the time tracking batch's core: time_entries,
// one person's logged hours on one day. Member-gated create (any member
// can log time); a member may only edit/delete their OWN entries.
//
// "Own" is defined by created_by (migrations/0025's own doc comment
// explains why): this codebase has no existing link from a users row to a
// people row (people has no user_id column - a people row is a record
// *about* a person, not necessarily itself a login-capable user), so
// there is no way to derive "this entry's person_id corresponds to this
// caller" from the schema as it stands today. created_by uuid NOT NULL
// REFERENCES users(id), tracking whoever actually submitted the entry, is
// the pragmatic interim rule this batch uses instead - an owner can still
// edit/delete any entry, same "owner override" shape every other owner-
// gated write in this codebase has.
package timetracking

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// dateLayout mirrors internal/hr/internal/payroll's own convention.
const dateLayout = "2006-01-02"

const entryAuditEntityType = "time_entry"
const (
	createEntryAuditAction = "create"
	updateEntryAuditAction = "update"
	deleteEntryAuditAction = "delete"
)

// Entry is one time_entries row.
type Entry struct {
	ID                 uuid.UUID
	FirmID             uuid.UUID
	PersonID           uuid.UUID
	WorkflowInstanceID *uuid.UUID
	CreatedBy          uuid.UUID
	Date               time.Time
	Hours              string
	Description        *string
	Billable           bool
	CreatedAt          time.Time
}

// EntryInput is CreateEntry/UpdateEntry's request shape.
type EntryInput struct {
	PersonID    uuid.UUID
	Date        string // "YYYY-MM-DD", required
	Hours       string // decimal string, required, 0 < hours <= 24
	Description string
	Billable    bool
}

func parseHours(s string) (float64, error) {
	s = strings.TrimSpace(s)
	var h float64
	if _, err := fmt.Sscanf(s, "%f", &h); err != nil || s == "" {
		return 0, fmt.Errorf("%w: hours %q must be a number", ErrInvalidEntry, s)
	}
	if h <= 0 || h > 24 {
		return 0, fmt.Errorf("%w: hours must be greater than 0 and at most 24", ErrInvalidEntry)
	}
	return h, nil
}

// CreateEntry logs a new time entry for input.PersonID within firmID,
// attributed to userID as its creator. Member-gated (any member can log
// time, not just owners - time tracking is everyday self-service data
// entry, not a structural firm decision).
func CreateEntry(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input EntryInput) (Entry, error) {
	if input.PersonID == uuid.Nil {
		return Entry{}, fmt.Errorf("%w: personId must be set", ErrInvalidEntry)
	}
	if input.Date == "" {
		return Entry{}, fmt.Errorf("%w: date must not be empty", ErrInvalidEntry)
	}
	date, err := time.Parse(dateLayout, input.Date)
	if err != nil {
		return Entry{}, fmt.Errorf("%w: date %q must be in YYYY-MM-DD format", ErrInvalidEntry, input.Date)
	}
	if _, err := parseHours(input.Hours); err != nil {
		return Entry{}, err
	}
	var description *string
	if d := strings.TrimSpace(input.Description); d != "" {
		description = &d
	}

	var entry Entry
	err = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var personExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM people WHERE id = $1 AND firm_id = $2)`, input.PersonID, firmID).Scan(&personExists); err != nil {
			return fmt.Errorf("check person exists: %w", err)
		}
		if !personExists {
			return fmt.Errorf("%w: person not found", ErrInvalidEntry)
		}

		entry = Entry{FirmID: firmID, PersonID: input.PersonID, CreatedBy: userID, Date: date, Description: description, Billable: input.Billable}
		if err := tx.QueryRow(ctx, `
			INSERT INTO time_entries (firm_id, person_id, created_by, date, hours, description, billable)
			VALUES ($1, $2, $3, $4, $5::numeric, $6, $7)
			RETURNING id, hours::text, created_at
		`, firmID, input.PersonID, userID, date, input.Hours, description, input.Billable).Scan(&entry.ID, &entry.Hours, &entry.CreatedAt); err != nil {
			return fmt.Errorf("insert time entry: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, entry.ID, entryAuditEntityType, createEntryAuditAction, map[string]any{
			"personId": input.PersonID.String(), "date": input.Date, "hours": input.Hours,
		})
	})
	if err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// ListEntriesOptions filters ListEntries.
type ListEntriesOptions struct {
	PersonID uuid.UUID // zero value means unfiltered
	From, To string    // "YYYY-MM-DD", empty means unbounded
}

// ListEntries returns firmID's time entries, most recent date first.
// Member-gated read: every member can see the firm's whole time sheet,
// same tier as internal/hr.ListPeople - only editing/deleting is
// restricted to the entry's own creator (or an owner).
func ListEntries(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListEntriesOptions) ([]Entry, error) {
	var entries []Entry
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var personFilter *uuid.UUID
		if opts.PersonID != uuid.Nil {
			personFilter = &opts.PersonID
		}
		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, person_id, workflow_instance_id, created_by, date, hours::text, description, billable, created_at
			FROM time_entries
			WHERE firm_id = $1
			  AND ($2::uuid IS NULL OR person_id = $2)
			  AND ($3 = '' OR date >= $3::date)
			  AND ($4 = '' OR date <= $4::date)
			ORDER BY date DESC, created_at DESC
		`, firmID, personFilter, opts.From, opts.To)
		if err != nil {
			return fmt.Errorf("list time entries: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var e Entry
			if err := rows.Scan(&e.ID, &e.FirmID, &e.PersonID, &e.WorkflowInstanceID, &e.CreatedBy, &e.Date, &e.Hours, &e.Description, &e.Billable, &e.CreatedAt); err != nil {
				return err
			}
			entries = append(entries, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// GetEntry resolves one time entry by id within firmID. Member-gated,
// same tier as ListEntries.
func GetEntry(ctx context.Context, pool *pgxpool.Pool, firmID, userID, entryID uuid.UUID) (Entry, error) {
	var entry Entry
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		return getEntryTx(ctx, tx, firmID, entryID, &entry)
	})
	if err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// getEntryTx loads entryID within firmID into *out.
//
// ciaudit:ignore-firmid-check: internal helper, only called by GetEntry/
// UpdateEntry/DeleteEntry, each of which has already run
// permission.IsMember before reaching this call - firmID here scopes the
// query, not a fresh authorization decision.
func getEntryTx(ctx context.Context, tx pgx.Tx, firmID, entryID uuid.UUID, out *Entry) error {
	err := tx.QueryRow(ctx, `
		SELECT id, firm_id, person_id, workflow_instance_id, created_by, date, hours::text, description, billable, created_at
		FROM time_entries WHERE id = $1 AND firm_id = $2
	`, entryID, firmID).Scan(&out.ID, &out.FirmID, &out.PersonID, &out.WorkflowInstanceID, &out.CreatedBy, &out.Date, &out.Hours, &out.Description, &out.Billable, &out.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrEntryNotFound
		}
		return fmt.Errorf("look up time entry: %w", err)
	}
	return nil
}

// checkEntryEditableTx enforces this package's edit-restriction rule:
// userID must either be the entry's own creator or hold an owner-flagged
// role - see this package's own doc comment for why "own entries" is
// keyed on created_by, not a users->people link.
func checkEntryEditableTx(ctx context.Context, tx pgx.Tx, firmID, userID uuid.UUID, entry Entry) error {
	if entry.CreatedBy == userID {
		return nil
	}
	isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
	if err != nil {
		return err
	}
	if !isOwner {
		return ErrNotEntryOwner
	}
	return nil
}

// UpdateEntry changes entryID's mutable fields within firmID. Restricted
// to the entry's own creator, or an owner - see this package's own doc
// comment.
func UpdateEntry(ctx context.Context, pool *pgxpool.Pool, firmID, userID, entryID uuid.UUID, input EntryInput) (Entry, error) {
	var date time.Time
	if input.Date != "" {
		var err error
		date, err = time.Parse(dateLayout, input.Date)
		if err != nil {
			return Entry{}, fmt.Errorf("%w: date %q must be in YYYY-MM-DD format", ErrInvalidEntry, input.Date)
		}
	}
	if input.Hours != "" {
		if _, err := parseHours(input.Hours); err != nil {
			return Entry{}, err
		}
	}
	var description *string
	if d := strings.TrimSpace(input.Description); d != "" {
		description = &d
	}

	var entry Entry
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		if err := getEntryTx(ctx, tx, firmID, entryID, &entry); err != nil {
			return err
		}
		if err := checkEntryEditableTx(ctx, tx, firmID, userID, entry); err != nil {
			return err
		}

		if input.Date == "" {
			date = entry.Date
		}
		hours := entry.Hours
		if input.Hours != "" {
			hours = input.Hours
		}

		if err := tx.QueryRow(ctx, `
			UPDATE time_entries SET date = $1, hours = $2::numeric, description = COALESCE($3, description), billable = $4
			WHERE id = $5 AND firm_id = $6
			RETURNING id, firm_id, person_id, workflow_instance_id, created_by, date, hours::text, description, billable, created_at
		`, date, hours, description, input.Billable, entryID, firmID).Scan(
			&entry.ID, &entry.FirmID, &entry.PersonID, &entry.WorkflowInstanceID, &entry.CreatedBy,
			&entry.Date, &entry.Hours, &entry.Description, &entry.Billable, &entry.CreatedAt,
		); err != nil {
			return fmt.Errorf("update time entry: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, entry.ID, entryAuditEntityType, updateEntryAuditAction, map[string]any{"hours": hours})
	})
	if err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// DeleteEntry removes entryID within firmID. Restricted to the entry's
// own creator, or an owner - same rule as UpdateEntry.
func DeleteEntry(ctx context.Context, pool *pgxpool.Pool, firmID, userID, entryID uuid.UUID) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var entry Entry
		if err := getEntryTx(ctx, tx, firmID, entryID, &entry); err != nil {
			return err
		}
		if err := checkEntryEditableTx(ctx, tx, firmID, userID, entry); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `DELETE FROM time_entries WHERE id = $1 AND firm_id = $2`, entryID, firmID); err != nil {
			return fmt.Errorf("delete time entry: %w", err)
		}
		return auditlog.Write(ctx, tx, firmID, userID, entryID, entryAuditEntityType, deleteEntryAuditAction, map[string]any{})
	})
}
