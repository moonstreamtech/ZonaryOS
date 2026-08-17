// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Part 1 of the NATS JetStream/Edge Agent completion batch (Vision §9):
// an OPTIONAL message-queue-backed ingestion path for edge events,
// alongside (not instead of) the existing direct-HTTP-POST path
// (RecordEvent, events.go). Default-off, the same pattern
// internal/license.Verifier and internal/telemetry.Reporter already
// establish: NATSBridge is nil unless ZONARYOS_NATS_URL is set
// (internal/platform/config.Config.NATSURL), and every call site that
// might use it checks for nil first - when nil, this file's own code
// never runs at all, and the system behaves exactly as it did before
// this batch existed.
//
// Scope boundary (this batch's own): NATS as an external, optional
// service only - no embedded NATS server in the main ZonaryOS binary.
// The actual Edge Agent process (a separate deployable, not part of this
// repository - see this package's own doc comment) is assumed to run its
// own local JetStream (or a leaf-node connection to a shared broker) and
// publish there; what this file provides is the SERVER's own client:
// connect, ensure the stream it expects exists, and run a durable PULL
// consumer that hands each message to the exact same RecordEvent this
// package's HTTP path already uses - no duplicated business logic, no
// duplicated authorization. This bridge never publishes anything itself
// (only edge agents publish; the server only ever subscribes) - if a
// future batch needs the server to push TO an agent over NATS too, that
// is a different, not-yet-built capability (poll-based commands,
// commands.go, cover that direction for now - this batch's own scope
// boundary explicitly defers WebSocket/any other real-time push).
package edgeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

// natsStreamName is the JetStream stream the server ensures exists and
// consumes from. NATS stream names may not contain "." (nats.go's own
// checkStreamName rejects it) - this batch's own design brief names the
// stream "ZONARYOS.EDGE.EVENTS", which is a valid SUBJECT (dots are the
// normal subject-hierarchy separator) but not a valid STREAM name, so
// the stream itself is named with underscores instead; the subject the
// stream captures (and the one an edge agent actually publishes to) is
// the literal dotted name from the design brief.
const (
	natsStreamName          = "ZONARYOS_EDGE_EVENTS"
	natsSubject             = "ZONARYOS.EDGE.EVENTS"
	natsDurableConsumerName = "zonaryos-edge-events-server"
)

// NATSBridge holds the server's JetStream connection - conn/js/stream
// exactly as this batch's own design brief specifies. Deliberately not
// exported field-by-field: everything a caller needs is behind
// NewNATSBridge/RunSubscriber/Close.
type NATSBridge struct {
	conn   *nats.Conn
	js     nats.JetStreamContext
	stream string
}

// natsEventMessage is the self-contained wire shape an edge agent
// publishes to natsSubject - NATS carries no HTTP headers, so (unlike
// the HTTP path, where the bearer token lives in the Authorization
// header) the token has to travel INSIDE the message body itself.
// Consuming this message and handing rawToken/EventType/Payload
// straight to the existing RecordEvent means the exact same
// authentication and business logic runs regardless of which transport
// delivered the event - no parallel, potentially-diverging
// implementation.
type natsEventMessage struct {
	Token     string         `json:"token"`
	EventType string         `json:"eventType"`
	Payload   map[string]any `json:"payload"`
}

// NewNATSBridge connects to natsURL and ensures natsStreamName exists
// (file storage, not memory - events must survive server restarts, per
// this batch's own design brief - and a 7-day max age, so a
// long-offline server doesn't accumulate an unbounded backlog forever).
// Returns (nil, nil), not an error, when natsURL is empty - the
// default-off contract every call site relies on (see this file's own
// doc comment); callers should treat a nil, nil-error return as "NATS is
// simply not configured", not as a fallback-needed condition.
func NewNATSBridge(ctx context.Context, natsURL string) (*NATSBridge, error) {
	if natsURL == "" {
		return nil, nil
	}

	conn, err := nats.Connect(natsURL, nats.Name("zonaryos-server"))
	if err != nil {
		return nil, fmt.Errorf("connect to NATS at %q: %w", natsURL, err)
	}
	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open JetStream context: %w", err)
	}

	// AddStream is idempotent-in-effect for this bridge's own purposes:
	// if the stream already exists with an incompatible config, NATS
	// returns an error here rather than silently reconciling it - this
	// bridge does not attempt to update an existing stream's config, on
	// the same "don't silently rewrite infrastructure another operator
	// may have deliberately configured" reasoning this codebase applies
	// elsewhere (e.g. accounts' code/type being immutable once created).
	_, err = js.AddStream(&nats.StreamConfig{
		Name:      natsStreamName,
		Subjects:  []string{natsSubject},
		Storage:   nats.FileStorage,
		MaxAge:    7 * 24 * time.Hour,
		Retention: nats.LimitsPolicy,
	})
	if err != nil && err != nats.ErrStreamNameAlreadyInUse {
		conn.Close()
		return nil, fmt.Errorf("ensure JetStream stream %q: %w", natsStreamName, err)
	}

	return &NATSBridge{conn: conn, js: js, stream: natsStreamName}, nil
}

