// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// Delivery is one webhook_deliveries row - the delivery-history view's
// own data source.
type Delivery struct {
	ID             uuid.UUID
	WebhookID      uuid.UUID
	EventType      string
	Payload        map[string]any
	ResponseStatus *int
	ResponseBody   *string
	DeliveredAt    time.Time
	Success        bool
}

// ListDeliveries returns webhookID's own delivery history, most recent
// first - owner-gated, same tier as ListWebhooks.
func ListDeliveries(ctx context.Context, pool *pgxpool.Pool, firmID, userID, webhookID uuid.UUID) ([]Delivery, error) {
	var deliveries []Delivery
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

		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM webhooks WHERE id = $1 AND firm_id = $2)`, webhookID, firmID).Scan(&exists); err != nil {
			return fmt.Errorf("check webhook: %w", err)
		}
		if !exists {
			return ErrWebhookNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, webhook_id, event_type, payload, response_status, response_body, delivered_at, success
			FROM webhook_deliveries WHERE webhook_id = $1 AND firm_id = $2
			ORDER BY delivered_at DESC LIMIT 100
		`, webhookID, firmID)
		if err != nil {
			return fmt.Errorf("list webhook deliveries: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var d Delivery
			var payloadRaw []byte
			if err := rows.Scan(&d.ID, &d.WebhookID, &d.EventType, &payloadRaw, &d.ResponseStatus, &d.ResponseBody, &d.DeliveredAt, &d.Success); err != nil {
				return fmt.Errorf("scan webhook delivery: %w", err)
			}
			if err := json.Unmarshal(payloadRaw, &d.Payload); err != nil {
				return fmt.Errorf("unmarshal delivery payload: %w", err)
			}
			deliveries = append(deliveries, d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return deliveries, nil
}
