// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package localization is the address and tax framework FOUNDATION only
// (Open Points item 24 - localization/fiscal compliance - stays fully
// open beyond this): real, firm-scoped address (addresses) and tax rate
// (tax_rates) catalog records, CRUD, owner-gated writes/member-gated
// reads, the same tier as internal/inventory's product/supplier
// mutations. No VAT/GST calculation logic, no fiscal reporting - a
// tax_rates row is just a named, stored percentage a firm can later
// reference (see internal/invoicing's tax_rate_id override), not a rules
// engine that computes which rate applies to what.
package localization

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// Address is one addresses row.
type Address struct {
	ID            uuid.UUID
	FirmID        uuid.UUID
	Label         *string
	Line1         string
	Line2         *string
	City          string
	StateProvince *string
	PostalCode    *string
	CountryCode   string
	IsDefault     bool
	CreatedAt     time.Time
}

func optionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// AddressInput is CreateAddress/UpdateAddress's shared field shape.
type AddressInput struct {
	Label         string
	Line1         string
	Line2         string
	City          string
	StateProvince string
	PostalCode    string
	CountryCode   string
	IsDefault     bool
}

func validateAddressInput(input AddressInput) error {
	if strings.TrimSpace(input.Line1) == "" {
		return fmt.Errorf("%w: line1 must not be empty", ErrInvalidAddress)
	}
	if strings.TrimSpace(input.City) == "" {
		return fmt.Errorf("%w: city must not be empty", ErrInvalidAddress)
	}
	if len(strings.TrimSpace(input.CountryCode)) != 2 {
		return fmt.Errorf("%w: countryCode must be a 2-letter ISO 3166-1 alpha-2 code", ErrInvalidAddress)
	}
	return nil
}

const addressColumns = "id, firm_id, label, line1, line2, city, state_province, postal_code, country_code, is_default, created_at"

func scanAddress(row pgx.Row) (Address, error) {
	var a Address
	if err := row.Scan(&a.ID, &a.FirmID, &a.Label, &a.Line1, &a.Line2, &a.City, &a.StateProvince, &a.PostalCode, &a.CountryCode, &a.IsDefault, &a.CreatedAt); err != nil {
		return Address{}, err
	}
	return a, nil
}

// CreateAddress adds a new address to firmID - owner-gated, same tier as
// internal/logistics.CreateDelivery.
func CreateAddress(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input AddressInput) (Address, error) {
	if err := validateAddressInput(input); err != nil {
		return Address{}, err
	}

	var addr Address
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
			INSERT INTO addresses (firm_id, label, line1, line2, city, state_province, postal_code, country_code, is_default)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING `+addressColumns, firmID, optionalString(input.Label), strings.TrimSpace(input.Line1), optionalString(input.Line2),
			strings.TrimSpace(input.City), optionalString(input.StateProvince), optionalString(input.PostalCode),
			strings.ToUpper(strings.TrimSpace(input.CountryCode)), input.IsDefault)
		a, err := scanAddress(row)
		if err != nil {
			return fmt.Errorf("insert address: %w", err)
		}
		addr = a
		return nil
	})
	if err != nil {
		return Address{}, err
	}
	return addr, nil
}

// ListAddresses returns every address for firmID - member-gated read.
func ListAddresses(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Address, error) {
	var addresses []Address
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+addressColumns+` FROM addresses WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list addresses: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAddress(rows)
			if err != nil {
				return fmt.Errorf("scan address: %w", err)
			}
			addresses = append(addresses, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return addresses, nil
}

// UpdateAddress replaces addressID's mutable fields - owner-gated, same
// tier as CreateAddress. Full-replace (not a partial patch), matching
// this package's deliberately minimal CRUD scope.
func UpdateAddress(ctx context.Context, pool *pgxpool.Pool, firmID, userID, addressID uuid.UUID, input AddressInput) (Address, error) {
	if err := validateAddressInput(input); err != nil {
		return Address{}, err
	}

	var addr Address
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
			UPDATE addresses SET label = $1, line1 = $2, line2 = $3, city = $4, state_province = $5, postal_code = $6, country_code = $7, is_default = $8
			WHERE id = $9 AND firm_id = $10
			RETURNING `+addressColumns, optionalString(input.Label), strings.TrimSpace(input.Line1), optionalString(input.Line2),
			strings.TrimSpace(input.City), optionalString(input.StateProvince), optionalString(input.PostalCode),
			strings.ToUpper(strings.TrimSpace(input.CountryCode)), input.IsDefault, addressID, firmID)
		a, err := scanAddress(row)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrAddressNotFound
			}
			return fmt.Errorf("update address: %w", err)
		}
		addr = a
		return nil
	})
	if err != nil {
		return Address{}, err
	}
	return addr, nil
}

// DeleteAddress removes addressID - owner-gated, same tier as
// CreateAddress.
func DeleteAddress(ctx context.Context, pool *pgxpool.Pool, firmID, userID, addressID uuid.UUID) error {
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

		tag, err := tx.Exec(ctx, `DELETE FROM addresses WHERE id = $1 AND firm_id = $2`, addressID, firmID)
		if err != nil {
			return fmt.Errorf("delete address: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrAddressNotFound
		}
		return nil
	})
}