// Close releases the underlying NATS connection - a no-op (safe to call)
// on a nil *NATSBridge, mirroring every other default-off type in this
// codebase's own nil-safety convention (e.g. internal/license.Verifier's
// methods).
func (b *NATSBridge) Close() {
	if b == nil || b.conn == nil {
		return
	}
	b.conn.Close()
}

// RunSubscriber runs the server's durable, pull-based JetStream consumer
// until ctx is cancelled, polling every pollInterval (this batch's own
// design brief: "durable, pull-based (not push), the server polls every
// N seconds"). A pull consumer, not a push one, was chosen because it
// gives the server explicit control over how many messages it fetches
// per poll (Fetch's own batch size below) rather than JetStream pushing
// an unbounded stream at whatever rate the server can or can't keep up
// with - the same backpressure-friendly reasoning a durable pull
// consumer is generally preferred for a batch-style consumer over a
// continuously-open push subscription.
//
// Each fetched message is unmarshalled into natsEventMessage and handed
// to the ordinary RecordEvent - the identical function call the HTTP
// path (handleRecordEvent) makes, so a message published over NATS gets
// exactly the same authentication (the token embedded in the message),
// validation, edge_events insert, and on_edge_event rule-trigger wiring
// (RecordEvent's own doc comment) as an HTTP-delivered event. A message
// is Ack'd only after RecordEvent succeeds - a transient failure (e.g. a
// momentary Postgres outage) leaves the message unacked, so JetStream
// redelivers it on a later poll rather than silently dropping it; a
// permanent failure (e.g. ErrInvalidToken, a malformed message body) is
// still Ack'd, since retrying an unfixable message forever would only
// ever produce the same error - it is logged instead, the same
// "swallow, log, keep the subscriber loop alive" posture
// internal/notification's own best-effort delivery already establishes.
func RunSubscriber(ctx context.Context, bridge *NATSBridge, pool *pgxpool.Pool, pollInterval time.Duration) {
	if bridge == nil {
		return
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := PollOnce(ctx, bridge, pool); err != nil {
				slog.Error("NATS: poll failed", "err", err)
			}
		}
	}
}

// PollOnce binds to (creating if necessary) the durable pull consumer
// and processes exactly one Fetch batch - RunSubscriber's own loop calls
// this every tick; it is also exported as this file's test hook (see
// nats_integration_test.go), and is directly usable by an operator/ops
// script wanting a one-shot drain without running the full ticker loop.
// A no-op on a nil bridge, mirroring Close's own nil-safety.
func PollOnce(ctx context.Context, bridge *NATSBridge, pool *pgxpool.Pool) error {
	if bridge == nil {
		return nil
	}
	sub, err := bridge.js.PullSubscribe(natsSubject, natsDurableConsumerName, nats.ManualAck(), nats.AckExplicit())
	if err != nil {
		return fmt.Errorf("create durable pull consumer: %w", err)
	}
	processPulledMessages(ctx, sub, pool)
	return nil
}

// processPulledMessages fetches one batch and processes each message -
// split out from RunSubscriber's own loop for testability (a test can
// call this directly against a real, short-lived NATS server without
// waiting on a ticker).
func processPulledMessages(ctx context.Context, sub *nats.Subscription, pool *pgxpool.Pool) {
	msgs, err := sub.Fetch(10, nats.MaxWait(2*time.Second))
	if err != nil {
		// nats.ErrTimeout just means "no messages available right now" -
		// the overwhelmingly common case on an idle stream, not a real
		// error worth logging every poll interval.
		if err != nats.ErrTimeout {
			slog.Warn("NATS: fetch from durable consumer failed", "err", err)
		}
		return
	}

	for _, msg := range msgs {
		var m natsEventMessage
		if err := json.Unmarshal(msg.Data, &m); err != nil {
			slog.Error("NATS: malformed edge event message, discarding", "err", err)
			_ = msg.Ack()
			continue
		}

		if _, err := RecordEvent(ctx, pool, m.Token, m.EventType, m.Payload); err != nil {
			if isPermanentRecordEventError(err) {
				slog.Error("NATS: queued edge event rejected, discarding", "eventType", m.EventType, "err", err)
				_ = msg.Ack()
			} else {
				slog.Warn("NATS: RecordEvent failed for queued edge event, will retry", "eventType", m.EventType, "err", err)
				_ = msg.Nak()
			}
			continue
		}
		_ = msg.Ack()
	}
}

// isPermanentRecordEventError reports whether err is one of RecordEvent's
// own validation/authorization sentinels - a condition retrying will
// never fix (a bad token, an empty eventType, a deleted agent) - as
// opposed to an infrastructure error (a transient Postgres failure) that
// a later redelivery might succeed at.
func isPermanentRecordEventError(err error) bool {
	return errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrInvalidAgent) || errors.Is(err, ErrAgentNotFound)
}
