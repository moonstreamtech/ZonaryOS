// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Part 3 of the multi-language UI/localization depth/fiscal compliance
// batch: a narrow slice of Open Points item 24 - VAT/tax-number FORMAT
// validation per country, not tax calculation, not e-invoicing compliance
// (item 24 stays open beyond this). fiscal_country_configs
// (migrations/0036) is platform-wide reference data, the same "global,
// not tenant-scoped" shape internal/currency's own exchange_rates table
// already establishes - no firm_id, no RLS, platform-admin-gated writes
// (handlers.go), unauthenticated reads (the format/label/currency a
// country uses carries no sensitive data, and a firm's own frontend needs
// to read it before a bearer token exists for a brand-new session the
// same way internal/currency's GET /api/exchange-rates already argues).
package localization

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FiscalCountryConfig is one fiscal_country_configs row.
type FiscalCountryConfig struct {
	CountryCode          string
	VATNumberPattern     *string
	VATNumberLabel       string
	CurrencyCode         string
	DateFormat           string
	FiscalYearStartMonth int
}

// FiscalCountryConfigInput is CreateFiscalCountryConfig/
// UpdateFiscalCountryConfig's shared field shape.
type FiscalCountryConfigInput struct {
	CountryCode          string
	VATNumberPattern     string
	VATNumberLabel       string
	CurrencyCode         string
	DateFormat           string
	FiscalYearStartMonth int
}

func normalizeCountryCode(c string) string {
	return strings.ToUpper(strings.TrimSpace(c))
}

func validateFiscalCountryConfigInput(input FiscalCountryConfigInput) error {
	if normalizeCountryCode(input.CountryCode) == "" {
		return fmt.Errorf("%w: countryCode must not be empty", ErrInvalidFiscalCountryConfig)
	}
	if strings.TrimSpace(input.VATNumberLabel) == "" {
		return fmt.Errorf("%w: vatNumberLabel must not be empty", ErrInvalidFiscalCountryConfig)
	}
	if strings.TrimSpace(input.CurrencyCode) == "" {
		return fmt.Errorf("%w: currencyCode must not be empty", ErrInvalidFiscalCountryConfig)
	}
	if strings.TrimSpace(input.DateFormat) == "" {
		return fmt.Errorf("%w: dateFormat must not be empty", ErrInvalidFiscalCountryConfig)
	}
	if input.FiscalYearStartMonth < 1 || input.FiscalYearStartMonth > 12 {
		return fmt.Errorf("%w: fiscalYearStartMonth must be between 1 and 12", ErrInvalidFiscalCountryConfig)
	}
	if strings.TrimSpace(input.VATNumberPattern) != "" {
		if _, err := regexp.Compile(input.VATNumberPattern); err != nil {
			return fmt.Errorf("%w: vatNumberPattern is not a valid regular expression: %v", ErrInvalidFiscalCountryConfig, err)
		}
	}
	return nil
}

const fiscalCountryConfigColumns = "country_code, vat_number_pattern, vat_number_label, currency_code, date_format, fiscal_year_start_month"

func scanFiscalCountryConfig(row pgx.Row) (FiscalCountryConfig, error) {
	var c FiscalCountryConfig
	if err := row.Scan(&c.CountryCode, &c.VATNumberPattern, &c.VATNumberLabel, &c.CurrencyCode, &c.DateFormat, &c.FiscalYearStartMonth); err != nil {
		return FiscalCountryConfig{}, err
	}
	return c, nil
}

// ListFiscalCountryConfigs returns every seeded fiscal_country_configs
// row, ordered by country_code - unauthenticated, same posture as
// internal/currency.ListRates (reference data, no sensitive content).
func ListFiscalCountryConfigs(ctx context.Context, pool *pgxpool.Pool) ([]FiscalCountryConfig, error) {
	rows, err := pool.Query(ctx, `SELECT `+fiscalCountryConfigColumns+` FROM fiscal_country_configs ORDER BY country_code`)
	if err != nil {
		return nil, fmt.Errorf("list fiscal country configs: %w", err)
	}
	defer rows.Close()
	var configs []FiscalCountryConfig
	for rows.Next() {
		c, err := scanFiscalCountryConfig(rows)
		if err != nil {
			return nil, fmt.Errorf("scan fiscal country config: %w", err)
		}
		configs = append(configs, c)
	}
	return configs, rows.Err()
}

