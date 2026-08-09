// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package currency is the multi-currency foundation (not full
// implementation - see this package's own scope-boundary notes below):
// a platform-wide (not firm-scoped - migrations/0020's own comment)
// exchange_rates table, GetRate/ConvertAmount lookups, and the
// internal/accounting journal-posting wire-in
// (internal/accounting.PostJournalEntryTx) that auto-converts a line's
// amount into its target account's own currency when they differ,
// falling back to a 1:1 rate (never failing the transition) when no rate
// is on file - see ConvertAmountTx's own doc comment for the exact
// safety guarantee.
//
// Scope boundaries: no real-time exchange rate API integration (manual
// entry only, via CreateRate). No currency-aware reporting/rounding
// rules beyond a plain numeric(19,4) amount. No automatic rate refresh.
package currency

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ratePattern matches exchange_rates.rate's numeric(19,8) column - same
// "bounded-precision decimal string, never a Go float" convention every
// other money-adjacent field in this codebase uses (see
// internal/accounting/journal.go's amountPattern).
var ratePattern = regexp.MustCompile(`^[0-9]{1,11}(\.[0-9]{1,8})?$`)

// Rate is one exchange_rates row.
type Rate struct {
	ID           uuid.UUID
	FromCurrency string
	ToCurrency   string
	Rate         string
	ValidFrom    time.Time
	ValidTo      *time.Time
	Source       string
	CreatedAt    time.Time
}

// CreateRateInput is CreateRate's request shape.
type CreateRateInput struct {
	FromCurrency string
	ToCurrency   string
	Rate         string
	ValidFrom    time.Time
	ValidTo      *time.Time
	Source       string
}

func normalizeCurrencyCode(c string) string {
	return strings.ToUpper(strings.TrimSpace(c))
}

// CreateRate stores a new exchange_rates row - platform-admin-gated at
// the HTTP layer (handlers.go's handleCreateRate, via
// internal/platformadmin.Allowlist), not here: exchange rates are
// platform-wide, not firm-scoped, so there is no firmID to check
// membership/ownership against the way every other CRUD write in this
// codebase does.
func CreateRate(ctx context.Context, pool *pgxpool.Pool, input CreateRateInput) (Rate, error) {
	from := normalizeCurrencyCode(input.FromCurrency)
	to := normalizeCurrencyCode(input.ToCurrency)
	if from == "" || to == "" {
		return Rate{}, fmt.Errorf("%w: fromCurrency and toCurrency must not be empty", ErrInvalidRate)
	}
	if !ratePattern.MatchString(input.Rate) {
		return Rate{}, fmt.Errorf("%w: rate %q must be a positive decimal with at most 8 fraction digits", ErrInvalidRate, input.Rate)
	}
	if input.ValidFrom.IsZero() {
		return Rate{}, fmt.Errorf("%w: validFrom must be set", ErrInvalidRate)
	}
	if input.ValidTo != nil && !input.ValidTo.After(input.ValidFrom) {
		return Rate{}, fmt.Errorf("%w: validTo must be after validFrom", ErrInvalidRate)
	}

	r := Rate{FromCurrency: from, ToCurrency: to, Rate: input.Rate, ValidFrom: input.ValidFrom, ValidTo: input.ValidTo, Source: strings.TrimSpace(input.Source)}
	if err := pool.QueryRow(ctx, `
		INSERT INTO exchange_rates (from_currency, to_currency, rate, valid_from, valid_to, source)
		VALUES ($1, $2, $3::numeric, $4, $5, NULLIF($6, ''))
		RETURNING id, created_at
	`, from, to, input.Rate, input.ValidFrom, input.ValidTo, r.Source).Scan(&r.ID, &r.CreatedAt); err != nil {
		return Rate{}, fmt.Errorf("insert exchange rate: %w", err)
	}
	return r, nil
}

// ListRatesOptions filters ListRates - every zero-valued field is
// unfiltered (From/To empty means "any currency", At nil means "don't
// filter by validity, return every row").
type ListRatesOptions struct {
	From string
	To   string
	At   *time.Time
}

// ListRates returns exchange_rates rows matching opts, most recent
// valid_from first - the read side of GET /api/exchange-rates,
// deliberately unauthenticated (see handlers.go): exchange rates carry
// no sensitive data.
func ListRates(ctx context.Context, pool *pgxpool.Pool, opts ListRatesOptions) ([]Rate, error) {
	query := `SELECT id, from_currency, to_currency, rate::text, valid_from, valid_to, COALESCE(source, ''), created_at FROM exchange_rates WHERE true`
	args := []any{}
	if opts.From != "" {
		args = append(args, normalizeCurrencyCode(opts.From))
		query += fmt.Sprintf(" AND from_currency = $%d", len(args))
	}
	if opts.To != "" {
		args = append(args, normalizeCurrencyCode(opts.To))
		query += fmt.Sprintf(" AND to_currency = $%d", len(args))
	}
	if opts.At != nil {
		args = append(args, *opts.At)
		query += fmt.Sprintf(" AND valid_from <= $%d AND (valid_to IS NULL OR valid_to > $%d)", len(args), len(args))
	}
	query += ` ORDER BY valid_from DESC`

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list exchange rates: %w", err)
	}
	defer rows.Close()

	var rates []Rate
	for rows.Next() {
		var r Rate
		if err := rows.Scan(&r.ID, &r.FromCurrency, &r.ToCurrency, &r.Rate, &r.ValidFrom, &r.ValidTo, &r.Source, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan exchange rate: %w", err)
		}
		rates = append(rates, r)
	}
	return rates, rows.Err()
}

