// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package accounting_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
)

// These tests exercise PostJournalEntryTx's multi-currency wire-in (this
// batch, internal/currency): a line whose supplied Currency differs from
// its target account's own currency is auto-converted using the rate
// valid at posting time, falling back to 1:1 (never failing the
// transition) when no rate is on file. setupTest/seedOwner/seedAccount
// are shared with accounting_integration_test.go (same package).

func TestPostJournalEntry_MismatchedCurrencyAutoConverts(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm Currency A", "sub-currency-a")
	if _, err := accounting.CreateAccount(ctx, appPool, firmID, userID, "1000", "Cash (USD)", accounting.AccountTypeAsset, nil, "USD"); err != nil {
		t.Fatalf("seed USD account: %v", err)
	}
	if _, err := accounting.CreateAccount(ctx, appPool, firmID, userID, "3000", "Revenue", accounting.AccountTypeRevenue, nil, ""); err != nil {
		t.Fatalf("seed TRY account: %v", err)
	}

	// TRY -> USD at 0.03 (i.e. 1 TRY = 0.03 USD), valid from an hour ago,
	// open-ended.
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO exchange_rates (from_currency, to_currency, rate, valid_from, source)
		VALUES ('TRY', 'USD', 0.03, $1, 'manual')
	`, time.Now().Add(-1*time.Hour)); err != nil {
		t.Fatalf("seed exchange rate: %v", err)
	}

	// The debit line is posted with Currency "TRY" against the USD
	// account - PostJournalEntryTx must convert 100.00 TRY -> 3.00 USD
	// (100 * 0.03) before storing, and tag the stored row with the
	// account's own currency (USD), not the line's original one.
	entryID, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "Cross-currency sale", []accounting.LineInput{
		{AccountCode: "1000", Side: accounting.SideDebit, Amount: "100.00", Currency: "TRY"},
		{AccountCode: "3000", Side: accounting.SideCredit, Amount: "3.00"},
	})
	if err != nil {
		t.Fatalf("PostJournalEntry: %v", err)
	}

	result, err := accounting.ListJournalEntries(ctx, appPool, firmID, userID, accounting.ListOptions{})
	if err != nil {
		t.Fatalf("ListJournalEntries: %v", err)
	}
	var posted accounting.JournalEntry
	for _, e := range result.Entries {
		if e.ID == entryID {
			posted = e
		}
	}
	if posted.ID == uuid.Nil {
		t.Fatalf("expected to find the posted entry %s", entryID)
	}

	var debitLine *accounting.JournalLine
	for i := range posted.Lines {
		if posted.Lines[i].AccountCode == "1000" {
			debitLine = &posted.Lines[i]
		}
	}
	if debitLine == nil {
		t.Fatal("expected a line against account 1000")
	}
	if debitLine.Currency != "USD" {
		t.Errorf("expected the converted line's stored currency to be the account's own (USD), got %q", debitLine.Currency)
	}
	if debitLine.Amount != "3.0000" {
		t.Errorf("expected the converted amount to be 3.0000 (100.00 TRY * 0.03), got %q", debitLine.Amount)
	}
}

func TestPostJournalEntry_MissingRateFallsBackTo1To1WithoutFailing(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm Currency B", "sub-currency-b")
	if _, err := accounting.CreateAccount(ctx, appPool, firmID, userID, "1000", "Cash (USD)", accounting.AccountTypeAsset, nil, "USD"); err != nil {
		t.Fatalf("seed USD account: %v", err)
	}
	if _, err := accounting.CreateAccount(ctx, appPool, firmID, userID, "3000", "Revenue", accounting.AccountTypeRevenue, nil, ""); err != nil {
		t.Fatalf("seed TRY account: %v", err)
	}

	// No exchange_rates row exists for GBP -> USD at all - this must
	// never fail the transition (the whole point of the fallback).
	entryID, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "No rate on file", []accounting.LineInput{
		{AccountCode: "1000", Side: accounting.SideDebit, Amount: "50.00", Currency: "GBP"},
		{AccountCode: "3000", Side: accounting.SideCredit, Amount: "50.00"},
	})
	if err != nil {
		t.Fatalf("PostJournalEntry must not fail when no exchange rate is on file, got: %v", err)
	}

	result, err := accounting.ListJournalEntries(ctx, appPool, firmID, userID, accounting.ListOptions{})
	if err != nil {
		t.Fatalf("ListJournalEntries: %v", err)
	}
	var posted accounting.JournalEntry
	for _, e := range result.Entries {
		if e.ID == entryID {
			posted = e
		}
	}
	var debitLine *accounting.JournalLine
	for i := range posted.Lines {
		if posted.Lines[i].AccountCode == "1000" {
			debitLine = &posted.Lines[i]
		}
	}
	if debitLine == nil {
		t.Fatal("expected a line against account 1000")
	}
	// Fallback: the ORIGINAL amount is stored unconverted, tagged with
	// the account's own currency (USD) - not the impossible-to-resolve
	// "GBP" the caller supplied.
	if debitLine.Currency != "USD" {
		t.Errorf("expected the fallback line's stored currency to be the account's own (USD), got %q", debitLine.Currency)
	}
	if debitLine.Amount != "50.0000" {
		t.Errorf("expected the fallback amount to be the original, unconverted 50.0000, got %q", debitLine.Amount)
	}
}
