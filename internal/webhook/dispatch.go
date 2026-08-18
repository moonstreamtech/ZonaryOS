// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// SignatureHeader is the HTTP header a receiving endpoint reads to
// verify a delivery really came from ZonaryOS - HMAC-SHA256(secret,
// body), hex-encoded (the same "recompute over the raw body bytes,
// constant-time-compare against this header" verification shape GitHub/
// Stripe webhook consumers already expect, so an integrator's existing
// verification code needs only the header name changed).
const SignatureHeader = "X-ZonaryOS-Signature"

// deliveryTimeout bounds a single outbound HTTP attempt - a webhook
// receiver that never responds must not hold this delivery's goroutine
// (and, via the one retry, doubly so) open indefinitely.
const deliveryTimeout = 10 * time.Second

// maxRecordedResponseBody caps how much of a receiver's response body
// webhook_deliveries.response_body stores - enough for a meaningful error
// message, not an unbounded amount of a misbehaving/malicious endpoint's
// output.
const maxRecordedResponseBody = 4096

func generateSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Dispatch is the one call site every event-emitting handler makes,
// right after its own primary operation has committed (see this
// package's own doc comment on why - webhook_deliveries and every
// intermediate write, e.g. rolling back the workflow instance it was
// about to describe, must never end up recorded for an operation that
// didn't actually happen). Dispatch itself runs synchronously (a single
// fast, indexed lookup: which of firmID's active webhooks subscribe to
// eventType), so it adds negligible latency to the caller's own request;
// each MATCHED webhook's actual outbound HTTP delivery then runs in its
// own goroutine (deliverOne), which is what "fire-and-forget, not
// blocking the HTTP response" actually refers to. A lookup failure (a
// transient DB error) is logged, not returned - a webhook dispatch
// failure must never fail the operation that triggered it; this is
// notification, not a required side effect of the request.
//
// ciaudit:ignore-firmid-check: internal, non-caller-facing dispatch entry
// point - same reasoning as activeWebhooksForEvent's own doc comment
// below (which this delegates the actual query to); the caller who
// triggered the underlying event has already been authorized for THAT by
// its own handler before Dispatch is ever called.
func Dispatch(pool *pgxpool.Pool, firmID uuid.UUID, eventType Event, payload map[string]any) {
	ctx := context.Background()
	hooks, err := activeWebhooksForEvent(ctx, pool, firmID, eventType)
	if err != nil {
		slog.Warn("webhook dispatch: list active webhooks failed", "firmId", firmID, "event", eventType, "err", err)
		return
	}
	for _, hook := range hooks {
		go deliverOne(pool, hook, eventType, payload)
	}
}

