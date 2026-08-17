// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/moonstreamtech/ZonaryOS/internal/costcenter"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// TestExecuteTransition_PropagatesCostCenterIDToJournalLines confirms
// that a bridged transition's payload carrying a well-known
// "cost_center_id" field gets propagated onto every posted journal_lines
// row - internal/workflow's resolveJournalLines reads this key directly
// off the raw payload (the same way it already reads "currency"),
// decoupled from FieldTypeCostCenter itself (that only adds existence
// validation, a separate concern - this test's own definition doesn't
// even declare the field as FieldTypeCostCenter, to prove the bridge
// works independent of it).
func TestExecuteTransition_PropagatesCostCenterIDToJournalLines(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID := seedFirm(ctx, t, adminPool, "Firm A")
	ownerID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "cost-center-bridge-owner")

	var expenseAccountID, cashAccountID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO accounts (firm_id, code, name, type) VALUES ($1, '5000', 'Expense', 'expense') RETURNING id
	`, firmID).Scan(&expenseAccountID); err != nil {
		t.Fatalf("seed expense account: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO accounts (firm_id, code, name, type) VALUES ($1, '1000', 'Cash', 'asset') RETURNING id
	`, firmID).Scan(&cashAccountID); err != nil {
		t.Fatalf("seed cash account: %v", err)
	}

	cc, err := costcenter.CreateCostCenter(ctx, appPool, firmID, ownerID, costcenter.CreateCostCenterInput{Code: "OPS", Name: "Operations"})
	if err != nil {
		t.Fatalf("CreateCostCenter: %v", err)
	}

	spec := workflow.DefinitionSpec{
		Key: "expense_report", Name: "Expense Report",
		CreatePermission: workflow.PermissionSpec{Key: "workflow.expense_report.create"},
		Fields: []workflow.FieldSpec{
			{Name: "amount", Type: workflow.FieldTypeNumber, Required: true},
			{Name: "cost_center_id", Type: workflow.FieldTypeString, Required: false},
		},
		States: []workflow.StateSpec{
			{Key: "draft", Name: "Draft", IsInitial: true},
			{Key: "posted", Name: "Posted", IsTerminal: true},
		},
		Transitions: []workflow.TransitionSpec{{
			ActionKey: "post", Name: "Post", FromStateKey: "draft", ToStateKey: "posted",
			Permission: workflow.PermissionSpec{Key: "workflow.expense_report.post"},
			Effects: []workflow.TransitionEffect{
				workflow.JournalEffect(workflow.JournalTemplate{
					Description: "Expense report",
					Lines: []workflow.LineTemplate{
						{AccountCode: "5000", Side: "debit", AmountField: "amount"},
						{AccountCode: "1000", Side: "credit", AmountField: "amount"},
					},
				}),
			},
		}},
	}
	defID, err := workflow.DefineWorkflowForFirm(ctx, appPool, firmID, ownerID, spec)
	if err != nil {
		t.Fatalf("DefineWorkflowForFirm: %v", err)
	}

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, ownerID, defID, map[string]any{
		"amount": 250.0, "cost_center_id": cc.ID.String(),
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := workflow.ExecuteTransition(ctx, appPool, firmID, ownerID, instanceID, "post", nil); err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}

	var debitCostCenter, creditCostCenter *uuid.UUID
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT jl.cost_center_id FROM journal_lines jl
			JOIN journal_entries je ON je.id = jl.entry_id
			WHERE je.firm_id = $1 AND jl.account_id = $2 AND jl.side = 'debit'
		`, firmID, expenseAccountID).Scan(&debitCostCenter); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			SELECT jl.cost_center_id FROM journal_lines jl
			JOIN journal_entries je ON je.id = jl.entry_id
			WHERE je.firm_id = $1 AND jl.account_id = $2 AND jl.side = 'credit'
		`, firmID, cashAccountID).Scan(&creditCostCenter)
	})
	if err != nil {
		t.Fatalf("look up posted journal lines: %v", err)
	}
	if debitCostCenter == nil || *debitCostCenter != cc.ID {
		t.Errorf("expected the debit line's cost_center_id to be %s, got %v", cc.ID, debitCostCenter)
	}
	if creditCostCenter == nil || *creditCostCenter != cc.ID {
		t.Errorf("expected the credit line's cost_center_id to be %s, got %v", cc.ID, creditCostCenter)
	}
}

// TestExecuteTransition_NoCostCenterIDLeavesJournalLinesUntagged confirms
// a payload with no cost_center_id field posts lines with cost_center_id
// NULL - the overwhelmingly common case, and every pre-existing bridged
// transition in this codebase, must keep working unchanged.
func TestExecuteTransition_NoCostCenterIDLeavesJournalLinesUntagged(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID := seedFirm(ctx, t, adminPool, "Firm A")
	ownerID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "no-cost-center-owner")

	var expenseAccountID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO accounts (firm_id, code, name, type) VALUES ($1, '5000', 'Expense', 'expense') RETURNING id
	`, firmID).Scan(&expenseAccountID); err != nil {
		t.Fatalf("seed expense account: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO accounts (firm_id, code, name, type) VALUES ($1, '1000', 'Cash', 'asset')
	`, firmID); err != nil {
		t.Fatalf("seed cash account: %v", err)
	}

	spec := workflow.DefinitionSpec{
		Key: "expense_report_2", Name: "Expense Report 2",
		CreatePermission: workflow.PermissionSpec{Key: "workflow.expense_report_2.create"},
		Fields:           []workflow.FieldSpec{{Name: "amount", Type: workflow.FieldTypeNumber, Required: true}},
		States: []workflow.StateSpec{
			{Key: "draft", Name: "Draft", IsInitial: true},
			{Key: "posted", Name: "Posted", IsTerminal: true},
		},
		Transitions: []workflow.TransitionSpec{{
			ActionKey: "post", Name: "Post", FromStateKey: "draft", ToStateKey: "posted",
			Permission: workflow.PermissionSpec{Key: "workflow.expense_report_2.post"},
			Effects: []workflow.TransitionEffect{
				workflow.JournalEffect(workflow.JournalTemplate{
					Description: "Expense report",
					Lines: []workflow.LineTemplate{
						{AccountCode: "5000", Side: "debit", AmountField: "amount"},
						{AccountCode: "1000", Side: "credit", AmountField: "amount"},
					},
				}),
			},
		}},
	}
	defID, err := workflow.DefineWorkflowForFirm(ctx, appPool, firmID, ownerID, spec)
	if err != nil {
		t.Fatalf("DefineWorkflowForFirm: %v", err)
	}
	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, ownerID, defID, map[string]any{"amount": 100.0})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, ownerID, instanceID, "post", nil); err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}

	var costCenterID *uuid.UUID
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT jl.cost_center_id FROM journal_lines jl
			JOIN journal_entries je ON je.id = jl.entry_id
			WHERE je.firm_id = $1 AND jl.account_id = $2
		`, firmID, expenseAccountID).Scan(&costCenterID)
	})
	if err != nil {
		t.Fatalf("look up posted journal line: %v", err)
	}
	if costCenterID != nil {
		t.Errorf("expected cost_center_id to be NULL when the payload carries none, got %v", *costCenterID)
	}
}
