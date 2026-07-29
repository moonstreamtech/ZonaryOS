package workflow

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StockToSaleKey is the workflow_definitions.key for this PR's concrete
// first workflow instance. "Add stock" is instance creation (in_stock is
// the initial state); "record a sale" is the one transition, in_stock ->
// sold. No wizard integration yet - SeedStockToSaleWorkflow is the
// migration/fixture-level stand-in the wizard will eventually replace.
const StockToSaleKey = "stock_to_sale"

// Permission keys the Stock In -> Sale workflow registers into the global
// permissions catalog (see DefineWorkflow) - exported so callers and tests
// reference the same constants instead of duplicating string literals.
const (
	AddStockPermission   = "workflow.stock_to_sale.add_stock"
	RecordSalePermission = "workflow.stock_to_sale.record_sale"
)

// StockToSaleSpec is the concrete two-step "Stock In -> Sale" workflow
// described in the vertical slice's original scope.
var StockToSaleSpec = DefinitionSpec{
	Key:  StockToSaleKey,
	Name: "Stock In -> Sale",
	CreatePermission: PermissionSpec{
		Key:         AddStockPermission,
		Description: "Add a new stock item (starts a Stock In -> Sale workflow instance).",
	},
	States: []StateSpec{
		{Key: "in_stock", Name: "In Stock", IsInitial: true},
		{Key: "sold", Name: "Sold", IsTerminal: true},
	},
	Transitions: []TransitionSpec{
		{
			FromStateKey: "in_stock",
			ToStateKey:   "sold",
			ActionKey:    "record_sale",
			Name:         "Record Sale",
			Permission: PermissionSpec{
				Key:         RecordSalePermission,
				Description: "Record the sale of an in-stock item.",
			},
		},
	},
}

// SeedStockToSaleWorkflow instantiates StockToSaleSpec for firmID,
// returning its workflow_definitions.id. Intended for test fixtures and
// firm provisioning until the wizard (a later PR) can define workflows
// like this one interactively.
func SeedStockToSaleWorkflow(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflow(ctx, pool, firmID, StockToSaleSpec)
}

// SeedStockToSaleWorkflowTx is SeedStockToSaleWorkflow's DefineWorkflowTx
// counterpart, for callers that need it as one step inside a larger
// transaction - namely the firm-creation wizard, which provisions this
// workflow atomically alongside the firm itself, its default role, and
// that role's permission grants.
func SeedStockToSaleWorkflowTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflowTx(ctx, tx, firmID, StockToSaleSpec)
}
