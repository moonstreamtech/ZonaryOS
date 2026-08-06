// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package accounting

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// coreAccountCode/salesRevenueAccountCode/tradeReceivablesAccountCode are
// exported so internal/workflow's own concrete workflow specs (e.g.
// stock_to_sale.go's record_sale JournalTemplate) can reference the exact
// same codes this package seeds, instead of duplicating the literal
// strings in a second package with no compile-time link between them.
const (
	TradeReceivablesAccountCode = "1100"
	SalesRevenueAccountCode     = "4000"
)

// seedAccount is one row SeedDefaultChartOfAccountsTx inserts - a plain,
// unexported input shape (not the public Account type) since created_at/
// id aren't known yet at seed time.
type seedAccount struct {
	code string
	name string
	typ  AccountType
}

// coreAccounts are seeded for every firm, regardless of its wizard
// answers - the design brief's "always" tier: a firm without them
// couldn't record even the most basic transaction (cash movement, an
// owner's paid-in capital, retained earnings at year end).
var coreAccounts = []seedAccount{
	{code: "1000", name: "Cash", typ: AccountTypeAsset},
	{code: TradeReceivablesAccountCode, name: "Trade Receivables", typ: AccountTypeAsset},
	{code: "2000", name: "Trade Payables", typ: AccountTypeLiability},
	{code: "3000", name: "Owner Equity", typ: AccountTypeEquity},
	{code: "3100", name: "Retained Earnings", typ: AccountTypeEquity},
}

// sellsAccounts are seeded when the firm's wizard answers say it sells
// products - the same precondition (Vision's design brief) that seeds
// the Stock In -> Sale workflow (internal/wizard.CreateDefaultFirm's
// sel.Sells && sel.TracksInventory), so stock_to_sale.go's record_sale
// JournalTemplate (which posts against TradeReceivablesAccountCode/
// SalesRevenueAccountCode) can always assume these accounts exist
// whenever that workflow does.
var sellsAccounts = []seedAccount{
	{code: "1200", name: "Inventory", typ: AccountTypeAsset},
	{code: SalesRevenueAccountCode, name: "Sales Revenue", typ: AccountTypeRevenue},
	{code: "5000", name: "Cost of Goods Sold", typ: AccountTypeExpense},
}

// purchasesAccounts are seeded when the firm's wizard answers say it
// purchases from suppliers.
var purchasesAccounts = []seedAccount{
	{code: "2100", name: "Accounts Payable", typ: AccountTypeLiability},
	{code: "5100", name: "Purchase Expense", typ: AccountTypeExpense},
}

// SeedChartOptions gates which of the parametric, wizard-answer-dependent
// account groups SeedDefaultChartOfAccountsTx seeds beyond the always-on
// core tier - a plain struct of booleans (not internal/wizard.SeedSelection
// itself) so this package doesn't depend on internal/wizard (which
// already depends on internal/workflow; this package must stay a leaf
// dependency both internal/workflow and internal/wizard can import
// without a cycle).
type SeedChartOptions struct {
	// Sells seeds sellsAccounts (Inventory, Sales Revenue, Cost of Goods
	// Sold) - mirrors internal/wizard.SeedSelection.Sells.
	Sells bool
	// PurchasesFromSuppliers seeds purchasesAccounts (Accounts Payable,
	// Purchase Expense) - mirrors
	// internal/wizard.SeedSelection.PurchasesFromSuppliers.
	//
	// Manufacturing's own accounts (Work in Progress, Finished Goods) are
	// deliberately not represented here at all: internal/wizard's own
	// decision tree has no manufacturing branch yet (it's still a
	// NodePlaceholder dead end - see internal/wizard/tree.go's package
	// doc comment), so there is no wizard answer that could ever set a
	// corresponding option here. Adding those two accounts now, unseeded
	// by anything, would just be dead schema.
	PurchasesFromSuppliers bool
}

// SeedDefaultChartOfAccountsTx seeds firmID's starter chart of accounts,
// parametrically, from opts - the design brief's "seed a minimal but real
// default chart of accounts based on the wizard's answers." Intended to
// be called once, from inside the same transaction
// internal/wizard.CreateDefaultFirm already uses to provision a new firm
// (mirroring internal/auditlog.RegisterReadPermissionTx and
// internal/workflow.SeedStockToSaleWorkflowTx's own "*Tx, called mid firm-
// creation-transaction" shape) - not a general-purpose account-creation
// path (that's CreateAccount, owner-gated, for after the firm exists).
//
// ciaudit:ignore-firmid-check: provisioning-only helper, only called by
// internal/wizard.CreateDefaultFirm with a firmID it just created in the
// same transaction - never reachable with a caller-supplied firmID via an
// HTTP handler.
func SeedDefaultChartOfAccountsTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, opts SeedChartOptions) error {
	accounts := make([]seedAccount, 0, len(coreAccounts)+len(sellsAccounts)+len(purchasesAccounts))
	accounts = append(accounts, coreAccounts...)
	if opts.Sells {
		accounts = append(accounts, sellsAccounts...)
	}
	if opts.PurchasesFromSuppliers {
		accounts = append(accounts, purchasesAccounts...)
	}

	for _, a := range accounts {
		if _, err := tx.Exec(ctx, `
			INSERT INTO accounts (firm_id, code, name, type) VALUES ($1, $2, $3, $4)
		`, firmID, a.code, a.name, string(a.typ)); err != nil {
			return fmt.Errorf("seed account %q: %w", a.code, err)
		}
	}
	return nil
}
