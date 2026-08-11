// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package hr is Vision §3's minimal HR foundation: every business has
// people, so a firm-scoped record of who they are (employees,
// contractors, partners) and what contract terms they're under - not a
// payroll/time-tracking/performance-review system (see the migration's
// own scope-boundary comment, migrations/0010_hr_core.up.sql).
//
// Authorization follows this codebase's usual discipline
// (docs/DEVELOPMENT.md's Authorization checklist): IsMember before any
// read, IsOwner before any write - people/contract records are treated
// as firm-structural data (the same tier as the chart of accounts, see
// internal/accounting), not something every member can edit.
package hr

import (
	"context"
	"encoding/json"
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

const personAuditEntityType = "person"

const (
	createPersonAuditAction = "create"
	updatePersonAuditAction = "update"
)

// PersonType is a closed set enforced by the migration's own CHECK
// constraint (migrations/0010_hr_core.up.sql) - validated here too, for
// a clean typed error before the row ever reaches that constraint.
type PersonType string

const (
	PersonTypeEmployee   PersonType = "employee"
	PersonTypeContractor PersonType = "contractor"
	PersonTypePartner    PersonType = "partner"
)

func (t PersonType) valid() bool {
	switch t {
	case PersonTypeEmployee, PersonTypeContractor, PersonTypePartner:
		return true
	default:
		return false
	}
}

// PersonStatus mirrors the migration's status CHECK constraint.
type PersonStatus string

const (
	PersonStatusActive   PersonStatus = "active"
	PersonStatusInactive PersonStatus = "inactive"
)

func (s PersonStatus) valid() bool {
	return s == PersonStatusActive || s == PersonStatusInactive
}

// Person is one people row.
type Person struct {
	ID           uuid.UUID
	FirmID       uuid.UUID
	FullName     string
	Type         PersonType
	Email        *string
	Phone        *string
	StartDate    *time.Time
	EndDate      *time.Time
	Status       PersonStatus
	CustomFields map[string]any
	CreatedAt    time.Time
}

// dateLayout is the plain calendar-date format start_date/end_date parse
// against - matches an HTML <input type="date"> value verbatim, same
// convention internal/workflow's payloadDateLayout uses for its own
// FieldTypeDate fields.
const dateLayout = "2006-01-02"

// parseOptionalDate parses s (expected "YYYY-MM-DD", or empty for "not
// set") into *time.Time - shared by CreatePerson/UpdatePerson/CreateContract.
func parseOptionalDate(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return nil, fmt.Errorf("%w: date %q must be in YYYY-MM-DD format", ErrInvalidPerson, s)
	}
	return &t, nil
}

// CreatePersonInput is CreatePerson's request shape.
type CreatePersonInput struct {
	FullName     string
	Type         PersonType
	Email        string
	Phone        string
	StartDate    string // "YYYY-MM-DD", optional
	EndDate      string // "YYYY-MM-DD", optional
	CustomFields map[string]any
}

func optionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// CreatePerson adds a new person to firmID. Owner-gated: adding a person
// record is a structural firm decision, the same tier as
// internal/accounting.CreateAccount.
func CreatePerson(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreatePersonInput) (Person, error) {
	fullName := strings.TrimSpace(input.FullName)
	if fullName == "" {
		return Person{}, fmt.Errorf("%w: full name must not be empty", ErrInvalidPerson)
	}
	if !input.Type.valid() {
		return Person{}, fmt.Errorf("%w: type must be one of employee/contractor/partner", ErrInvalidPerson)
	}
	startDate, err := parseOptionalDate(input.StartDate)
	if err != nil {
		return Person{}, err
	}
	endDate, err := parseOptionalDate(input.EndDate)
	if err != nil {
		return Person{}, err
	}
	customFields := input.CustomFields
	if customFields == nil {
		customFields = map[string]any{}
	}
	customFieldsJSON, err := json.Marshal(customFields)
	if err != nil {
		return Person{}, fmt.Errorf("marshal custom fields: %w", err)
	}

	var person Person
	err = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
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

		person = Person{
			FirmID: firmID, FullName: fullName, Type: input.Type,
			Email: optionalString(input.Email), Phone: optionalString(input.Phone),
			StartDate: startDate, EndDate: endDate, Status: PersonStatusActive, CustomFields: customFields,
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO people (firm_id, full_name, type, email, phone, start_date, end_date, custom_fields)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id, created_at
		`, firmID, fullName, string(input.Type), person.Email, person.Phone, startDate, endDate, customFieldsJSON).Scan(&person.ID, &person.CreatedAt); err != nil {
			return fmt.Errorf("insert person: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, person.ID, personAuditEntityType, createPersonAuditAction, map[string]any{
			"fullName": fullName, "type": string(input.Type),
		})
	})
	if err != nil {
		return Person{}, err
	}
	return person, nil
}

// PersonUpdate is UpdatePerson's partial-update input - a nil field
// leaves that column unchanged, matching internal/accounting.AccountUpdate's
// convention. Date fields use a nested pointer-to-pointer-like shape
// (*string, "" clears, nil leaves alone) is avoided here in favor of a
// simpler *string contract: a non-nil empty string clears the date back
// to NULL, a non-nil non-empty string sets it, nil leaves it unchanged -
// consistent with optionalString's own "empty means unset" convention
// used throughout this package.
type PersonUpdate struct {
	FullName     *string
	Email        *string
	Phone        *string
	StartDate    *string
	EndDate      *string
	Status       *PersonStatus
	CustomFields map[string]any
}

// UpdatePerson changes personID's mutable fields within firmID.
// Owner-gated, same tier as CreatePerson. Type is immutable once
// created - the same "identity fields don't change" judgment call
// internal/accounting.AccountUpdate makes for an account's code/type.
func UpdatePerson(ctx context.Context, pool *pgxpool.Pool, firmID, userID, personID uuid.UUID, update PersonUpdate) (Person, error) {
	if update.FullName != nil && strings.TrimSpace(*update.FullName) == "" {
		return Person{}, fmt.Errorf("%w: full name must not be empty", ErrInvalidPerson)
	}
	if update.Status != nil && !update.Status.valid() {
		return Person{}, fmt.Errorf("%w: status must be active or inactive", ErrInvalidPerson)
	}

	var startDate, endDate *time.Time
	var startDateSet, endDateSet bool
	if update.StartDate != nil {
		startDateSet = true
		var err error
		startDate, err = parseOptionalDate(*update.StartDate)
		if err != nil {
			return Person{}, err
		}
	}
	if update.EndDate != nil {
		endDateSet = true
		var err error
		endDate, err = parseOptionalDate(*update.EndDate)
		if err != nil {
			return Person{}, err
		}
	}

	var customFieldsJSON []byte
	if update.CustomFields != nil {
		var err error
		customFieldsJSON, err = json.Marshal(update.CustomFields)
		if err != nil {
			return Person{}, fmt.Errorf("marshal custom fields: %w", err)
		}
	}

	// pgx needs a plain *string, not a *PersonStatus (a named string
	// type), to encode this parameter - converted once here rather than
	// asking the query below to know about this package's own types.
	var statusParam *string
	if update.Status != nil {
		s := string(*update.Status)
		statusParam = &s
	}

	var person Person
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

		var typeStr, statusStr string
		var customFieldsRaw []byte
		err = tx.QueryRow(ctx, `
			UPDATE people SET
				full_name = COALESCE($1, full_name),
				email = CASE WHEN $2::boolean THEN $3 ELSE email END,
				phone = CASE WHEN $4::boolean THEN $5 ELSE phone END,
				start_date = CASE WHEN $6::boolean THEN $7::date ELSE start_date END,
				end_date = CASE WHEN $8::boolean THEN $9::date ELSE end_date END,
				status = COALESCE($10, status),
				custom_fields = COALESCE($11, custom_fields)
			WHERE id = $12 AND firm_id = $13
			RETURNING id, firm_id, full_name, type, email, phone, start_date, end_date, status, custom_fields, created_at
		`,
			update.FullName,
			update.Email != nil, optionalString(derefOr(update.Email, "")),
			update.Phone != nil, optionalString(derefOr(update.Phone, "")),
			startDateSet, startDate,
			endDateSet, endDate,
			statusParam,
			nullableJSON(customFieldsJSON),
			personID, firmID,
		).Scan(&person.ID, &person.FirmID, &person.FullName, &typeStr, &person.Email, &person.Phone,
			&person.StartDate, &person.EndDate, &statusStr, &customFieldsRaw, &person.CreatedAt)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrPersonNotFound
			}
			return fmt.Errorf("update person: %w", err)
		}
		person.Type = PersonType(typeStr)
		person.Status = PersonStatus(statusStr)
		if err := json.Unmarshal(customFieldsRaw, &person.CustomFields); err != nil {
			return fmt.Errorf("unmarshal custom fields: %w", err)
		}

		changes := map[string]any{}
		if update.FullName != nil {
			changes["fullName"] = *update.FullName
		}
		if update.Status != nil {
			changes["status"] = string(*update.Status)
		}
		return auditlog.Write(ctx, tx, firmID, userID, person.ID, personAuditEntityType, updatePersonAuditAction, changes)
	})
	if err != nil {
		return Person{}, err
	}
	return person, nil
}

func derefOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

// nullableJSON returns nil (SQL NULL, meaning "no change" via COALESCE
// above) for an empty/nil byte slice, or data itself otherwise.
func nullableJSON(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	return data
}

// ListPeopleOptions filters ListPeople - a zero value returns every
// person, matching this codebase's usual "zero value means unfiltered"
// convention (internal/auditlog.ListOptions, internal/workflow.ListInstancesOptions).
type ListPeopleOptions struct {
	// Status, when non-empty, keeps only people with this status.
	Status PersonStatus
	// Search, when non-empty, keeps only people whose search_tsv
	// (migrations/0024 - full_name/email/phone) matches this text via
	// full-text search.
	Search string
}

// ListPeople returns firmID's people, ordered by full name. Member-gated
// (not owner-gated): reading the roster is ordinary firm data
// visibility, the same tier as internal/accounting.ListAccounts - only
// creating/editing a person is owner-only.
func ListPeople(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListPeopleOptions) ([]Person, error) {
	var people []Person
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, full_name, type, email, phone, start_date, end_date, status, custom_fields, created_at
			FROM people
			WHERE ($1 = '' OR status = $1)
			  AND ($2 = '' OR search_tsv @@ plainto_tsquery('simple', normalize_search_text($2)))
			ORDER BY full_name
		`, string(opts.Status), opts.Search)
		if err != nil {
			return fmt.Errorf("list people: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var p Person
			var typeStr, statusStr string
			var customFieldsRaw []byte
			if err := rows.Scan(&p.ID, &p.FirmID, &p.FullName, &typeStr, &p.Email, &p.Phone, &p.StartDate, &p.EndDate, &statusStr, &customFieldsRaw, &p.CreatedAt); err != nil {
				return err
			}
			p.Type = PersonType(typeStr)
			p.Status = PersonStatus(statusStr)
			if err := json.Unmarshal(customFieldsRaw, &p.CustomFields); err != nil {
				return fmt.Errorf("unmarshal custom fields: %w", err)
			}
			people = append(people, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return people, nil
}

// GetPerson resolves one person by id within firmID. Member-gated, same
// tier as ListPeople.
func GetPerson(ctx context.Context, pool *pgxpool.Pool, firmID, userID, personID uuid.UUID) (Person, error) {
	var person Person
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var typeStr, statusStr string
		var customFieldsRaw []byte
		err = tx.QueryRow(ctx, `
			SELECT id, firm_id, full_name, type, email, phone, start_date, end_date, status, custom_fields, created_at
			FROM people WHERE id = $1 AND firm_id = $2
		`, personID, firmID).Scan(&person.ID, &person.FirmID, &person.FullName, &typeStr, &person.Email, &person.Phone,
			&person.StartDate, &person.EndDate, &statusStr, &customFieldsRaw, &person.CreatedAt)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrPersonNotFound
			}
			return fmt.Errorf("look up person: %w", err)
		}
		person.Type = PersonType(typeStr)
		person.Status = PersonStatus(statusStr)
		return json.Unmarshal(customFieldsRaw, &person.CustomFields)
	})
	if err != nil {
		return Person{}, err
	}
	return person, nil
}

// PersonExistsTx reports whether personID is a real people row within
// firmID - the cross-module check internal/workflow's own
// FieldTypePersonReference payload validation uses (checkPersonField,
// engine.go) to confirm a task's assignee_person_id actually names a
// real person, the same "resolve a foreign reference inside the caller's
// own already-open transaction" shape checkReferenceField (internal/workflow)
// already uses for a workflow-instance reference.
//
// ciaudit:ignore-firmid-check: internal cross-package helper, called only
// by internal/workflow.checkPersonField, itself only reachable from
// CreateInstance after permission.IsMember/Has have already run in the
// same transaction - firmID here scopes the query (defense in depth
// alongside RLS), it is not itself an authorization decision.
func PersonExistsTx(ctx context.Context, tx pgx.Tx, firmID, personID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM people WHERE id = $1 AND firm_id = $2)
	`, personID, firmID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check person exists: %w", err)
	}
	return exists, nil
}
