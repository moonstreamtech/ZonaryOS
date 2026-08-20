// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package alerting

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestIsBreached_ZeroThresholdTriggersOnAnyPositiveValue is this batch's
// own required test: "a rule with threshold=0 triggers on any error" -
// isBreached is the exact comparison Checker uses for every metric, so
// this proves the "any error at all" reading directly rather than
// through a full end-to-end integration test.
func TestIsBreached_ZeroThresholdTriggersOnAnyPositiveValue(t *testing.T) {
	if isBreached(0, 0) {
		t.Error("expected a genuinely zero metric value to NOT breach a zero threshold")
	}
	if !isBreached(0.01, 0) {
		t.Error("expected any positive metric value (even 1%) to breach a zero threshold")
	}
}

// TestNotifyPlatformAdmins_LogsOneWarnLinePerAdminEmail is this batch's
// own required "confirmed creates ... platform-admin notification"
// test: since there is no real email/push delivery in this codebase yet
// (Open Points item 17, deliberately deferred - see notifyPlatformAdmins'
// own doc comment), the structured slog.Warn line IS the notification
// channel, so this test captures it directly via a temporary slog
// handler rather than needing a live mail server.
func TestNotifyPlatformAdmins_LogsOneWarnLinePerAdminEmail(t *testing.T) {
	var buf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	rule := AlertRule{ID: uuid.New(), Name: "High error rate", Metric: MetricErrorRate, Threshold: 0}
	event := AlertEvent{ID: uuid.New(), RuleID: rule.ID, TriggeredAt: time.Now(), MetricValue: 0.42}
	adminEmails := []string{"admin1@example.com", "admin2@example.com"}

	notifyPlatformAdmins(rule, event, adminEmails)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != len(adminEmails) {
		t.Fatalf("expected %d log lines (one per admin email), got %d: %q", len(adminEmails), len(lines), buf.String())
	}
	seen := map[string]bool{}
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("unmarshal log line: %v", err)
		}
		if entry["ruleName"] != rule.Name {
			t.Errorf("expected ruleName %q in log line, got %v", rule.Name, entry["ruleName"])
		}
		email, _ := entry["platformAdminEmail"].(string)
		seen[email] = true
	}
	for _, email := range adminEmails {
		if !seen[email] {
			t.Errorf("expected a log line addressed to %q, got none", email)
		}
	}
}
