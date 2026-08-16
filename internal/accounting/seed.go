// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package accounting

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// These account-code constants are exported so internal/workflow's own
// concrete workflow specs (e.g. stock_to_sale.go's record_sale/
// purchase_order.go's receive JournalTemplate) can reference the exact
// same codes this package seeds, instead of duplicating the literal
// strings in a second package with no compile-time link between them.
const (
	CashAccountCode             = "1000"
	TradeReceivablesAccountCode = "1100"
	InventoryAccountCode        = "1200"
	TradePayablesAccountCode    = "2000"
	SalesRevenueAccountCode     = "4000"
	// SalariesPayableAccountCode/SalaryExpenseAccountCode (payroll
	// foundation batch): "2200"/"5200" - the next free liability/expense
	// codes after TradePayablesAccountCode's "2000"/Accounts Payable's
	// "2100" and Cost of Goods Sold's "5000"/Purchase Expense's "5100"
	// respectively (see peopleAccounts below and coreAccounts/
	// purchasesAccounts above for what's already taken).
	SalariesPayableAccountCode = "2200"
	SalaryExpenseAccountCode   = "5200"
	// WorkInProgressAccountCode/FinishedGoodsAccountCode (manufacturing
	// module foundation batch): "1300"/"1400" - the next free asset codes
	// after Inventory's "1200" (see manufacturingAccounts below and
	// coreAccounts/sellsAccounts above for what's already taken).
	WorkInProgressAccountCode = "1300"
	FinishedGoodsAccountCode  = "1400"
	// InventoryAdjustmentAccountCode (warehouse management/cycle counting
	// batch): "1500" - the next free asset code after Finished Goods'
	// "1400". internal/warehouse's cycle-count completion posts a
	// balanced entry against this account and InventoryAccountCode for
	// each line whose counted quantity differs from system_quantity, the
	// same "correct the ledger, don't rewrite stock_movements in place"
	// pattern manufacturing's own WIP/Finished Goods postings establish.
	InventoryAdjustmentAccountCode = "1500"
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
	{code: CashAccountCode, name: "Cash", typ: AccountTypeAsset},
	{code: TradeReceivablesAccountCode, name: "Trade Receivables", typ: AccountTypeAsset},
	{code: TradePayablesAccountCode, name: "Trade Payables", typ: AccountTypeLiability},
	{code: "3000", name: "Owner Equity", typ: AccountTypeEquity},
	{code: "3100", name: "Retained Earnings", typ: AccountTypeEquity},
}

// sellsAccounts are seeded when the firm's wizard answers say it sells
// products - the same precondition (Vision's design brief) that seeds
// the Stock In -> Sale workflow (internal/wizard.CreateDefaultFirm's
// sel.Sells && sel.TracksInventory), so stock_to_sale.go's record_sale
// JournalTemplate (which posts against TradeReceivablesAccountCode/
// SalesRevenueAccountCode) can always assume these accounts exist
// whenever that workflow does. InventoryAccountCode is ALSO seeded by
// purchasesAccounts below (deduplicated by SeedDefaultChartOfAccountsTx,
// see its own doc comment) - a firm can track inventory without also
// purchasing from suppliers (e.g. manufacturing its own stock, not
// modeled yet either) or purchase from suppliers without selling
// products at all (e.g. a service firm buying non-resale supplies), so
// neither flag alone is a reliable precondition for the other.
var sellsAccounts = []seedAccount{
	{code: InventoryAccountCode, name: "Inventory", typ: AccountTypeAsset},
	{code: SalesRevenueAccountCode, name: "Sales Revenue", typ: AccountTypeRevenue},
	{code: "5000", name: "Cost of Goods Sold", typ: AccountTypeExpense},
}

// purchasesAccounts are seeded when the firm's wizard answers say it
// purchases from suppliers. InventoryAccountCode is included here too
// (not just in sellsAccounts) so purchase_order.go's receive
// JournalTemplate (DR Inventory / CR Trade Payables - goods actually
// received from a supplier) can always assume it exists whenever the
// Purchase Order workflow does, regardless of whether the firm also
// answered "yes" to selling products - see this batch's report for the
// full reasoning (a purchase_order-without-Inventory firm would otherwise
// hit ErrAccountNotFound the first time it executed "receive").
var purchasesAccounts = []seedAccount{
	{code: InventoryAccountCode, name: "Inventory", typ: AccountTypeAsset},
	{code: "2100", name: "Accounts Payable", typ: AccountTypeLiability},
	{code: "5100", name: "Purchase Expense", typ: AccountTypeExpense},
}

