// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package localization

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// taxRatePattern matches tax_rates.rate's numeric(5,4) column - a
// fraction (e.g. "0.2000" for 20%), NOT a percentage - see
// internal/invoicing's own doc comment on invoice_lines.tax_rate's
// different, percentage-shaped numeric(5,2) column for the conversion
// this package's own consumers must apply.
var taxRatePattern = regexp.MustCompile(`^[0-9](\.[0-9]{1,4})?$`)

// TaxRate is one tax_rates row.
type TaxRate struct {
	ID          uuid.UUID
	FirmID      uuid.UUID
	Name        string
	Rate        string
	CountryCode *string
	Region      *string
	AppliesTo   *string
	IsDefault   bool
	CreatedAt   time.Time
}

// TaxRateInput is CreateTaxRate/UpdateTaxRate's shared field shape.
type TaxRateInput struct {
	Name        string
	Rate        string
	CountryCode string
	Region      string
	AppliesTo   string
	IsDefault   bool
}

func validateTaxRateInput(input TaxRateInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("%w: name must not be empty", ErrInvalidTaxRate)
	}
	if !taxRatePattern.MatchString(input.Rate) {
		return fmt.Errorf("%w: rate %q must be a decimal fraction between 0 and 9.9999", ErrInvalidTaxRate, input.Rate)
	}
	return nil
}

const taxRateColumns = "id, firm_id, name, rate::text, country_code, region, applies_to, is_default, created_at"

func scanTaxRate(row pgx.Row) (TaxRate, error) {
	var t TaxRate
	if err := row.Scan(&t.ID, &t.FirmID, &t.Name, &t.Rate, &t.CountryCode, &t.Region, &t.AppliesTo, &t.IsDefault, &t.CreatedAt); err != nil {
		return TaxRate{}, err
	}
	return t, nil
}

// CreateTaxRate adds a new tax rate to firmID - owner-gated, same tier as
// CreateAddress.
func CreateTaxRate(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input TaxRateInput) (TaxRate, error) {
	if err := validateTaxRateInput(input); err != nil {
		return TaxRate{}, err
	}

	var rate TaxRate
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
			INSERT INTO tax_rates (firm_id, name, rate, country_code, region, applies_to, is_default)
			VALUES ($1, $2, $3::numeric, $4, $5, $6, $7)
			RETURNING `+taxRateColumns, firmID, strings.TrimSpace(input.Name), input.Rate,
			optionalString(input.CountryCode), optionalString(input.Region), optionalString(input.AppliesTo), input.IsDefault)
		t, err := scanTaxRate(row)
		if err != nil {
			return fmt.Errorf("insert tax rate: %w", err)
		}
		rate = t
		return nil
	})
	if err != nil {
		return TaxRate{}, err
	}
	return rate, nil
}

// ListTaxRates returns every tax rate for firmID - member-gated read.
func ListTaxRates(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]TaxRate, error) {
	var rates []TaxRate
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+taxRateColumns+` FROM tax_rates WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list tax rates: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTaxRate(rows)
			if err != nil {
				return fmt.Errorf("scan tax rate: %w", err)
			}
			rates = append(rates, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return rates, nil
}

// UpdateTaxRate replaces taxRateID's mutable fields - owner-gated,
// full-replace, same shape as UpdateAddress.
func UpdateTaxRate(ctx context.Context, pool *pgxpool.Pool, firmID, userID, taxRateID uuid.UUID, input TaxRateInput) (TaxRate, error) {
	if err := validateTaxRateInput(input); err != nil {
		return TaxRate{}, err
	}

	var rate TaxRate
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
			UPDATE tax_rates SET name = $1, rate = $2::numeric, country_code = $3, region = $4, applies_to = $5, is_default = $6
			WHERE id = $7 AND firm_id = $8
			RETURNING `+taxRateColumns, strings.TrimSpace(input.Name), input.Rate,
			optionalString(input.CountryCode), optionalString(input.Region), optionalString(input.AppliesTo), input.IsDefault, taxRateID, firmID)
		t, err := scanTaxRate(row)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrTaxRateNotFound
			}
			return fmt.Errorf("update tax rate: %w", err)
		}
		rate = t
		return nil
	})
	if err != nil {
		return TaxRate{}, err
	}
	return rate, nil
}

// DeleteTaxRate removes taxRateID - owner-gated, same tier as
// CreateTaxRate. Any invoice_lines row referencing it has its
// tax_rate_id set NULL (ON DELETE SET NULL, migrations/0020) - its own
// already-resolved tax_rate value is untouched.
func DeleteTaxRate(ctx context.Context, pool *pgxpool.Pool, firmID, userID, taxRateID uuid.UUID) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
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

		tag, err := tx.Exec(ctx, `DELETE FROM tax_rates WHERE id = $1 AND firm_id = $2`, taxRateID, firmID)
		if err != nil {
			return fmt.Errorf("delete tax rate: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrTaxRateNotFound
		}
		return nil
	})
}

// GetTaxRateTx reads one tax_rates row scoped to (taxRateID, firmID) -
// internal/invoicing's own tax_rate_id override resolution uses this
// directly, within its own already-open, already-authorized transaction.
//
// ciaudit:ignore-firmid-check: called only by internal/invoicing's own
// owner-gated line-mutation functions, which have already run
// permission.IsMember/IsOwner before reaching this call - firmID here
// scopes a read query, not a fresh authorization decision.
func GetTaxRateTx(ctx context.Context, tx pgx.Tx, firmID, taxRateID uuid.UUID) (TaxRate, error) {
	row := tx.QueryRow(ctx, `SELECT `+taxRateColumns+` FROM tax_rates WHERE id = $1 AND firm_id = $2`, taxRateID, firmID)
	t, err := scanTaxRate(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return TaxRate{}, ErrTaxRateNotFound
		}
		return TaxRate{}, fmt.Errorf("look up tax rate: %w", err)
	}
	return t, nil
}
