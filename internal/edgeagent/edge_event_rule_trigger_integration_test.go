// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package edgeagent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/moonstreamtech/ZonaryOS/internal/edgeagent"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// captureSlog temporarily replaces slog's default logger with one that
// writes structured JSON lines into buf, restoring the original on
// cleanup - EvaluateEdgeEventRules' matched-rule "fired" signal is a
// slog.Info line (see that function's own doc comment for why: no real
// ZonaryOS user is in scope for an automated edge-event match, so unlike
// every human/API-triggered rule match, there's no audit_log row to
// query instead). This is the only observable side effect a test can
// assert against for that code path, the same "assert on the log line"
// pattern internal/workflow's own TestProcessDueScheduledRuns_MarksRanAndReschedules
// test uses for the identical scheduler.go notify-only shape.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	return &buf
}

// TestRecordEvent_FiresMatchingOnEdgeEventRule confirms Part 3's wiring
// end to end: an enabled on_edge_event rule whose condition tree matches
// the incoming event's payload fires (logs) when RecordEvent is called
// with a matching event_type/payload, and does NOT fire for a
// non-matching payload.
func TestRecordEvent_FiresMatchingOnEdgeEventRule(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "edge-rule-owner")
	agent, err := edgeagent.CreateAgent(ctx, appPool, firmID, ownerID, edgeagent.CreateAgentInput{Name: "Sensor Gateway"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	token, _, err := edgeagent.IssueToken(ctx, appPool, firmID, ownerID, agent.ID, edgeagent.IssueTokenInput{Name: "Primary"})
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	// A minimal workflow definition purely to give the rule somewhere to
	// be filed under - workflow_rules.definition_key stays NOT NULL
	// regardless of trigger type (see
	// workflow.listEnabledEdgeEventRulesTx's own doc comment), even
	// though on_edge_event dispatch itself ignores it.
	spec := workflow.DefinitionSpec{
		Key: "sensor_watch", Name: "Sensor Watch",
		CreatePermission: workflow.PermissionSpec{Key: "workflow.sensor_watch.create"},
		States:           []workflow.StateSpec{{Key: "active", Name: "Active", IsInitial: true}},
	}
	defID, err := workflow.DefineWorkflowForFirm(ctx, appPool, firmID, ownerID, spec)
	if err != nil {
		t.Fatalf("DefineWorkflowForFirm: %v", err)
	}
	_ = defID

	rule := workflow.Rule{
		Name:    "High temperature alert",
		Trigger: workflow.TriggerOnEdgeEvent,
		ConditionTree: workflow.ExpressionNode{
			Type: workflow.ConditionField, Field: "tempC", Op: string(workflow.OpGt), Value: 30.0,
		},
		Actions: []workflow.Action{{
			Type: workflow.ActionNotify, Channel: workflow.NotifyChannelAuditLog, MessageTemplate: "High temp: {{tempC}}",
		}},
		Autonomous: true,
		Enabled:    true,
	}
	if _, err := workflow.CreateRuleForFirm(ctx, appPool, firmID, ownerID, "sensor_watch", rule); err != nil {
		t.Fatalf("CreateRuleForFirm: %v", err)
	}

	buf := captureSlog(t)

	// Non-matching payload: must NOT fire.
	if _, err := edgeagent.RecordEvent(ctx, appPool, token, "temperature_reading", map[string]any{"tempC": 22.0}); err != nil {
		t.Fatalf("RecordEvent (below threshold): %v", err)
	}
	if strings.Contains(buf.String(), "edge event rule fired") {
		t.Errorf("expected the rule NOT to fire for a below-threshold reading, but it did: %s", buf.String())
	}

	// Matching payload: must fire.
	if _, err := edgeagent.RecordEvent(ctx, appPool, token, "temperature_reading", map[string]any{"tempC": 38.5}); err != nil {
		t.Fatalf("RecordEvent (above threshold): %v", err)
	}

	var fired bool
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["msg"] == "edge event rule fired" && entry["ruleName"] == "High temperature alert" && entry["eventType"] == "temperature_reading" {
			fired = true
			if msg, _ := entry["message"].(string); msg != "High temp: 38.5" {
				t.Errorf("expected the rendered notify message %q, got %q", "High temp: 38.5", msg)
			}
		}
	}
	if !fired {
		t.Errorf("expected the on_edge_event rule to fire for an above-threshold reading, log output: %s", buf.String())
	}
}