// peopleAccounts are seeded when the firm's wizard answers say it manages
// people (employees/contractors it runs payroll for) - the same
// precondition internal/wizard.SeedSelection.ManagesPeople gates, so
// internal/payroll's period-close journal posting (DR Salary Expense /
// CR Salaries Payable) can always assume these two accounts exist
// whenever a firm has payroll periods to close at all.
var peopleAccounts = []seedAccount{
	{code: SalaryExpenseAccountCode, name: "Salary Expense", typ: AccountTypeExpense},
	{code: SalariesPayableAccountCode, name: "Salaries Payable", typ: AccountTypeLiability},
}

// manufacturingAccounts are seeded when the firm's wizard answers say it
// manufactures products - the same precondition
// internal/wizard.SeedSelection.SeedManufacturing gates, so
// internal/manufacturing's production-order material-issue/completion
// journal postings (DR Work in Progress / CR Inventory on issue, DR
// Finished Goods / CR Work in Progress on completion) can always assume
// these two accounts exist whenever a firm has production orders to run
// at all. InventoryAccountCode is included here too (same reasoning
// purchasesAccounts' own doc comment gives for including it alongside
// sellsAccounts): a manufacturing-only firm - SeedManufacturing true but
// neither Sells nor PurchasesFromSuppliers - would otherwise have nowhere
// to issue components FROM, hitting ErrAccountNotFound the first time it
// started a production order.
var manufacturingAccounts = []seedAccount{
	{code: InventoryAccountCode, name: "Inventory", typ: AccountTypeAsset},
	{code: WorkInProgressAccountCode, name: "Work in Progress", typ: AccountTypeAsset},
	{code: FinishedGoodsAccountCode, name: "Finished Goods", typ: AccountTypeAsset},
}

// inventoryAccounts are seeded when the firm's wizard answers say it
// tracks physical inventory - the same precondition
// internal/wizard.SeedSelection.TracksInventory gates, so
// internal/warehouse's cycle-count adjustment postings (DR/CR Inventory
// Adjustment) can always assume this account exists whenever a firm has
// cycle counts to run at all. InventoryAccountCode is included here too
// (same reasoning purchasesAccounts/manufacturingAccounts already give
// for including it): a firm that tracks inventory but answered "no" to
// selling/purchasing/manufacturing would otherwise have no Inventory
// account for the adjustment's other leg.
var inventoryAccounts = []seedAccount{
	{code: InventoryAccountCode, name: "Inventory", typ: AccountTypeAsset},
	{code: InventoryAdjustmentAccountCode, name: "Inventory Adjustment", typ: AccountTypeExpense},
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
	PurchasesFromSuppliers bool
	// ManagesPeople seeds peopleAccounts (Salary Expense, Salaries
	// Payable) - mirrors internal/wizard.SeedSelection.ManagesPeople.
	ManagesPeople bool
	// SeedManufacturing seeds manufacturingAccounts (Inventory, Work in
	// Progress, Finished Goods) - mirrors
	// internal/wizard.SeedSelection.SeedManufacturing.
	SeedManufacturing bool
	// TracksInventory seeds inventoryAccounts (Inventory, Inventory
	// Adjustment) - mirrors internal/wizard.SeedSelection.TracksInventory.
	TracksInventory bool
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
	// Deduplicated by code, not a plain concatenated slice: InventoryAccountCode
	// appears in both sellsAccounts and purchasesAccounts (see their own
	// doc comments above) - a firm that answered "yes" to both would
	// otherwise hit accounts' UNIQUE (firm_id, code) constraint trying to
	// insert "1200" twice. A map keyed by code, converted to a
	// code-sorted slice for a deterministic insert order, handles this
	// cleanly regardless of which combination of groups is active.
	byCode := make(map[string]seedAccount, len(coreAccounts)+len(sellsAccounts)+len(purchasesAccounts)+len(peopleAccounts)+len(manufacturingAccounts)+len(inventoryAccounts))
	for _, a := range coreAccounts {
		byCode[a.code] = a
	}
	if opts.Sells {
		for _, a := range sellsAccounts {
			byCode[a.code] = a
		}
	}
	if opts.PurchasesFromSuppliers {
		for _, a := range purchasesAccounts {
			byCode[a.code] = a
		}
	}
	if opts.ManagesPeople {
		for _, a := range peopleAccounts {
			byCode[a.code] = a
		}
	}
	if opts.SeedManufacturing {
		for _, a := range manufacturingAccounts {
			byCode[a.code] = a
		}
	}
	if opts.TracksInventory {
		for _, a := range inventoryAccounts {
			byCode[a.code] = a
		}
	}

	codes := make([]string, 0, len(byCode))
	for code := range byCode {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	for _, code := range codes {
		a := byCode[code]
		if _, err := tx.Exec(ctx, `
			INSERT INTO accounts (firm_id, code, name, type) VALUES ($1, $2, $3, $4)
		`, firmID, a.code, a.name, string(a.typ)); err != nil {
			return fmt.Errorf("seed account %q: %w", a.code, err)
		}
	}
	return nil
}
