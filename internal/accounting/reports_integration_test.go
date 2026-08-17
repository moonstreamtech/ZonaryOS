// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package accounting_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
)

// TestGetProfitAndLoss_GroupsByAccountAndComputesNetIncome posts two
// sales (DR Trade Receivables / CR Sales Revenue) and one expense entry
// (DR Cost of Goods Sold / CR Trade Payables), then confirms the P&L
// report groups by account (not just a single total) and computes net
// income as revenue minus expenses.
func TestGetProfitAndLoss_GroupsByAccountAndComputesNetIncome(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "pnl-owner")
	seedAccount(ctx, t, appPool, firmID, userID, "1100", "Trade Receivables", accounting.AccountTypeAsset)
	seedAccount(ctx, t, appPool, firmID, userID, "4000", "Sales Revenue", accounting.AccountTypeRevenue)
	seedAccount(ctx, t, appPool, firmID, userID, "5000", "Cost of Goods Sold", accounting.AccountTypeExpense)
	seedAccount(ctx, t, appPool, firmID, userID, "2000", "Trade Payables", accounting.AccountTypeLiability)

	if _, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "Sale 1", []accounting.LineInput{
		{AccountCode: "1100", Side: accounting.SideDebit, Amount: "100.00"},
		{AccountCode: "4000", Side: accounting.SideCredit, Amount: "100.00"},
	}); err != nil {
		t.Fatalf("post sale 1: %v", err)
	}
	if _, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "Sale 2", []accounting.LineInput{
		{AccountCode: "1100", Side: accounting.SideDebit, Amount: "50.00"},
		{AccountCode: "4000", Side: accounting.SideCredit, Amount: "50.00"},
	}); err != nil {
		t.Fatalf("post sale 2: %v", err)
	}
	if _, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "COGS", []accounting.LineInput{
		{AccountCode: "5000", Side: accounting.SideDebit, Amount: "30.00"},
		{AccountCode: "2000", Side: accounting.SideCredit, Amount: "30.00"},
	}); err != nil {
		t.Fatalf("post cogs: %v", err)
	}

	report, err := accounting.GetProfitAndLoss(ctx, appPool, firmID, userID, nil, nil)
	if err != nil {
		t.Fatalf("GetProfitAndLoss: %v", err)
	}

	if len(report.Revenue) != 1 {
		t.Fatalf("expected exactly one revenue account line, got %+v", report.Revenue)
	}
	if report.Revenue[0].Amount != "150.0000" {
		t.Errorf("expected Sales Revenue amount 150.0000 (100+50), got %q", report.Revenue[0].Amount)
	}
	if report.TotalRevenue != "150.0000" {
		t.Errorf("expected total revenue 150.0000, got %q", report.TotalRevenue)
	}

	if len(report.Expenses) != 1 {
		t.Fatalf("expected exactly one expense account line, got %+v", report.Expenses)
	}
	if report.Expenses[0].Amount != "30.0000" {
		t.Errorf("expected COGS amount 30.0000, got %q", report.Expenses[0].Amount)
	}
	if report.TotalExpenses != "30.0000" {
		t.Errorf("expected total expenses 30.0000, got %q", report.TotalExpenses)
	}

	if report.NetIncome != "120.0000" {
		t.Errorf("expected net income 120.0000 (150-30), got %q", report.NetIncome)
	}
}

// TestGetProfitAndLoss_DateFiltering confirms ?from=/?to= actually excludes
// entries outside the window - posting two sales a day apart and querying
// a period that only covers the first.
func TestGetProfitAndLoss_DateFiltering(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "pnl-date-owner")
	seedAccount(ctx, t, appPool, firmID, userID, "1100", "Trade Receivables", accounting.AccountTypeAsset)
	seedAccount(ctx, t, appPool, firmID, userID, "4000", "Sales Revenue", accounting.AccountTypeRevenue)

	if _, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "In range", []accounting.LineInput{
		{AccountCode: "1100", Side: accounting.SideDebit, Amount: "10.00"},
		{AccountCode: "4000", Side: accounting.SideCredit, Amount: "10.00"},
	}); err != nil {
		t.Fatalf("post entry: %v", err)
	}

	now := time.Now().UTC()
	from := now.Add(-time.Hour)
	to := now.Add(time.Hour)
	report, err := accounting.GetProfitAndLoss(ctx, appPool, firmID, userID, &from, &to)
	if err != nil {
		t.Fatalf("GetProfitAndLoss (in range): %v", err)
	}
	if report.TotalRevenue != "10.0000" {
		t.Errorf("expected the entry to be included in range, total revenue 10.0000, got %q", report.TotalRevenue)
	}

	pastFrom := now.Add(-48 * time.Hour)
	pastTo := now.Add(-24 * time.Hour)
	pastReport, err := accounting.GetProfitAndLoss(ctx, appPool, firmID, userID, &pastFrom, &pastTo)
	if err != nil {
		t.Fatalf("GetProfitAndLoss (out of range): %v", err)
	}
	if pastReport.TotalRevenue != "0" {
		t.Errorf("expected no revenue for a window before the entry was posted, got %q", pastReport.TotalRevenue)
	}
	if len(pastReport.Revenue) != 1 || pastReport.Revenue[0].Amount != "0" {
		t.Errorf("expected the revenue account to still appear with a zero amount, got %+v", pastReport.Revenue)
	}
}

