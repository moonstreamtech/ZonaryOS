// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package localization_test

import (
	"context"
	"testing"

	"github.com/moonstreamtech/ZonaryOS/internal/localization"
)

// TestValidateVATNumber_TurkishFormat is Part 3's own explicit test
// requirement: TR validation accepts a valid Turkish tax ID format,
// rejects a clearly invalid one. Pure function, no Postgres needed - the
// same regex migrations/0036 seeds into fiscal_country_configs.TR.
func TestValidateVATNumber_TurkishFormat(t *testing.T) {
	const trPattern = `^[0-9]{10}$`

	if !localization.ValidateVATNumber(trPattern, "1234567890") {
		t.Error("expected a 10-digit Turkish tax ID to be accepted")
	}
	if localization.ValidateVATNumber(trPattern, "not-a-tax-id") {
		t.Error("expected a clearly invalid tax ID to be rejected")
	}
	if localization.ValidateVATNumber(trPattern, "12345") {
		t.Error("expected a too-short tax ID to be rejected")
	}
}

func TestValidateVATNumber_MalformedPatternNeverRejects(t *testing.T) {
	// An uncompilable pattern must never turn into a false "invalid"
	// warning - see ValidateVATNumber's own doc comment.
	if !localization.ValidateVATNumber("([invalid", "anything") {
		t.Error("expected a malformed pattern to be treated as \"can't validate\" (true), never a false rejection")
	}
}

// TestFiscalCountryConfigs_SeededRows exercises the real seeded rows
// (migrations/0036) against Postgres - the same "exercise against a real
// database, RLS/reference-data can't be meaningfully faked" convention
// this package's other integration tests already follow. This table
// carries no firm data and isn't in setupTest's own TRUNCATE list, so the
// migration's seed rows are exactly what's queried here.
func TestFiscalCountryConfigs_SeededRows(t *testing.T) {
	_, appPool := setupTest(t)
	ctx := context.Background()

	tr, err := localization.GetFiscalCountryConfig(ctx, appPool, "TR")
	if err != nil {
		t.Fatalf("GetFiscalCountryConfig(TR): %v", err)
	}
	if tr.VATNumberLabel == "" || tr.CurrencyCode != "TRY" || tr.FiscalYearStartMonth != 1 {
		t.Fatalf("unexpected TR config: %+v", tr)
	}
	if tr.VATNumberPattern == nil || !localization.ValidateVATNumber(*tr.VATNumberPattern, "1234567890") {
		t.Fatalf("expected TR's own seeded pattern to accept a 10-digit tax ID, got %+v", tr)
	}

	gb, err := localization.GetFiscalCountryConfig(ctx, appPool, "gb")
	if err != nil {
		t.Fatalf("GetFiscalCountryConfig(gb) (lowercase, should normalize): %v", err)
	}
	if gb.FiscalYearStartMonth != 4 {
		t.Fatalf("expected GB's fiscal year to start in April (month 4), got %d", gb.FiscalYearStartMonth)
	}

	if _, err := localization.GetFiscalCountryConfig(ctx, appPool, "ZZ"); err != localization.ErrFiscalCountryConfigNotFound {
		t.Fatalf("expected ErrFiscalCountryConfigNotFound for an unseeded country, got %v", err)
	}

	all, err := localization.ListFiscalCountryConfigs(ctx, appPool)
	if err != nil {
		t.Fatalf("ListFiscalCountryConfigs: %v", err)
	}
	if len(all) < 5 {
		t.Fatalf("expected at least the 5 seeded countries (TR/DE/GB/US/FR), got %d", len(all))
	}
}
