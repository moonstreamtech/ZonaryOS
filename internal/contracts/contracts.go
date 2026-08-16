// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package contracts is the contracts management / document workflows /
// legal foundation module: a general-purpose contract registry
// (customer/supplier/employment/nda/service/other - distinct from
// internal/hr's own `contracts` table, which is an employment/
// compensation-term record scoped to one person, not a general legal
// contract), attached contract_documents, an expiry query, and a daily
// background scheduler notifying firm owners of contracts coming due for
// renewal. Not a full legal suite: no e-signature integration (Open
// Points item 17), no version control for contract text (a URL to an
// external document, not stored content), no new approval mechanism (see
// docs/DEVELOPMENT.md's own reasoning for why pending_approvals isn't
// wired here).
//
// Authorization follows this codebase's usual discipline: IsMember
// before any read, IsMember then IsOwner before any write - the same
// tier internal/asset's own mutations use.
package contracts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const (
	contractAuditEntityType = "contract"

	createAuditAction = "create"
	updateAuditAction = "update"
)

// postgresUniqueViolation is the SQLSTATE code Postgres raises for a
// UNIQUE constraint violation - same constant/reasoning as every other
// package in this codebase that redeclares it locally.
const postgresUniqueViolation = "23505"

var contractTypes = map[string]bool{
	"customer": true, "supplier": true, "employment": true, "nda": true, "service": true, "other": true,
}

var contractStatuses = map[string]bool{
	"draft": true, "review": true, "active": true, "expired": true, "terminated": true, "renewed": true,
}

var counterpartyTypes = map[string]bool{
	"customer": true, "supplier": true, "person": true, "other": true,
}

// Contract is one contract_registry row.
type Contract struct {
	ID                uuid.UUID
	FirmID            uuid.UUID
	ReferenceNumber   string
	Title             string
	Type              string
	CounterpartyName  string
	CounterpartyID    *uuid.UUID
	CounterpartyType  *string
	Status            string
	Value             *string
	Currency          string
	StartDate         *time.Time
	EndDate           *time.Time
	AutoRenewal       bool
	RenewalNoticeDays *int
	SignedAt          *time.Time
	SignedBy          *uuid.UUID
	Notes             *string
	CustomFields      map[string]any
	CreatedAt         time.Time
}

// CreateContractInput is CreateContract's request shape. StartDate/
// EndDate are plain "YYYY-MM-DD" strings (parsed via parseOptionalDate),
// the same convention internal/asset's own CreateAssetInput uses for
// DATE-column fields.
type CreateContractInput struct {
	ReferenceNumber   string
	Title             string
	Type              string
	CounterpartyName  string
	CounterpartyID    *uuid.UUID
	CounterpartyType  string
	Status            string // optional, defaults to "draft" (contract_registry's own DB default)
	Value             *string
	Currency          string
	StartDate         string
	EndDate           string
	AutoRenewal       bool
	RenewalNoticeDays *int
	Notes             string
	CustomFields      map[string]any
}

func validateContractFields(referenceNumber, title, typ, counterpartyName, counterpartyType, status string) (string, string, string, error) {
	referenceNumber = strings.TrimSpace(referenceNumber)
	if referenceNumber == "" {
		return "", "", "", fmt.Errorf("%w: reference number must not be empty", ErrInvalidContract)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return "", "", "", fmt.Errorf("%w: title must not be empty", ErrInvalidContract)
	}
	if !contractTypes[typ] {
		return "", "", "", fmt.Errorf("%w: type %q is not one of customer/supplier/employment/nda/service/other", ErrInvalidContract, typ)
	}
	counterpartyName = strings.TrimSpace(counterpartyName)
	if counterpartyName == "" {
		return "", "", "", fmt.Errorf("%w: counterparty name must not be empty", ErrInvalidContract)
	}
	if counterpartyType != "" && !counterpartyTypes[counterpartyType] {
		return "", "", "", fmt.Errorf("%w: counterparty type %q is not one of customer/supplier/person/other", ErrInvalidContract, counterpartyType)
	}
	if status != "" && !contractStatuses[status] {
		return "", "", "", fmt.Errorf("%w: status %q is not one of draft/review/active/expired/terminated/renewed", ErrInvalidContract, status)
	}
	return referenceNumber, title, counterpartyName, nil
}

