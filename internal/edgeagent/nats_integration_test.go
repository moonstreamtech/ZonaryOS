// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package edgeagent_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"

	"github.com/moonstreamtech/ZonaryOS/internal/edgeagent"
)

// startTestNATS runs a real, short-lived, JetStream-enabled NATS server
// in-process (github.com/nats-io/nats-server/v2/test's own exported test
// helper, the same one that package's own test suite uses) - a real
// NATS server, not a mock, for the same reason this codebase's Postgres
// integration tests never mock RLS: JetStream stream/consumer semantics
// are exactly what this batch's own design is testing.
func startTestNATS(t *testing.T) string {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1 // OS-assigned free port - parallel test runs never collide.
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	srv := natstest.RunServer(&opts)
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	return srv.ClientURL()
}

// TestNewNATSBridge_EmptyURLIsDefaultOff is the batch's own required
// regression test: when ZONARYOS_NATS_URL is not set, NewNATSBridge
// returns (nil, nil) rather than an error, and RunSubscriber/PollOnce
// are no-ops on a nil bridge - the existing HTTP event path
// (TestRecordEvent_InsertsEventAndReturnsIt et al., already covered
// elsewhere in this package's test suite) is completely unaffected,
// since it never touches this file's code at all when NATS isn't
// configured.
func TestNewNATSBridge_EmptyURLIsDefaultOff(t *testing.T) {
	ctx := context.Background()
	bridge, err := edgeagent.NewNATSBridge(ctx, "")
	if err != nil {
		t.Fatalf("expected no error for an empty NATS URL, got %v", err)
	}
	if bridge != nil {
		t.Fatalf("expected a nil bridge for an empty NATS URL, got %+v", bridge)
	}

	// Both must be safe, immediate no-ops on a nil bridge - no panic, no
	// hang.
	bridge.Close()
	if err := edgeagent.PollOnce(ctx, bridge, nil); err != nil {
		t.Errorf("expected PollOnce on a nil bridge to be a no-op, got %v", err)
	}

	doneCtx, cancel := context.WithCancel(ctx)
	cancel()
	edgeagent.RunSubscriber(doneCtx, bridge, nil, time.Millisecond) // must return immediately, not hang.
}

// TestNATSBridge_PublishedEventIsConsumedAndRecorded is the batch's own
// required test for the "set" path: a message published to the stream
// (simulating an edge agent, which is a separate deployable not part of
// this repository - see this package's own doc comment) is consumed by
// PollOnce and ends up as a real edge_events row via the same
// RecordEvent the HTTP path uses.
func TestNATSBridge_PublishedEventIsConsumedAndRecorded(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "nats-owner")
	agent, err := edgeagent.CreateAgent(ctx, appPool, firmID, ownerID, edgeagent.CreateAgentInput{Name: "Gateway"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	token, _, err := edgeagent.IssueToken(ctx, appPool, firmID, ownerID, agent.ID, edgeagent.IssueTokenInput{Name: "Primary"})
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	natsURL := startTestNATS(t)
	bridge, err := edgeagent.NewNATSBridge(ctx, natsURL)
	if err != nil {
		t.Fatalf("NewNATSBridge: %v", err)
	}
	defer bridge.Close()

	// Publish directly with a plain client connection - simulating what
	// an edge agent process does: connect and publish, no server-side
	// code involved on the publishing side at all (see nats.go's own
	// doc comment: this bridge never publishes, only subscribes).
	pubConn, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect publisher: %v", err)
	}
	defer pubConn.Close()
	pubJS, err := pubConn.JetStream()
	if err != nil {
		t.Fatalf("publisher JetStream context: %v", err)
	}

	msg, err := json.Marshal(map[string]any{
		"token":     token,
		"eventType": "temperature_reading",
		"payload":   map[string]any{"tempC": 21.5},
	})
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	// "ZONARYOS.EDGE.EVENTS" mirrors nats.go's own unexported natsSubject
	// constant - this is an external black-box test (package
	// edgeagent_test), so it publishes to the real wire subject exactly
	// as an actual edge agent would, rather than reaching into the
	// package's internals.
	if _, err := pubJS.Publish("ZONARYOS.EDGE.EVENTS", msg); err != nil {
		t.Fatalf("publish edge event message: %v", err)
	}

	if err := edgeagent.PollOnce(ctx, bridge, appPool); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	events, err := edgeagent.ListEvents(ctx, appPool, firmID, ownerID, agent.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event recorded from the NATS-published message, got %d", len(events))
	}
	if events[0].EventType != "temperature_reading" {
		t.Errorf("expected eventType %q, got %q", "temperature_reading", events[0].EventType)
	}
	if tempC, ok := events[0].Payload["tempC"].(float64); !ok || tempC != 21.5 {
		t.Errorf("expected payload tempC 21.5, got %v", events[0].Payload["tempC"])
	}

	// A second PollOnce must not re-record the same, already-Ack'd
	// message.
	if err := edgeagent.PollOnce(ctx, bridge, appPool); err != nil {
		t.Fatalf("PollOnce (second): %v", err)
	}
	eventsAgain, err := edgeagent.ListEvents(ctx, appPool, firmID, ownerID, agent.ID)
	if err != nil {
		t.Fatalf("ListEvents (second): %v", err)
	}
	if len(eventsAgain) != 1 {
		t.Errorf("expected the message to be consumed exactly once (Ack'd), got %d events after a second poll", len(eventsAgain))
	}
}

