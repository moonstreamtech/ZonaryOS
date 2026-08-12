// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package payroll

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

const entryAuditEntityType = "payroll_entry"
const (
	createEntryAuditAction = "create"
	updateEntryAuditAction = "update"
)

// defaultEntryCurrency mirrors internal/hr's own defaultContractCurrency
// convention.
const defaultEntryCurrency = "TRY"

// Entry is one payroll_entries row.
type Entry struct {
	ID          uuid.UUID
	FirmID      uuid.UUID
	PeriodID    uuid.UUID
	PersonID    uuid.UUID
	ContractID  *uuid.UUID
	GrossAmount string
	Deductions  map[string]any
	NetAmount   string
	Currency    string
	Notes       *string
	CreatedAt   time.Time
}

// EntryInput is CreateEntry/UpdateEntry's request shape.
type EntryInput struct {
	PersonID    uuid.UUID
	ContractID  *uuid.UUID
	GrossAmount string // decimal string, required
	Deductions  map[string]any
	NetAmount   string // decimal string, required
	Currency    string
	Notes       string
}

func validateAmount(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if !amountPattern.MatchString(value) {
		return "", fmt.Errorf("%w: %s %q must be a positive decimal with at most 4 fraction digits", ErrInvalidEntry, field, value)
	}
	return value, nil
}

// CreateEntry adds a new payroll entry to periodID within firmID. Owner-
// gated, same tier as CreatePeriod. Rejected once the period is no longer
// 'open' (ErrPeriodNotOpen) - entries are only editable pre-close.
func CreateEntry(ctx context.Context, pool *pgxpool.Pool, firmID, userID, periodID uuid.UUID, input EntryInput) (Entry, error) {
	gross, err := validateAmount("grossAmount", input.GrossAmount)
	if err != nil {
		return Entry{}, err
	}
	net, err := validateAmount("netAmount", input.NetAmount)
	if err != nil {
		return Entry{}, err
	}
	if input.PersonID == uuid.Nil {
		return Entry{}, fmt.Errorf("%w: personId must be set", ErrInvalidEntry)
	}
	deductions := input.Deductions
	if deductions == nil {
		deductions = map[string]any{}
	}
	deductionsJSON, err := json.Marshal(deductions)
	if err != nil {
		return Entry{}, fmt.Errorf("marshal deductions: %w", err)
	}
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = defaultEntryCurrency
	}
	var notes *string
	if n := strings.TrimSpace(input.Notes); n != "" {
		notes = &n
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
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotOwner
		}

		var period Period
		if err := getPeriodTx(ctx, tx, firmID, periodID, &period); err != nil {
			return err
		}
		if period.Status != PeriodStatusOpen {
			return ErrPeriodNotOpen
		}

		var personExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM people WHERE id = $1 AND firm_id = $2)`, input.PersonID, firmID).Scan(&personExists); err != nil {
			return fmt.Errorf("check person exists: %w", err)
		}
		if !personExists {
			return fmt.Errorf("%w: person not found", ErrInvalidEntry)
		}

		entry = Entry{
			FirmID: firmID, PeriodID: periodID, PersonID: input.PersonID, ContractID: input.ContractID,
			Currency: currency, Notes: notes,
		}
		var deductionsRaw []byte
		// RETURNING gross_amount::text/net_amount::text (not the raw
		// caller-submitted strings) - same reasoning as internal/hr's
		// CreateContract: the response always reflects the DB's own
		// normalized numeric(19,4) form.
		if err := tx.QueryRow(ctx, `
			INSERT INTO payroll_entries (firm_id, period_id, person_id, contract_id, gross_amount, deductions, net_amount, currency, notes)
			VALUES ($1, $2, $3, $4, $5::numeric, $6, $7::numeric, $8, $9)
			RETURNING id, gross_amount::text, deductions, net_amount::text, created_at
		`, firmID, periodID, input.PersonID, input.ContractID, gross, deductionsJSON, net, currency, notes,
		).Scan(&entry.ID, &entry.GrossAmount, &deductionsRaw, &entry.NetAmount, &entry.CreatedAt); err != nil {
			return fmt.Errorf("insert payroll entry: %w", err)
		}
		if err := json.Unmarshal(deductionsRaw, &entry.Deductions); err != nil {
			return fmt.Errorf("unmarshal deductions: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, entry.ID, entryAuditEntityType, createEntryAuditAction, map[string]any{
			"periodId": periodID.String(), "personId": input.PersonID.String(), "grossAmount": gross, "netAmount": net,
		})
	})
	if err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// ListEntries returns periodID's payroll entries within firmID. Member-
// gated, same tier as ListPeriods.
func ListEntries(ctx context.Context, pool *pgxpool.Pool, firmID, userID, periodID uuid.UUID) ([]Entry, error) {
	var entries []Entry
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, period_id, person_id, contract_id, gross_amount::text, deductions, net_amount::text, currency, notes, created_at
			FROM payroll_entries WHERE period_id = $1 AND firm_id = $2 ORDER BY created_at
		`, periodID, firmID)
		if err != nil {
			return fmt.Errorf("list payroll entries: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var e Entry
			var deductionsRaw []byte
			if err := rows.Scan(&e.ID, &e.FirmID, &e.PeriodID, &e.PersonID, &e.ContractID, &e.GrossAmount, &deductionsRaw, &e.NetAmount, &e.Currency, &e.Notes, &e.CreatedAt); err != nil {
				return err
			}
			if err := json.Unmarshal(deductionsRaw, &e.Deductions); err != nil {
				return fmt.Errorf("unmarshal deductions: %w", err)
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

// GetEntry resolves one payroll entry by id within firmID. Member-gated,
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

		var deductionsRaw []byte
		err = tx.QueryRow(ctx, `
			SELECT id, firm_id, period_id, person_id, contract_id, gross_amount::text, deductions, net_amount::text, currency, notes, created_at
			FROM payroll_entries WHERE id = $1 AND firm_id = $2
		`, entryID, firmID).Scan(&entry.ID, &entry.FirmID, &entry.PeriodID, &entry.PersonID, &entry.ContractID,
			&entry.GrossAmount, &deductionsRaw, &entry.NetAmount, &entry.Currency, &entry.Notes, &entry.CreatedAt)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrEntryNotFound
			}
			return fmt.Errorf("look up payroll entry: %w", err)
		}
		return json.Unmarshal(deductionsRaw, &entry.Deductions)
	})
	if err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// UpdateEntry changes entryID's mutable fields within firmID. Owner-gated.