// CreateContract adds a new contract to firmID. Owner-gated.
func CreateContract(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateContractInput) (Contract, error) {
	referenceNumber, title, counterpartyName, err := validateContractFields(
		input.ReferenceNumber, input.Title, input.Type, input.CounterpartyName, input.CounterpartyType, input.Status)
	if err != nil {
		return Contract{}, err
	}
	status := input.Status
	if status == "" {
		status = "draft"
	}
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = "TRY"
	}
	if input.CustomFields == nil {
		input.CustomFields = map[string]any{}
	}
	startDate, err := parseOptionalDate(input.StartDate)
	if err != nil {
		return Contract{}, fmt.Errorf("%w: startDate must be YYYY-MM-DD", ErrInvalidContract)
	}
	endDate, err := parseOptionalDate(input.EndDate)
	if err != nil {
		return Contract{}, fmt.Errorf("%w: endDate must be YYYY-MM-DD", ErrInvalidContract)
	}

	var c Contract
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

		row := tx.QueryRow(ctx, `
			INSERT INTO contract_registry (
				firm_id, reference_number, title, type, counterparty_name, counterparty_id, counterparty_type,
				status, value, currency, start_date, end_date, auto_renewal, renewal_notice_days, notes, custom_fields
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			RETURNING `+contractColumns, firmID, referenceNumber, title, input.Type, counterpartyName, input.CounterpartyID,
			optionalString(input.CounterpartyType), status, input.Value, currency, startDate, endDate,
			input.AutoRenewal, input.RenewalNoticeDays, optionalString(input.Notes), input.CustomFields)
		var scanErr error
		c, scanErr = scanContractRow(row)
		if scanErr != nil {
			var pgErr *pgconn.PgError
			if errors.As(scanErr, &pgErr) && pgErr.Code == postgresUniqueViolation {
				return ErrDuplicateReferenceNumber
			}
			return fmt.Errorf("insert contract: %w", scanErr)
		}

		return auditlog.Write(ctx, tx, firmID, userID, c.ID, contractAuditEntityType, createAuditAction, map[string]any{"title": title, "referenceNumber": referenceNumber})
	})
	if err != nil {
		return Contract{}, err
	}
	return c, nil
}

// UpdateContractInput is UpdateContract's partial-update input - nil
// leaves that field unchanged, the same convention internal/asset's own
// UpdateAssetInput uses.
type UpdateContractInput struct {
	Title             *string
	Status            *string
	CounterpartyID    *uuid.UUID
	HasCounterpartyID bool
	Value             *string
	StartDate         *string
	EndDate           *string
	AutoRenewal       *bool
	RenewalNoticeDays *int
	SignedAt          *time.Time
	SignedBy          *uuid.UUID
	Notes             *string
	CustomFields      map[string]any
}

// UpdateContract applies input's changes to contractID. Owner-gated.
func UpdateContract(ctx context.Context, pool *pgxpool.Pool, firmID, userID, contractID uuid.UUID, input UpdateContractInput) (Contract, error) {
	if input.Title != nil && strings.TrimSpace(*input.Title) == "" {
		return Contract{}, fmt.Errorf("%w: title must not be empty", ErrInvalidContract)
	}
	if input.Status != nil && !contractStatuses[*input.Status] {
		return Contract{}, fmt.Errorf("%w: status %q is not one of draft/review/active/expired/terminated/renewed", ErrInvalidContract, *input.Status)
	}
	var startDate, endDate *time.Time
	if input.StartDate != nil {
		d, err := parseOptionalDate(*input.StartDate)
		if err != nil {
			return Contract{}, fmt.Errorf("%w: startDate must be YYYY-MM-DD", ErrInvalidContract)
		}
		startDate = d
	}
	if input.EndDate != nil {
		d, err := parseOptionalDate(*input.EndDate)
		if err != nil {
			return Contract{}, fmt.Errorf("%w: endDate must be YYYY-MM-DD", ErrInvalidContract)
		}
		endDate = d
	}

	var c Contract
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

		row := tx.QueryRow(ctx, `
			UPDATE contract_registry SET
				title = COALESCE($3, title),
				status = COALESCE($4, status),
				counterparty_id = CASE WHEN $5::boolean THEN $6 ELSE counterparty_id END,
				value = COALESCE($7, value),
				start_date = COALESCE($8, start_date),
				end_date = COALESCE($9, end_date),
				auto_renewal = COALESCE($10, auto_renewal),
				renewal_notice_days = COALESCE($11, renewal_notice_days),
				signed_at = COALESCE($12, signed_at),
				signed_by = COALESCE($13, signed_by),
				notes = COALESCE($14, notes),
				custom_fields = COALESCE($15, custom_fields)
			WHERE id = $1 AND firm_id = $2
			RETURNING `+contractColumns, contractID, firmID, input.Title, input.Status, input.HasCounterpartyID, input.CounterpartyID,
			input.Value, startDate, endDate, input.AutoRenewal, input.RenewalNoticeDays, input.SignedAt, input.SignedBy,
			input.Notes, input.CustomFields)
		var scanErr error
		c, scanErr = scanContractRow(row)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return ErrContractNotFound
		}
		if scanErr != nil {
			var pgErr *pgconn.PgError
			if errors.As(scanErr, &pgErr) && pgErr.Code == postgresUniqueViolation {
				return ErrDuplicateReferenceNumber
			}
			return fmt.Errorf("update contract: %w", scanErr)
		}

		return auditlog.Write(ctx, tx, firmID, userID, c.ID, contractAuditEntityType, updateAuditAction, map[string]any{})
	})
	if err != nil {
		return Contract{}, err
	}
	return c, nil
}

