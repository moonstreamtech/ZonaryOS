// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package portability

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

const (
	importAuditEntityType = "firm_import"
	importAuditAction     = "import_configuration"
)

// ImportCounts is how many rows of one entity kind ImportConfiguration
// actually inserted versus skipped because they already existed.
type ImportCounts struct {
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

// ImportSummary is ImportConfiguration's result - one ImportCounts per
// configuration entity kind it handles. Historical data (workflow
// instances, journal entries, invoices, deliveries, stock levels,
// customers, suppliers, people) is never imported - see this package's
// own doc comment - so there is deliberately no summary field for any of
// those; ImportSummary's own shape is the honest, complete list of what
// this endpoint actually touches.
type ImportSummary struct {
	WorkflowDefinitions ImportCounts `json:"workflowDefinitions"`
	Rules               ImportCounts `json:"rules"`
	Accounts            ImportCounts `json:"accounts"`
	Products            ImportCounts `json:"products"`
}

// ImportConfiguration imports ONLY the configuration portion of doc
// (workflow_definitions, rules, chart-of-accounts accounts, products)
// into firmID - never doc's historical-data fields (workflow_instances,
// journal_entries, invoices, deliveries, stock_levels, customers,
// suppliers, people), which this function doesn't even read. Wrapped in
// one WithFirmContext transaction - either every entity that isn't
// skipped actually commits, or (on the first hard error) none of them
// do; a skip is not an error and never aborts the transaction.
//
// Idempotent by construction, not by a pre-flight diff: each entity kind
// is imported through its own module's real creation primitive
// (DefineWorkflowTx / CreateRuleTx / CreateAccountTx / CreateProductTx),
// and a collision against that entity's own natural, already-enforced
// uniqueness (a workflow's key, an account's (firm, code), a product's
// (firm, sku) - all real DB constraints already relied on elsewhere in
// this codebase) is caught and counted as Skipped rather than treated as
// a failure. Rules have no such DB constraint of their own (workflow_rules
// carries no unique index on (definition_key, name) - see
// workflow.RuleExistsTx's own doc comment), so rule dedup is one explicit
// SELECT before each attempted insert instead. Importing the exact same
// ExportDocument twice therefore always produces the same end state:
// the second run's counts are all Skipped, nothing is duplicated.
//
// Chart-of-accounts hierarchy (accounts.parent_id) is deliberately
// flattened on import: a source account's ParentID is a UUID from the
// EXPORTING firm's own database, meaningless (and potentially referring
// to a real row in a different firm entirely) in the importing firm -
// reconstructing the hierarchy would need a second pass remapping parent
// UUIDs by code after every account is created, out of scope for this
// batch's "configuration only, skip-if-exists" contract. Every imported
// account is created with no parent.
//
// Owner-gated: importing configuration into a firm is a structural,
// sensitive operation, the same tier as internal/workflow.DefineWorkflowForFirm.
func ImportConfiguration(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, doc ExportDocument) (ImportSummary, error) {
	if doc.Version != ExportVersion {
		return ImportSummary{}, fmt.Errorf("%w: unsupported version %q (expected %q)", ErrInvalidExportDocument, doc.Version, ExportVersion)
	}

	var summary ImportSummary
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

		// The importing owner's own role is the self-action auto-grant
		// target for any newly-imported workflow definition's permission
		// keys - the same lookup, and the same reasoning, DefineWorkflowForFirm
		// itself uses (engine.go): whoever triggers the import shouldn't
		// be left holding a freshly-imported workflow nobody can use yet.
		var granteeRoleID uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT r.id FROM user_firm_roles ufr
			JOIN roles r ON r.id = ufr.role_id
			WHERE ufr.user_id = $1 AND ufr.firm_id = $2 AND r.is_owner
			LIMIT 1
		`, userID, firmID).Scan(&granteeRoleID); err != nil {
			return fmt.Errorf("look up acting owner's role: %w", err)
		}

		for _, spec := range doc.WorkflowDefinitions {
			err := withSavepoint(ctx, tx, func(spTx pgx.Tx) error {
				_, err := workflow.DefineWorkflowTx(ctx, spTx, firmID, granteeRoleID, spec)
				return err
			})
			if err != nil {
				if errors.Is(err, workflow.ErrDefinitionKeyExists) {
					summary.WorkflowDefinitions.Skipped++
					continue
				}
				return fmt.Errorf("import workflow definition %q: %w", spec.Key, err)
			}
			summary.WorkflowDefinitions.Imported++
		}

		for _, rule := range doc.Rules {
			exists, err := workflow.RuleExistsTx(ctx, tx, firmID, rule.DefinitionKey, rule.Name)
			if err != nil {
				return err
			}
			if exists {
				summary.Rules.Skipped++
				continue
			}
			rule.FirmID = firmID
			if _, err := workflow.CreateRuleTx(ctx, tx, rule); err != nil {
				return fmt.Errorf("import rule %q (definition %q): %w", rule.Name, rule.DefinitionKey, err)
			}
			summary.Rules.Imported++
		}

		for _, a := range doc.Accounts {
			err := withSavepoint(ctx, tx, func(spTx pgx.Tx) error {
				_, err := accounting.CreateAccountTx(ctx, spTx, firmID, a.Code, a.Name, a.Type, nil, a.Currency)
				return err
			})
			if err != nil {
				if errors.Is(err, accounting.ErrAccountCodeExists) {
					summary.Accounts.Skipped++
					continue
				}
				return fmt.Errorf("import account %q: %w", a.Code, err)
			}
			summary.Accounts.Imported++
		}

		for _, p := range doc.Products {
			input := inventory.CreateProductInput{
				SKU: p.SKU, Name: p.Name, Description: derefOr(p.Description), Unit: p.Unit,
				UnitPrice: derefOr(p.UnitPrice), CostPrice: derefOr(p.CostPrice), TaxRate: derefOr(p.TaxRate),
				Category: derefOr(p.Category), MinQuantity: p.MinQuantity, CustomFields: p.CustomFields,
			}
			err := withSavepoint(ctx, tx, func(spTx pgx.Tx) error {
				_, err := inventory.CreateProductTx(ctx, spTx, firmID, input)
				return err
			})
			if err != nil {
				if errors.Is(err, inventory.ErrProductSKUExists) {
					summary.Products.Skipped++
					continue
				}
				return fmt.Errorf("import product %q: %w", p.SKU, err)
			}
			summary.Products.Imported++
		}

		return auditlog.Write(ctx, tx, firmID, userID, firmID, importAuditEntityType, importAuditAction, map[string]any{
			"workflowDefinitionsImported": summary.WorkflowDefinitions.Imported, "workflowDefinitionsSkipped": summary.WorkflowDefinitions.Skipped,
			"rulesImported": summary.Rules.Imported, "rulesSkipped": summary.Rules.Skipped,
			"accountsImported": summary.Accounts.Imported, "accountsSkipped": summary.Accounts.Skipped,
			"productsImported": summary.Products.Imported, "productsSkipped": summary.Products.Skipped,
		})
	})
	if err != nil {
		return ImportSummary{}, err
	}
	return summary, nil
}

func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// withSavepoint runs fn inside a nested transaction (pgx issues this as a
// real SAVEPOINT on tx, since tx is itself already an open transaction).
// This matters specifically because the collision detection each entity
// loop in ImportConfiguration relies on (ErrDefinitionKeyExists /
// ErrAccountCodeExists / ErrProductSKUExists) is implemented by catching a
// genuine Postgres unique_violation error, and Postgres aborts an entire
// transaction the instant any statement inside it errors - simply catching
// the Go error and continuing to reuse tx would leave every later
// statement in this same import failing with "current transaction is
// aborted". Running each collision-prone insert inside its own savepoint
// lets a skip roll back just that savepoint, leaving tx (and everything
// already imported before it) intact.
func withSavepoint(ctx context.Context, tx pgx.Tx, fn func(pgx.Tx) error) error {
	spTx, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(spTx); err != nil {
		_ = spTx.Rollback(ctx)
		return err
	}
	return spTx.Commit(ctx)
}