// GetFiscalCountryConfig looks up one country's config by its ISO 3166-1
// alpha-2 code. Returns ErrFiscalCountryConfigNotFound if the country
// hasn't been seeded - callers that use this for a soft, best-effort hint
// (internal/firm's tax_id-pattern warning, this batch's fiscal-year-start
// default) treat that as "no hint available," not a hard error.
func GetFiscalCountryConfig(ctx context.Context, pool *pgxpool.Pool, countryCode string) (FiscalCountryConfig, error) {
	row := pool.QueryRow(ctx, `SELECT `+fiscalCountryConfigColumns+` FROM fiscal_country_configs WHERE country_code = $1`, normalizeCountryCode(countryCode))
	c, err := scanFiscalCountryConfig(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return FiscalCountryConfig{}, ErrFiscalCountryConfigNotFound
		}
		return FiscalCountryConfig{}, fmt.Errorf("get fiscal country config: %w", err)
	}
	return c, nil
}

// UpsertFiscalCountryConfig creates or replaces one country's config -
// platform-admin-gated at the HTTP layer (handlers.go), not here, the
// same division of responsibility internal/currency.CreateRate's own doc
// comment describes for its own platform-admin gate. country_code is the
// primary key, so this is a plain upsert (ON CONFLICT DO UPDATE) rather
// than separate create/update functions - platform admins are expected to
// edit an existing country's config in place, not version it.
func UpsertFiscalCountryConfig(ctx context.Context, pool *pgxpool.Pool, input FiscalCountryConfigInput) (FiscalCountryConfig, error) {
	if err := validateFiscalCountryConfigInput(input); err != nil {
		return FiscalCountryConfig{}, err
	}

	code := normalizeCountryCode(input.CountryCode)
	row := pool.QueryRow(ctx, `
		INSERT INTO fiscal_country_configs (country_code, vat_number_pattern, vat_number_label, currency_code, date_format, fiscal_year_start_month)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6)
		ON CONFLICT (country_code) DO UPDATE SET
			vat_number_pattern = EXCLUDED.vat_number_pattern,
			vat_number_label = EXCLUDED.vat_number_label,
			currency_code = EXCLUDED.currency_code,
			date_format = EXCLUDED.date_format,
			fiscal_year_start_month = EXCLUDED.fiscal_year_start_month
		RETURNING `+fiscalCountryConfigColumns,
		code, strings.TrimSpace(input.VATNumberPattern), strings.TrimSpace(input.VATNumberLabel),
		strings.ToUpper(strings.TrimSpace(input.CurrencyCode)), strings.TrimSpace(input.DateFormat), input.FiscalYearStartMonth,
	)
	c, err := scanFiscalCountryConfig(row)
	if err != nil {
		return FiscalCountryConfig{}, fmt.Errorf("upsert fiscal country config: %w", err)
	}
	return c, nil
}

// DeleteFiscalCountryConfig removes one country's config - platform-admin-
// gated at the HTTP layer, same tier as UpsertFiscalCountryConfig.
func DeleteFiscalCountryConfig(ctx context.Context, pool *pgxpool.Pool, countryCode string) error {
	tag, err := pool.Exec(ctx, `DELETE FROM fiscal_country_configs WHERE country_code = $1`, normalizeCountryCode(countryCode))
	if err != nil {
		return fmt.Errorf("delete fiscal country config: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrFiscalCountryConfigNotFound
	}
	return nil
}

// ValidateVATNumber reports whether value matches pattern (a Go regexp,
// as stored in FiscalCountryConfig.VATNumberPattern). A malformed pattern
// (should be unreachable in practice - UpsertFiscalCountryConfig already
// rejects one - but defensive here since the column has no DB-level
// regex-validity constraint) is treated as "can't validate," i.e. true,
// never as a false rejection of a value that might be perfectly valid -
// matching Part 3's own "soft validation, don't block" contract even in
// this pattern-is-bad edge case.
func ValidateVATNumber(pattern, value string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return true
	}
	return re.MatchString(strings.TrimSpace(value))
}
