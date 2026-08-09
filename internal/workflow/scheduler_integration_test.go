// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// These tests exercise the schedule-trigger's background scheduler (Open
// Points item 7's partial schedule-trigger support, scheduler.go).
// setupTest is shared with workflow_integration_test.go (same package).

func scheduleIntervalPtr(s string) *string { return &s }

func TestProcessDueScheduledRuns_MarksRanAndReschedules(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Scheduler A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	rule := workflow.Rule{
		FirmID:        firmID,
		DefinitionKey: workflow.StockToSaleKey,
		Name:          "Daily reminder",
		Trigger:       workflow.TriggerSchedule,
		Actions: []workflow.Action{
			{Type: workflow.ActionNotify, Channel: workflow.NotifyChannelAuditLog, MessageTemplate: "tick"},
		},
		Enabled:          true,
		ScheduleInterval: scheduleIntervalPtr("1s"),
	}
	ruleID, err := workflow.CreateRule(ctx, appPool, rule)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	// CreateRule seeds exactly one initial scheduled_rule_runs row,
	// scheduled roughly one interval out - back-date it into the past so
	// this test doesn't need to wait for real time to pass.
	var runID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		SELECT id FROM scheduled_rule_runs WHERE rule_id = $1
	`, ruleID).Scan(&runID); err != nil {
		t.Fatalf("expected CreateRule to have seeded an initial scheduled run: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `UPDATE scheduled_rule_runs SET scheduled_for = now() - interval '1 minute' WHERE id = $1`, runID); err != nil {
		t.Fatalf("backdate scheduled_for: %v", err)
	}

	workflow.ProcessDueScheduledRuns(ctx, appPool)

	var status string
	var ranAt *time.Time
	if err := adminPool.QueryRow(ctx, `SELECT status, ran_at FROM scheduled_rule_runs WHERE id = $1`, runID).Scan(&status, &ranAt); err != nil {
		t.Fatalf("query run status: %v", err)
	}
	if status != "ran" {
		t.Fatalf("expected the due run to be marked 'ran', got %q", status)
	}
	if ranAt == nil {
		t.Fatal("expected ran_at to be set")
	}

	// Since the rule has actions and a ScheduleInterval, a NEW run should
	// have been scheduled - the original run plus this one is 2 total.
	var totalRuns int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM scheduled_rule_runs WHERE rule_id = $1`, ruleID).Scan(&totalRuns); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if totalRuns != 2 {
		t.Fatalf("expected 2 scheduled_rule_runs rows (original + rescheduled), got %d", totalRuns)
	}

	var nextStatus string
	var nextScheduledFor time.Time
	if err := adminPool.QueryRow(ctx, `
		SELECT status, scheduled_for FROM scheduled_rule_runs WHERE rule_id = $1 AND id != $2
	`, ruleID, runID).Scan(&nextStatus, &nextScheduledFor); err != nil {
		t.Fatalf("query rescheduled run: %v", err)
	}
	if nextStatus != "pending" {
		t.Fatalf("expected the rescheduled run to be pending, got %q", nextStatus)
	}
	if nextScheduledFor.Before(time.Now()) {
		t.Fatalf("expected the rescheduled run's scheduled_for to be in the future, got %s", nextScheduledFor)
	}
}

func TestProcessDueScheduledRuns_DisabledRuleFailsWithoutRescheduling(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Scheduler B') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	rule := workflow.Rule{
		FirmID:        firmID,
		DefinitionKey: workflow.StockToSaleKey,
		Name:          "Disabled reminder",
		Trigger:       workflow.TriggerSchedule,
		Actions: []workflow.Action{
			{Type: workflow.ActionNotify, Channel: workflow.NotifyChannelAuditLog, MessageTemplate: "tick"},
		},
		Enabled:          false,
		ScheduleInterval: scheduleIntervalPtr("1s"),
	}
	ruleID, err := workflow.CreateRule(ctx, appPool, rule)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	var runID uuid.UUID
	if err := adminPool.QueryRow(ctx, `SELECT id FROM scheduled_rule_runs WHERE rule_id = $1`, ruleID).Scan(&runID); err != nil {
		t.Fatalf("expected initial scheduled run: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `UPDATE scheduled_rule_runs SET scheduled_for = now() - interval '1 minute' WHERE id = $1`, runID); err != nil {
		t.Fatalf("backdate scheduled_for: %v", err)
	}

	workflow.ProcessDueScheduledRuns(ctx, appPool)

	var status string
	var errorText *string
	if err := adminPool.QueryRow(ctx, `SELECT status, error_text FROM scheduled_rule_runs WHERE id = $1`, runID).Scan(&status, &errorText); err != nil {
		t.Fatalf("query run status: %v", err)
	}
	if status != "failed" {
		t.Fatalf("expected a disabled rule's due run to be marked 'failed', got %q", status)
	}
	if errorText == nil || *errorText == "" {
		t.Fatal("expected a non-empty error_text explaining the failure")
	}

	var totalRuns int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM scheduled_rule_runs WHERE rule_id = $1`, ruleID).Scan(&totalRuns); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if totalRuns != 1 {
		t.Fatalf("expected no reschedule after a failure, got %d total runs", totalRuns)
	}
}

// TestRunScheduler_RealBackgroundGoroutineFiresOnDueRun confirms the
// actual background goroutine (not just ProcessDueScheduledRuns called
// directly) picks up a due run in real time - RunScheduler is given a
// short (100ms) poll interval rather than production's
// SchedulerPollInterval, so this observes a real firing without waiting
// on real-world minutes.
func TestRunScheduler_RealBackgroundGoroutineFiresOnDueRun(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Scheduler C') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	rule := workflow.Rule{
		FirmID:        firmID,
		DefinitionKey: workflow.StockToSaleKey,
		Name:          "Real-time reminder",
		Trigger:       workflow.TriggerSchedule,
		Actions: []workflow.Action{
			{Type: workflow.ActionNotify, Channel: workflow.NotifyChannelAuditLog, MessageTemplate: "tick"},
		},
		Enabled:          true,
		ScheduleInterval: scheduleIntervalPtr("1s"),
	}
	ruleID, err := workflow.CreateRule(ctx, appPool, rule)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	var runID uuid.UUID
	if err := adminPool.QueryRow(ctx, `SELECT id FROM scheduled_rule_runs WHERE rule_id = $1`, ruleID).Scan(&runID); err != nil {
		t.Fatalf("expected initial scheduled run: %v", err)
	}
	// Already due the instant the scheduler's first tick fires.
	if _, err := adminPool.Exec(ctx, `UPDATE scheduled_rule_runs SET scheduled_for = now() WHERE id = $1`, runID); err != nil {
		t.Fatalf("backdate scheduled_for: %v", err)
	}

	schedulerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go workflow.RunScheduler(schedulerCtx, appPool, 100*time.Millisecond)

	deadline := time.Now().Add(3 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		if err := adminPool.QueryRow(ctx, `SELECT status FROM scheduled_rule_runs WHERE id = $1`, runID).Scan(&status); err != nil {
			t.Fatalf("query run status: %v", err)
		}
		if status == "ran" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status != "ran" {
		t.Fatalf("expected the real background scheduler to mark the due run 'ran' within 3s, last observed status %q", status)
	}
}
