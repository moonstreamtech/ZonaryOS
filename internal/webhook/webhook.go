// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package webhook is the notification half of the webhooks/API-keys/
// external-integration batch: a firm owner registers an HTTP endpoint
// plus a subset of Event... event types, and ZonaryOS calls that
// endpoint (HMAC-signed, so the receiver can verify authenticity)
// whenever a matching event fires elsewhere in the system.
//
// secret is stored PLAINTEXT (unlike internal/apikey.APIKey's key_hash or
// internal/edgeagent's token_hash) - a deliberate asymmetry: an API
// key/edge-agent token is a bearer credential ZonaryOS itself
// authenticates a caller against (so only a hash needs to ever exist
// server-side, the same "never store what you don't need to compare
// against" principle every credential in this codebase follows), while a
// webhook secret is an HMAC signing key the FIRM OWNER needs to read back
// at any time to configure their own receiving endpoint's signature
// verification - there is no equivalent "prove you are who you say you
// are" moment for a webhook secret the way there is for a bearer token,
// so hashing it would only make it useless for its actual purpose.
//
// Dispatch is fire-and-forget: Dispatch itself runs synchronously (one
// fast, indexed query - "which of this firm's active webhooks subscribe
// to this event"), but the actual outbound HTTP delivery to each matched
// webhook happens in its own goroutine, so a slow or unreachable
// receiving endpoint never adds latency to the request that triggered
// the event. Retry is a single immediate retry on failure, not
// exponential backoff - a real backoff/retry schedule needs a persistent
// job queue (survives a process restart between attempts), which is
// explicitly out of scope for this batch; see this package's own
// "Scope boundaries" note in docs/DEVELOPMENT.md.
package webhook

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/onboarding"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - webhook management is owner-gated.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's webhooks")

	// ErrWebhookNotFound means no webhooks row with the given id is
	// visible in the caller's firm context.
	ErrWebhookNotFound = errors.New("webhook not found")

	// ErrInvalidWebhook means Create/UpdateWebhook was given a
	// structurally invalid request (empty name/URL, or an event not in
	// the closed vocabulary this package supports - see AllEvents).
	ErrInvalidWebhook = errors.New("invalid webhook")
)

const auditEntityType = "webhook"

const (
	createAuditAction = "create"
	updateAuditAction = "update"
	deleteAuditAction = "delete"
)

// Event is this package's closed, hardcoded vocabulary of event types a
// webhook can subscribe to - deliberately not an open string a caller
// could subscribe to anything with: only a real, dispatched event type
// (see dispatch.go) can ever actually fire, so a webhook subscribed to a
// typo'd or made-up event simply never triggers, silently. Rejecting an
// unrecognized event at Create/UpdateWebhook time (see validEvents)
// catches that mistake immediately instead.
type Event string

const (
	EventWorkflowInstanceCreated    Event = "workflow.instance.created"
	EventWorkflowTransitionExecuted Event = "workflow.transition.executed"
	EventInvoiceCreated             Event = "invoice.created"
	EventInvoicePaid                Event = "invoice.paid"
	EventApprovalPending            Event = "approval.pending"
	// EventSalesOrderCreated/EventPurchaseOrderCreated (sales orders +
	// procurement cycle batch): dispatched by internal/salesorders.CreateSalesOrder
	// / internal/procurement.CreatePurchaseOrder respectively - the direct,
	// owner-gated creation path only, same "never dispatched from inside
	// the workflow bridge's still-open transaction" reasoning
	// EventInvoiceCreated's own dispatch site documents.
	EventSalesOrderCreated    Event = "sales_order.created"
	EventPurchaseOrderCreated Event = "purchase_order.created"
	// EventProductCreated/EventProductUpdated/EventCustomerCreated (data
	// pipeline/ETL/system integrations batch): dispatched by
	// internal/inventory.CreateProduct/UpdateProduct and
	// internal/crm.CreateCustomer respectively - the same "direct,
	// owner-gated mutation path only" reasoning EventInvoiceCreated's own
	// dispatch site documents. Added specifically so
	// internal/integration.DispatchOutbound (a second, independent call
	// right next to each of these Dispatch calls - see that function's
	// own doc comment) has a real event to react to for its own
	// field-mapped entity-sync push, distinct from this generic,
	// event-subscription-based webhooks system.
	EventProductCreated  Event = "product.created"
	EventProductUpdated  Event = "product.updated"
	EventCustomerCreated Event = "customer.created"
)

// AllEvents is every event type this batch supports - the create/edit
// form's own event checklist, and Create/UpdateWebhook's own validation
// allow-list.
var AllEvents = []Event{
	EventWorkflowInstanceCreated,
	EventWorkflowTransitionExecuted,
	EventInvoiceCreated,
	EventInvoicePaid,
	EventApprovalPending,
	EventSalesOrderCreated,
	EventPurchaseOrderCreated,
	EventProductCreated,
	EventProductUpdated,
	EventCustomerCreated,
}

func validEvent(e string) bool {
	for _, known := range AllEvents {
		if string(known) == e {
			return true
		}
	}
	return false
}

// Webhook is one webhooks row.
type Webhook struct {
	ID              uuid.UUID
	FirmID          uuid.UUID
	Name            string
	URL             string
	Events          []string
	Secret          string
	IsActive        bool
	LastTriggeredAt *time.Time
	CreatedAt       time.Time
}