// Rejected once the entry's period is no longer 'open'.
func UpdateEntry(ctx context.Context, pool *pgxpool.Pool, firmID, userID, entryID uuid.UUID, input EntryInput) (Entry, error) {
	gross, err := validateAmount("grossAmount", input.GrossAmount)
	if err != nil {
		return Entry{}, err
	}
	net, err := validateAmount("netAmount", input.NetAmount)
	if err != nil {
		return Entry{}, err
	}
	deductions := input.Deductions
	if deductions == nil {
		deductions = map[string]any{}
	}
	deductionsJSON, err := json.Marshal(deductions)
	if err != nil {
		return Entry{}, fmt.Errorf("marshal deductions: %w", err)
	}
	var notes *string
	if n := strings.TrimSpace(input.Notes); n != "" {
		notes = &n
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
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotOwner
		}

		var periodID uuid.UUID
		var periodStatus string
		if err := tx.QueryRow(ctx, `
			SELECT pe.period_id, pp.status FROM payroll_entries pe
			JOIN payroll_periods pp ON pp.id = pe.period_id
			WHERE pe.id = $1 AND pe.firm_id = $2
		`, entryID, firmID).Scan(&periodID, &periodStatus); err != nil {
			if err == pgx.ErrNoRows {
				return ErrEntryNotFound
			}
			return fmt.Errorf("look up payroll entry's period: %w", err)
		}
		if PeriodStatus(periodStatus) != PeriodStatusOpen {
			return ErrPeriodNotOpen
		}

		var deductionsRaw []byte
		if err := tx.QueryRow(ctx, `
			UPDATE payroll_entries SET gross_amount = $1::numeric, deductions = $2, net_amount = $3::numeric, notes = $4
			WHERE id = $5 AND firm_id = $6
			RETURNING id, firm_id, period_id, person_id, contract_id, gross_amount::text, deductions, net_amount::text, currency, notes, created_at
		`, gross, deductionsJSON, net, notes, entryID, firmID).Scan(
			&entry.ID, &entry.FirmID, &entry.PeriodID, &entry.PersonID, &entry.ContractID,
			&entry.GrossAmount, &deductionsRaw, &entry.NetAmount, &entry.Currency, &entry.Notes, &entry.CreatedAt,
		); err != nil {
			return fmt.Errorf("update payroll entry: %w", err)
		}
		if err := json.Unmarshal(deductionsRaw, &entry.Deductions); err != nil {
			return fmt.Errorf("unmarshal deductions: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, entry.ID, entryAuditEntityType, updateEntryAuditAction, map[string]any{
			"grossAmount": gross, "netAmount": net,
		})
	})
	if err != nil {
		return Entry{}, err
	}
	return entry, nil
}