// activeWebhooksForEvent reads firmID's own active, eventType-subscribed
// webhooks - not gated by permission.IsMember/Has: this is an internal
// system read triggered by an already-authorized operation completing
// (the caller who triggered the underlying event was already authorized
// for THAT), not a caller-facing endpoint with its own principal to
// check; there is no "user" here to check membership for. Uses
// db.WithFirmContext purely for its RLS session-scoping side effect, the
// same reasoning internal/workflow.EvaluateRules' own post-commit,
// system-triggered reads rely on.
//
// ciaudit:ignore-firmid-check: internal, non-caller-facing dispatch
// lookup - see this function's own doc comment for why no
// permission.IsMember/Has check applies here.
func activeWebhooksForEvent(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID, eventType Event) ([]Webhook, error) {
	var hooks []Webhook
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT `+webhookColumns+` FROM webhooks
			WHERE firm_id = $1 AND is_active AND events @> ARRAY[$2]::text[]
		`, firmID, string(eventType))
		if err != nil {
			return fmt.Errorf("list active webhooks for event: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			w, err := scanWebhook(rows)
			if err != nil {
				return fmt.Errorf("scan webhook: %w", err)
			}
			hooks = append(hooks, w)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return hooks, nil
}

// deliverOne sends one HMAC-signed POST to hook.URL, retrying exactly
// once (immediately, no backoff - see this package's own doc comment on
// why) on failure, and records the outcome of whichever attempt actually
// counts (the retry's, if one happened) as a single webhook_deliveries
// row - the schema has no attempt-number column, so "record each
// delivery attempt" here means "record this delivery's own final
// outcome," not one row per HTTP call.
func deliverOne(pool *pgxpool.Pool, hook Webhook, eventType Event, payload map[string]any) {
	ctx := context.Background()

	body, err := json.Marshal(map[string]any{
		"event":     string(eventType),
		"firmId":    hook.FirmID.String(),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"data":      payload,
	})
	if err != nil {
		slog.Warn("webhook delivery: marshal payload failed", "webhookId", hook.ID, "event", eventType, "err", err)
		return
	}
	signature := sign(hook.Secret, body)

	status, respBody, sendErr := send(ctx, hook.URL, body, signature)
	success := sendErr == nil && status >= 200 && status < 300
	if !success {
		// The single immediate retry this batch's own scope boundary
		// documents - not exponential backoff (needs a persistent job
		// queue, real future work).
		status, respBody, sendErr = send(ctx, hook.URL, body, signature)
		success = sendErr == nil && status >= 200 && status < 300
	}

	var statusPtr *int
	if sendErr == nil {
		s := status
		statusPtr = &s
	}
	responseBody := respBody
	if sendErr != nil {
		responseBody = sendErr.Error()
	}
	if len(responseBody) > maxRecordedResponseBody {
		responseBody = responseBody[:maxRecordedResponseBody]
	}

	if err := recordDelivery(ctx, pool, hook, eventType, body, statusPtr, responseBody, success); err != nil {
		slog.Warn("webhook delivery: record failed", "webhookId", hook.ID, "event", eventType, "err", err)
	}
}

// SendSigned POSTs body to url with an HMAC-SHA256(secret, body)
// signature header, retrying exactly once immediately on failure - the
// exact delivery mechanics deliverOne uses for a firm's own subscribed
// webhooks (sign+send, both otherwise unexported), exported here so
// another package that needs to push to an arbitrary URL (
// internal/integration's own outbound connector push, data pipeline/ETL/
// system integrations batch) reuses this instead of building a second
// HTTP client - the batch's own explicit "use the existing webhook
// delivery infrastructure... don't build a new HTTP client" instruction.
// Unlike deliverOne, this does not itself write any webhook_deliveries
// row - the caller (internal/integration) records its own outcome into
// its own integration_sync_logs table instead, since a connector push
// isn't a webhooks-table row at all.
func SendSigned(ctx context.Context, url, secret string, body []byte) (status int, respBody string, err error) {
	signature := sign(secret, body)
	status, respBody, err = send(ctx, url, body, signature)
	success := err == nil && status >= 200 && status < 300
	if !success {
		status, respBody, err = send(ctx, url, body, signature)
	}
	return status, respBody, err
}

// send performs one outbound POST, returning the response status/body on
// a completed HTTP round trip, or a non-nil error if the request itself
// never got a response at all (DNS failure, connection refused, timeout).
func send(ctx context.Context, url string, body []byte, signature string) (status int, respBody string, err error) {
	ctx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, signature)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxRecordedResponseBody))
	return resp.StatusCode, string(respBytes), nil
}

// recordDelivery inserts webhookDeliveries' own outcome row and updates
// webhooks.last_triggered_at - a fresh db.WithFirmContext transaction of
// its own (deliverOne runs in a background goroutine outlasting the
// original request, so it can't reuse any transaction from the handler
// that called Dispatch).
func recordDelivery(ctx context.Context, pool *pgxpool.Pool, hook Webhook, eventType Event, payload []byte, status *int, responseBody string, success bool) error {
	return zdb.WithFirmContext(ctx, pool, hook.FirmID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO webhook_deliveries (firm_id, webhook_id, event_type, payload, response_status, response_body, success)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, hook.FirmID, hook.ID, string(eventType), payload, status, responseBody, success); err != nil {
			return fmt.Errorf("insert webhook delivery: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE webhooks SET last_triggered_at = now() WHERE id = $1`, hook.ID); err != nil {
			return fmt.Errorf("touch webhook last_triggered_at: %w", err)
		}
		return nil
	})
}