const webhookColumns = "id, firm_id, name, url, events, secret, is_active, last_triggered_at, created_at"

func scanWebhook(row pgx.Row) (Webhook, error) {
	var w Webhook
	if err := row.Scan(&w.ID, &w.FirmID, &w.Name, &w.URL, &w.Events, &w.Secret, &w.IsActive, &w.LastTriggeredAt, &w.CreatedAt); err != nil {
		return Webhook{}, err
	}
	return w, nil
}

// WebhookInput is Create/UpdateWebhook's shared field shape.
type WebhookInput struct {
	Name     string
	URL      string
	Events   []string
	IsActive bool
}

func validateWebhookInput(input WebhookInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("%w: name must not be empty", ErrInvalidWebhook)
	}
	if strings.TrimSpace(input.URL) == "" {
		return fmt.Errorf("%w: url must not be empty", ErrInvalidWebhook)
	}
	for _, e := range input.Events {
		if !validEvent(e) {
			return fmt.Errorf("%w: unrecognized event %q", ErrInvalidWebhook, e)
		}
	}
	return nil
}

// CreateWebhook registers a new webhook for firmID - owner-gated, same
// tier as internal/apikey.CreateAPIKey. secret is generated server-side
// (crypto/rand, same entropy as internal/apikey's own key generation)
// and returned in full (see this package's own doc comment on why,
// unlike an API key, a webhook secret isn't a one-time reveal).
func CreateWebhook(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input WebhookInput) (Webhook, error) {
	if err := validateWebhookInput(input); err != nil {
		return Webhook{}, err
	}

	secret, err := generateSecret()
	if err != nil {
		return Webhook{}, err
	}

	var hook Webhook
	err = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
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

		events := input.Events
		if events == nil {
			events = []string{}
		}
		row := tx.QueryRow(ctx, `
			INSERT INTO webhooks (firm_id, name, url, events, secret, is_active)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING `+webhookColumns, firmID, strings.TrimSpace(input.Name), strings.TrimSpace(input.URL), events, secret, input.IsActive)
		w, err := scanWebhook(row)
		if err != nil {
			return fmt.Errorf("insert webhook: %w", err)
		}
		hook = w
		return auditlog.Write(ctx, tx, firmID, userID, hook.ID, auditEntityType, createAuditAction, map[string]any{
			"name": input.Name, "url": input.URL, "events": events, "isActive": input.IsActive,
		})
	})
	if err != nil {
		return Webhook{}, err
	}
	// onboarding.CompleteStep (see that package's own doc comment): fire-
	// and-forget, never allowed to fail this request - the
	// configure_webhook onboarding checklist item.
	onboarding.CompleteStep(pool, firmID, userID, onboarding.StepConfigureWebhook)
	return hook, nil
}

// ListWebhooks returns every webhook for firmID - owner-gated, same tier
// as CreateWebhook (webhook secrets are credential-management
// information, the same tier internal/edgeagent.ListTokens' own doc
// comment gives its analogous list).
func ListWebhooks(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Webhook, error) {
	var hooks []Webhook
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

		rows, err := tx.Query(ctx, `SELECT `+webhookColumns+` FROM webhooks WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list webhooks: %w", err)
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

// UpdateWebhook replaces webhookID's mutable fields (name/url/events/
// is_active) - owner-gated, same tier as CreateWebhook. The secret is
// never changed here - this package has no rotation endpoint (Scope
// boundaries); delete and recreate is the mechanism, same as
// internal/apikey's own "no rotation" scope boundary.
func UpdateWebhook(ctx context.Context, pool *pgxpool.Pool, firmID, userID, webhookID uuid.UUID, input WebhookInput) (Webhook, error) {
	if err := validateWebhookInput(input); err != nil {
		return Webhook{}, err
	}

	var hook Webhook
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

		events := input.Events
		if events == nil {
			events = []string{}
		}
		row := tx.QueryRow(ctx, `
			UPDATE webhooks SET name = $1, url = $2, events = $3, is_active = $4
			WHERE id = $5 AND firm_id = $6
			RETURNING `+webhookColumns, strings.TrimSpace(input.Name), strings.TrimSpace(input.URL), events, input.IsActive, webhookID, firmID)
		w, err := scanWebhook(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrWebhookNotFound
			}
			return fmt.Errorf("update webhook: %w", err)
		}
		hook = w
		return auditlog.Write(ctx, tx, firmID, userID, hook.ID, auditEntityType, updateAuditAction, map[string]any{
			"name": input.Name, "url": input.URL, "events": events, "isActive": input.IsActive,
		})
	})
	if err != nil {
		return Webhook{}, err
	}
	return hook, nil
}

// DeleteWebhook removes webhookID (and, via ON DELETE CASCADE, its own
// delivery history) - owner-gated, same tier as CreateWebhook.
func DeleteWebhook(ctx context.Context, pool *pgxpool.Pool, firmID, userID, webhookID uuid.UUID) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
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

		tag, err := tx.Exec(ctx, `DELETE FROM webhooks WHERE id = $1 AND firm_id = $2`, webhookID, firmID)
		if err != nil {
			return fmt.Errorf("delete webhook: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrWebhookNotFound
		}

		return auditlog.Write(ctx, tx, firmID, userID, webhookID, auditEntityType, deleteAuditAction, nil)
	})
}
