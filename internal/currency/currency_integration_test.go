// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package currency_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/currency"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise the multi-currency foundation against a real
// Postgres instance, same convention as every other package's
// integration tests in this codebase. Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set. See docs/DEVELOPMENT.md.

func setupTest(t *testing.T) (adminPool, appPool *pgxpool.Pool) {
	t.Helper()
	adminDSN := os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN := os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run currency integration tests")
	}
	ctx := context.Background()

	if err := zdb.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	adminPool, err := zdb.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)
	if _, err := adminPool.Exec(ctx, `TRUNCATE exchange_rates CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err = zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(appPool.Close)

	return adminPool, appPool
}

// TestGetRate_DateFiltering seeds a past (closed) rate and a current
// (open-ended) rate for the same pair, and confirms GetRate returns
// whichever one was actually valid at the requested instant - not just
// "the most recent row ever inserted".
func TestGetRate_DateFiltering(t *testing.T) {
	_, appPool := setupTest(t)
	ctx := context.Background()

	pastFrom := time.Now().Add(-30 * 24 * time.Hour)
	pastTo := time.Now().Add(-15 * 24 * time.Hour)
	if _, err := currency.CreateRate(ctx, appPool, currency.CreateRateInput{
		FromCurrency: "USD", ToCurrency: "TRY", Rate: "28.50000000", ValidFrom: pastFrom, ValidTo: &pastTo, Source: "manual",
	}); err != nil {
		t.Fatalf("seed past rate: %v", err)
	}

	currentFrom := time.Now().Add(-1 * time.Hour)
	if _, err := currency.CreateRate(ctx, appPool, currency.CreateRateInput{
		FromCurrency: "USD", ToCurrency: "TRY", Rate: "32.00000000", ValidFrom: currentFrom, Source: "manual",
	}); err != nil {
		t.Fatalf("seed current rate: %v", err)
	}

	// At a point in time squarely inside the past rate's own window.
	pastRate, err := currency.GetRate(ctx, appPool, "USD", "TRY", pastFrom.Add(5*24*time.Hour))
	if err != nil {
		t.Fatalf("GetRate (past): %v", err)
	}
	if pastRate != "28.50000000" {
		t.Errorf("expected the past rate 28.50000000, got %q", pastRate)
	}

	// At "now" - the past rate has already closed (valid_to in the
	// past), the current rate is open-ended and active.
	nowRate, err := currency.GetRate(ctx, appPool, "USD", "TRY", time.Now())
	if err != nil {
		t.Fatalf("GetRate (now): %v", err)
	}
	if nowRate != "32.00000000" {
		t.Errorf("expected the current rate 32.00000000, got %q", nowRate)
	}
}

func TestGetRate_UnknownPairReturnsErrRateNotFound(t *testing.T) {
	_, appPool := setupTest(t)
	ctx := context.Background()

	_, err := currency.GetRate(ctx, appPool, "EUR", "JPY", time.Now())
	if !errors.Is(err, currency.ErrRateNotFound) {
		t.Fatalf("expected ErrRateNotFound for a pair with no rate on file, got %v", err)
	}
}

func TestGetRate_IdentityPairReturnsOneWithoutAnyRow(t *testing.T) {
	_, appPool := setupTest(t)
	ctx := context.Background()

	rate, err := currency.GetRate(ctx, appPool, "TRY", "TRY", time.Now())
	if err != nil {
		t.Fatalf("GetRate (identity pair): %v", err)
	}
	if rate != "1" {
		t.Fatalf("expected identity pair to short-circuit to rate \"1\", got %q", rate)
	}
}

func TestConvertAmount_Multiplies(t *testing.T) {
	_, appPool := setupTest(t)
	ctx := context.Background()

	validFrom := time.Now().Add(-1 * time.Hour)
	if _, err := currency.CreateRate(ctx, appPool, currency.CreateRateInput{
		FromCurrency: "USD", ToCurrency: "TRY", Rate: "30", ValidFrom: validFrom, Source: "manual",
	}); err != nil {
		t.Fatalf("seed rate: %v", err)
	}

	converted, err := currency.ConvertAmount(ctx, appPool, "10.00", "USD", "TRY", time.Now())
	if err != nil {
		t.Fatalf("ConvertAmount: %v", err)
	}
	if converted != "300.0000000000" {
		t.Fatalf("expected 10.00 USD * 30 = 300.0000000000 TRY, got %q", converted)
	}
}

func TestConvertAmount_NoRateReturnsErrRateNotFound(t *testing.T) {
	_, appPool := setupTest(t)
	ctx := context.Background()

	_, err := currency.ConvertAmount(ctx, appPool, "10.00", "USD", "JPY", time.Now())
	if !errors.Is(err, currency.ErrRateNotFound) {
		t.Fatalf("expected ErrRateNotFound, got %v", err)
	}
}