// GetRateTx returns the most recent from->to rate valid at at, within an
// already-open transaction - the primitive internal/accounting's journal
// bridge (PostJournalEntryTx) uses, so the rate lookup is part of the
// same atomic operation as the journal entry it's converting an amount
// for, rather than a separate round trip outside that transaction.
// Identity pairs (from == to) short-circuit to "1" without ever querying
// the database - the overwhelmingly common case (an instance's currency
// already matches its target account's currency), and one that must
// never depend on a same-pair row actually existing in exchange_rates.
func GetRateTx(ctx context.Context, tx pgx.Tx, from, to string, at time.Time) (string, error) {
	from, to = normalizeCurrencyCode(from), normalizeCurrencyCode(to)
	if from == to {
		return "1", nil
	}
	var rate string
	err := tx.QueryRow(ctx, `
		SELECT rate::text FROM exchange_rates
		WHERE from_currency = $1 AND to_currency = $2 AND valid_from <= $3 AND (valid_to IS NULL OR valid_to > $3)
		ORDER BY valid_from DESC
		LIMIT 1
	`, from, to, at).Scan(&rate)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrRateNotFound
	}
	if err != nil {
		return "", fmt.Errorf("look up exchange rate %s->%s: %w", from, to, err)
	}
	return rate, nil
}

// GetRate is GetRateTx's pool-based counterpart, for callers with no
// already-open transaction (the GET /api/exchange-rates HTTP handler,
// this package's own tests). exchange_rates carries no RLS (see
// migrations/0020's own comment - it's platform-wide, not tenant-scoped),
// so this is a plain pool query, no db.WithFirmContext involved.
func GetRate(ctx context.Context, pool *pgxpool.Pool, from, to string, at time.Time) (string, error) {
	from, to = normalizeCurrencyCode(from), normalizeCurrencyCode(to)
	if from == to {
		return "1", nil
	}
	var rate string
	err := pool.QueryRow(ctx, `
		SELECT rate::text FROM exchange_rates
		WHERE from_currency = $1 AND to_currency = $2 AND valid_from <= $3 AND (valid_to IS NULL OR valid_to > $3)
		ORDER BY valid_from DESC
		LIMIT 1
	`, from, to, at).Scan(&rate)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrRateNotFound
	}
	if err != nil {
		return "", fmt.Errorf("look up exchange rate %s->%s: %w", from, to, err)
	}
	return rate, nil
}

// multiplyDecimal computes a*b as an exact decimal string via Postgres's
// own `numeric` arithmetic - never Go floating point, the same
// "financial amounts never pass through float" discipline
// internal/accounting/journal.go's own doc comments establish throughout.
func multiplyDecimal(ctx context.Context, tx pgx.Tx, a, b string) (string, error) {
	var result string
	if err := tx.QueryRow(ctx, `SELECT ($1::numeric * $2::numeric)::text`, a, b).Scan(&result); err != nil {
		return "", fmt.Errorf("multiply decimal: %w", err)
	}
	return result, nil
}

// ConvertAmountTx converts amount from currency `from` to `to`, at the
// rate valid at `at`, within an already-open transaction - GetRateTx
// plus an exact decimal multiply, never a Go float. Returns
// ErrRateNotFound verbatim when no rate covers the pair (from == to
// still short-circuits to amount unchanged via GetRateTx's own identity
// case) - the caller decides the fallback (see
// internal/accounting.PostJournalEntryTx's own 1:1-fallback wiring),
// this function itself never silently substitutes one.
func ConvertAmountTx(ctx context.Context, tx pgx.Tx, amount, from, to string, at time.Time) (string, error) {
	rate, err := GetRateTx(ctx, tx, from, to, at)
	if err != nil {
		return "", err
	}
	if rate == "1" {
		return amount, nil
	}
	return multiplyDecimal(ctx, tx, amount, rate)
}

// ConvertAmount is ConvertAmountTx's pool-based counterpart, for callers
// with no already-open transaction (this package's own tests, and any
// future non-transactional caller).
func ConvertAmount(ctx context.Context, pool *pgxpool.Pool, amount, from, to string, at time.Time) (string, error) {
	rate, err := GetRate(ctx, pool, from, to, at)
	if err != nil {
		return "", err
	}
	if rate == "1" {
		return amount, nil
	}
	var result string
	if err := pool.QueryRow(ctx, `SELECT ($1::numeric * $2::numeric)::text`, amount, rate).Scan(&result); err != nil {
		return "", fmt.Errorf("multiply decimal: %w", err)
	}
	return result, nil
}
