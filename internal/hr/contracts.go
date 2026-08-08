// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package hr

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// amountPattern mirrors internal/accounting's own amountPattern - a plain
// positive decimal string with up to 15 integer digits and up to 4
// fraction digits, matching contracts.amount's numeric(19,4) column.
// Redeclared here rather than imported/exported cross-package since
// internal/hr has no other dependency on internal/accounting and this is
// a two-line regex, not worth a cross-package coupling for.
var amountPattern = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,4})?$`)

const contractAuditEntityType = "contract"
const createContractAuditAction = "create"

// defaultContractCurrency mirrors internal/accounting's own defaultCurrency
// convention - Vision's scope boundary for this batch: no multi-currency
// conversion, just a stored field.
const defaultContractCurrency = "TRY"

// Contract is one contracts row.
type Contract struct {
	ID        uuid.UUID
	FirmID    uuid.UUID
	PersonID  uuid.UUID
	Type      string
	Amount    *string
	Currency  string
	StartDate time.Time
	EndDate   *time.Time
	Notes     *string
	CreatedAt time.Time
}

// CreateContractInput is CreateContract's request shape. Amount is a
// decimal string (not a float), matching internal/accounting's own
// "financial amounts never pass through float representation" discipline -
// though unlike a journal line, a contract amount has no balance
// invariant to check, so it's validated only for being a well-formed
// decimal, not summed against anything.
type CreateContractInput struct {
	Type      string
	Amount    string // decimal string, optional (empty means NULL)
	Currency  string
	StartDate string // "YYYY-MM-DD", required
	EndDate   string // "YYYY-MM-DD", optional
	Notes     string
}

// CreateContract adds a new contract for personID within firmID.
// Owner-gated, same tier as CreatePerson/CreateAccount.
func CreateContract(ctx context.Context, pool *pgxpool.Pool, firmID, userID, personID uuid.UUID, input CreateContractInput) (Contract, error) {
	contractType := strings.TrimSpace(input.Type)
	if contractType == "" {
		return Contract{}, fmt.Errorf("%w: type must not be empty", ErrInvalidContract)
	}
	if input.StartDate == "" {
		return Contract{}, fmt.Errorf("%w: start date must not be empty", ErrInvalidContract)
	}
	startDate, err := parseOptionalDate(input.StartDate)
	if err != nil || startDate == nil {
		return Contract{}, fmt.Errorf("%w: invalid start date", ErrInvalidContract)
	}
	endDate, err := parseOptionalDate(input.EndDate)
	if err != nil {
		return Contract{}, fmt.Errorf("%w: invalid end date", ErrInvalidContract)
	}
	var amount *string
	if strings.TrimSpace(input.Amount) != "" {
		if !amountPattern.MatchString(input.Amount) {
			return Contract{}, fmt.Errorf("%w: amount %q must be a positive decimal with at most 4 fraction digits", ErrInvalidContract, input.Amount)
		}
		amount = &input.Amount
	}
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = defaultContractCurrency
	}

	var contract Contract
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

		var personExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM people WHERE id = $1 AND firm_id = $2)`, personID, firmID).Scan(&personExists); err != nil {
			return fmt.Errorf("check person exists: %w", err)
		}
		if !personExists {
			return ErrPersonNotFound
		}

		contract = Contract{
			FirmID: firmID, PersonID: personID, Type: contractType,
			Currency: currency, StartDate: *startDate, EndDate: endDate, Notes: optionalString(input.Notes),
		}
		// RETURNING amount::text (not just id/created_at) so the returned
		// Contract reflects the DB's own normalized numeric(19,4) form
		// (e.g. "15000.0000") rather than echoing back whatever raw
		// string the caller submitted (e.g. "15000.00") - otherwise a
		// caller comparing CreateContract's response against a later
		// ListContracts read (which always scans amount::text fresh from
		// the DB) would see the same contract's amount formatted two
		// different ways depending on which call returned it.
		if err := tx.QueryRow(ctx, `
			INSERT INTO contracts (firm_id, person_id, type, amount, currency, start_date, end_date, notes)
			VALUES ($1, $2, $3, $4::numeric, $5, $6, $7, $8)
			RETURNING id, amount::text, created_at
		`, firmID, personID, contractType, amount, currency, startDate, endDate, contract.Notes).Scan(&contract.ID, &contract.Amount, &contract.CreatedAt); err != nil {
			return fmt.Errorf("insert contract: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, contract.ID, contractAuditEntityType, createContractAuditAction, map[string]any{
			"personId": personID.String(), "type": contractType,
		})
	})
	if err != nil {
		return Contract{}, err
	}
	return contract, nil
}

// ListContracts returns every contract for personID within firmID, most
// recent start date first. Member-gated, same tier as ListPeople.
func ListContracts(ctx context.Context, pool *pgxpool.Pool, firmID, userID, personID uuid.UUID) ([]Contract, error) {
	var contracts []Contract
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, person_id, type, amount::text, currency, start_date, end_date, notes, created_at
			FROM contracts
			WHERE person_id = $1
			ORDER BY start_date DESC
		`, personID)
		if err != nil {
			return fmt.Errorf("list contracts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var c Contract
			var amount *string
			if err := rows.Scan(&c.ID, &c.FirmID, &c.PersonID, &c.Type, &amount, &c.Currency, &c.StartDate, &c.EndDate, &c.Notes, &c.CreatedAt); err != nil {
				return err
			}
			c.Amount = amount
			contracts = append(contracts, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return contracts, nil
}