// TestGetProfitAndLoss_IncludesBudgetVarianceWhenActiveBudgetCoversPeriod
// confirms attachBudgetVarianceTx populates BudgetedAmount/Variance/
// VariancePercent on the matching expense line when an 'active' budget
// covers the report's [from,to] period, and that a request outside any
// budget's period (or with no from/to at all) leaves those fields nil -
// internal/budget itself isn't imported here (internal/accounting stays a
// leaf dependency), the budget/budget_lines rows are seeded via raw SQL,
// same as internal/costcenter's own tests avoid importing internal/budget
// for fixture setup.
func TestGetProfitAndLoss_IncludesBudgetVarianceWhenActiveBudgetCoversPeriod(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "pnl-budget-owner")
	seedAccount(ctx, t, appPool, firmID, userID, "5000", "Cost of Goods Sold", accounting.AccountTypeExpense)
	seedAccount(ctx, t, appPool, firmID, userID, "2000", "Trade Payables", accounting.AccountTypeLiability)

	var expenseAccountID uuid.UUID
	if err := adminPool.QueryRow(ctx, `SELECT id FROM accounts WHERE firm_id = $1 AND code = '5000'`, firmID).Scan(&expenseAccountID); err != nil {
		t.Fatalf("look up expense account: %v", err)
	}

	if _, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "COGS", []accounting.LineInput{
		{AccountCode: "5000", Side: accounting.SideDebit, Amount: "100.00"},
		{AccountCode: "2000", Side: accounting.SideCredit, Amount: "100.00"},
	}); err != nil {
		t.Fatalf("post cogs: %v", err)
	}

	yesterday := time.Now().Add(-24 * time.Hour)
	tomorrow := time.Now().Add(24 * time.Hour)
	var budgetID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO budgets (firm_id, name, period_type, period_start, period_end, status)
		VALUES ($1, 'Test Budget', 'monthly', $2::date, $3::date, 'active') RETURNING id
	`, firmID, yesterday, tomorrow).Scan(&budgetID); err != nil {
		t.Fatalf("seed budget: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO budget_lines (firm_id, budget_id, account_id, planned_amount) VALUES ($1, $2, $3, 80.00)
	`, firmID, budgetID, expenseAccountID); err != nil {
		t.Fatalf("seed budget line: %v", err)
	}

	from := time.Now().Add(-time.Hour)
	to := time.Now().Add(time.Hour)
	report, err := accounting.GetProfitAndLoss(ctx, appPool, firmID, userID, &from, &to)
	if err != nil {
		t.Fatalf("GetProfitAndLoss (within budget period): %v", err)
	}
	if len(report.Expenses) != 1 {
		t.Fatalf("expected exactly one expense account line, got %+v", report.Expenses)
	}
	line := report.Expenses[0]
	if line.BudgetedAmount == nil || *line.BudgetedAmount != "80.0000" {
		t.Fatalf("expected BudgetedAmount 80.0000, got %v", line.BudgetedAmount)
	}
	if line.Variance == nil || *line.Variance != "20.0000" {
		t.Fatalf("expected Variance 20.0000 (100 actual - 80 budgeted), got %v", line.Variance)
	}
	if line.VariancePercent == nil {
		t.Fatal("expected a non-nil VariancePercent")
	}
	percent, err := strconv.ParseFloat(*line.VariancePercent, 64)
	if err != nil {
		t.Fatalf("parse VariancePercent %q: %v", *line.VariancePercent, err)
	}
	if percent < 24.999 || percent > 25.001 {
		t.Errorf("expected VariancePercent ~25 (20/80*100), got %v", percent)
	}

	// Outside the budget's period entirely: no active budget covers it,
	// budget fields must stay nil.
	farFrom := time.Now().Add(-72 * time.Hour)
	farTo := time.Now().Add(-48 * time.Hour)
	outOfRangeReport, err := accounting.GetProfitAndLoss(ctx, appPool, firmID, userID, &farFrom, &farTo)
	if err != nil {
		t.Fatalf("GetProfitAndLoss (outside budget period): %v", err)
	}
	if len(outOfRangeReport.Expenses) != 1 || outOfRangeReport.Expenses[0].BudgetedAmount != nil {
		t.Errorf("expected no budget variance outside the budget's period, got %+v", outOfRangeReport.Expenses)
	}

	// No from/to at all: attachBudgetVarianceTx is a no-op, same as the
	// "no period to match a budget's own period against" case.
	noRangeReport, err := accounting.GetProfitAndLoss(ctx, appPool, firmID, userID, nil, nil)
	if err != nil {
		t.Fatalf("GetProfitAndLoss (no range): %v", err)
	}
	if len(noRangeReport.Expenses) != 1 || noRangeReport.Expenses[0].BudgetedAmount != nil {
		t.Errorf("expected no budget variance with no from/to bound, got %+v", noRangeReport.Expenses)
	}
}