// ListContracts returns firmID's contracts, ordered by created_at
// descending. Member-gated.
func ListContracts(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Contract, error) {
	var contractsList []Contract
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+contractColumns+` FROM contract_registry WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list contracts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanContractRow(rows)
			if err != nil {
				return err
			}
			contractsList = append(contractsList, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return contractsList, nil
}

// GetContract resolves one contract by id within firmID. Member-gated.
func GetContract(ctx context.Context, pool *pgxpool.Pool, firmID, userID, contractID uuid.UUID) (Contract, error) {
	var c Contract
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+contractColumns+` FROM contract_registry WHERE id = $1 AND firm_id = $2`, contractID, firmID)
		var scanErr error
		c, scanErr = scanContractRow(row)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return ErrContractNotFound
		}
		return scanErr
	})
	if err != nil {
		return Contract{}, err
	}
	return c, nil
}

// ListExpiringContracts returns firmID's active, non-auto-renewing
// contracts whose end_date falls within the next days days, ordered by
// end_date ascending (soonest first) - the same predicate
// GET .../contracts/expiring?days= and the background expiry scheduler
// both use. Member-gated.
func ListExpiringContracts(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, days int) ([]Contract, error) {
	var contractsList []Contract
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT `+contractColumns+` FROM contract_registry
			WHERE firm_id = $1 AND status = 'active' AND NOT auto_renewal
				AND end_date IS NOT NULL AND end_date <= (now() + ($2 * INTERVAL '1 day'))
			ORDER BY end_date
		`, firmID, days)
		if err != nil {
			return fmt.Errorf("list expiring contracts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanContractRow(rows)
			if err != nil {
				return err
			}
			contractsList = append(contractsList, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return contractsList, nil
}

// ExistsTx reports whether contractID exists within firmID - the
// existence-check primitive internal/workflow's contractValidator
// (FieldTypeContract) calls, same shape as internal/asset.AssetExistsTx.
//
// ciaudit:ignore-firmid-check: read-only existence helper, no data
// returned beyond a boolean - every caller already established its own
// firm/permission context before calling this.
func ExistsTx(ctx context.Context, tx pgx.Tx, firmID, contractID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM contract_registry WHERE id = $1 AND firm_id = $2)
	`, contractID, firmID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check contract exists: %w", err)
	}
	return exists, nil
}

const contractColumns = `
	id, firm_id, reference_number, title, type, counterparty_name, counterparty_id, counterparty_type,
	status, value::text, currency, start_date, end_date, auto_renewal, renewal_notice_days,
	signed_at, signed_by, notes, custom_fields, created_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanContractRow(row rowScanner) (Contract, error) {
	var c Contract
	if err := row.Scan(
		&c.ID, &c.FirmID, &c.ReferenceNumber, &c.Title, &c.Type, &c.CounterpartyName, &c.CounterpartyID, &c.CounterpartyType,
		&c.Status, &c.Value, &c.Currency, &c.StartDate, &c.EndDate, &c.AutoRenewal, &c.RenewalNoticeDays,
		&c.SignedAt, &c.SignedBy, &c.Notes, &c.CustomFields, &c.CreatedAt,
	); err != nil {
		return Contract{}, err
	}
	return c, nil
}

// optionalString returns nil for an empty/whitespace-only string, else a
// pointer to the trimmed value - same helper internal/asset's own package
// defines locally.
func optionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// parseOptionalDate parses a plain "YYYY-MM-DD" string into a *time.Time,
// or returns nil for an empty string - same helper/convention
// internal/asset's own parseOptionalDate uses for DATE-column fields.
func parseOptionalDate(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