// TestNATSBridge_InvalidTokenMessageIsDiscardedNotRedelivered confirms a
// message carrying a bad token is treated as a PERMANENT failure
// (isPermanentRecordEventError) - Ack'd and discarded on the first poll,
// not left pending for JetStream to keep redelivering forever. A
// well-formed, VALID follow-up message published afterward still gets
// recorded normally, proving the discarded message didn't wedge the
// consumer.
func TestNATSBridge_InvalidTokenMessageIsDiscardedNotRedelivered(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "nats-bad-token-owner")
	agent, err := edgeagent.CreateAgent(ctx, appPool, firmID, ownerID, edgeagent.CreateAgentInput{Name: "Gateway"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	token, _, err := edgeagent.IssueToken(ctx, appPool, firmID, ownerID, agent.ID, edgeagent.IssueTokenInput{Name: "Primary"})
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	natsURL := startTestNATS(t)
	bridge, err := edgeagent.NewNATSBridge(ctx, natsURL)
	if err != nil {
		t.Fatalf("NewNATSBridge: %v", err)
	}
	defer bridge.Close()

	pubConn, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect publisher: %v", err)
	}
	defer pubConn.Close()
	pubJS, err := pubConn.JetStream()
	if err != nil {
		t.Fatalf("publisher JetStream context: %v", err)
	}

	badMsg, err := json.Marshal(map[string]any{"token": "eat_not-a-real-token", "eventType": "bogus", "payload": map[string]any{}})
	if err != nil {
		t.Fatalf("marshal bad message: %v", err)
	}
	if _, err := pubJS.Publish("ZONARYOS.EDGE.EVENTS", badMsg); err != nil {
		t.Fatalf("publish bad message: %v", err)
	}
	goodMsg, err := json.Marshal(map[string]any{"token": token, "eventType": "door_opened", "payload": map[string]any{}})
	if err != nil {
		t.Fatalf("marshal good message: %v", err)
	}
	if _, err := pubJS.Publish("ZONARYOS.EDGE.EVENTS", goodMsg); err != nil {
		t.Fatalf("publish good message: %v", err)
	}

	if err := edgeagent.PollOnce(ctx, bridge, appPool); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	events, err := edgeagent.ListEvents(ctx, appPool, firmID, ownerID, agent.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 recorded event (the bad-token message discarded, not recorded), got %d", len(events))
	}
	if events[0].EventType != "door_opened" {
		t.Errorf("expected the recorded event to be the valid one, got eventType %q", events[0].EventType)
	}
}