// TestGetBalanceSheet_BalancesWithUnclosedEarnings confirms the
// fundamental accounting equation (Assets = Liabilities + Equity) holds
// once current-period earnings are folded into equity - see
// GetBalanceSheet's own doc comment for why that's the correct interim
// treatment given this batch has no period-closing mechanism.
func TestGetBalanceSheet_BalancesWithUnclosedEarnings(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "bs-owner")
	seedAccount(ctx, t, appPool, firmID, userID, "1100", "Trade Receivables", accounting.AccountTypeAsset)
	seedAccount(ctx, t, appPool, firmID, userID, "4000", "Sales Revenue", accounting.AccountTypeRevenue)
	seedAccount(ctx, t, appPool, firmID, userID, "3000", "Owner Equity", accounting.AccountTypeEquity)
	seedAccount(ctx, t, appPool, firmID, userID, "1000", "Cash", accounting.AccountTypeAsset)

	// Owner contributes capital: DR Cash / CR Owner Equity.
	if _, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "Capital contribution", []accounting.LineInput{
		{AccountCode: "1000", Side: accounting.SideDebit, Amount: "1000.00"},
		{AccountCode: "3000", Side: accounting.SideCredit, Amount: "1000.00"},
	}); err != nil {
		t.Fatalf("post capital contribution: %v", err)
	}
	// A sale with no closing entry: DR Trade Receivables / CR Sales
	// Revenue - this is exactly the shape that would leave a naive
	// (non-earnings-folded) balance sheet permanently "out of balance".
	if _, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "Sale", []accounting.LineInput{
		{AccountCode: "1100", Side: accounting.SideDebit, Amount: "200.00"},
		{AccountCode: "4000", Side: accounting.SideCredit, Amount: "200.00"},
	}); err != nil {
		t.Fatalf("post sale: %v", err)
	}

	report, err := accounting.GetBalanceSheet(ctx, appPool, firmID, userID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("GetBalanceSheet: %v", err)
	}

	if report.TotalAssets != "1200.0000" {
		t.Errorf("expected total assets 1200.0000 (1000 cash + 200 receivables), got %q", report.TotalAssets)
	}
	if report.TotalLiabilities != "0" {
		t.Errorf("expected total liabilities 0, got %q", report.TotalLiabilities)
	}
	if report.CurrentEarnings != "200.0000" {
		t.Errorf("expected current earnings 200.0000 (unclosed sales revenue), got %q", report.CurrentEarnings)
	}
	if report.TotalEquity != "1200.0000" {
		t.Errorf("expected total equity 1200.0000 (1000 contributed + 200 current earnings), got %q", report.TotalEquity)
	}
	if !report.Balanced {
		t.Errorf("expected the balance sheet to balance once current earnings are folded into equity, got difference %q", report.Difference)
	}
	if report.Difference != "0" && report.Difference != "0.0000" {
		t.Errorf("expected a zero difference, got %q", report.Difference)
	}
}

// TestGetBalanceSheet_AsOfExcludesFutureEntries confirms the as_of bound
// actually excludes entries posted after it.
func TestGetBalanceSheet_AsOfExcludesFutureEntries(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "bs-asof-owner")
	seedAccount(ctx, t, appPool, firmID, userID, "1000", "Cash", accounting.AccountTypeAsset)
	seedAccount(ctx, t, appPool, firmID, userID, "3000", "Owner Equity", accounting.AccountTypeEquity)

	past := time.Now().Add(-time.Hour)

	if _, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "Contribution", []accounting.LineInput{
		{AccountCode: "1000", Side: accounting.SideDebit, Amount: "500.00"},
		{AccountCode: "3000", Side: accounting.SideCredit, Amount: "500.00"},
	}); err != nil {
		t.Fatalf("post contribution: %v", err)
	}

	beforeReport, err := accounting.GetBalanceSheet(ctx, appPool, firmID, userID, past)
	if err != nil {
		t.Fatalf("GetBalanceSheet (as_of in the past): %v", err)
	}
	if beforeReport.TotalAssets != "0" {
		t.Errorf("expected zero assets as of before the entry was posted, got %q", beforeReport.TotalAssets)
	}
	if !beforeReport.Balanced {
		t.Errorf("expected an empty (zero-everywhere) balance sheet to still balance, got difference %q", beforeReport.Difference)
	}

	nowReport, err := accounting.GetBalanceSheet(ctx, appPool, firmID, userID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("GetBalanceSheet (as_of now): %v", err)
	}
	if nowReport.TotalAssets != "500.0000" {
		t.Errorf("expected total assets 500.0000 once the entry is included, got %q", nowReport.TotalAssets)
	}
}
