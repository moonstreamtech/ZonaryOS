// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// InstanceSummary is one workflow_instances row's minimal cross-module
// summary shape - definition + state, enough for a linking UI (this
// package's own AvailableAction/InstanceStateResult carry far more,
// which no caller here needs).
type InstanceSummary struct {
	ID             uuid.UUID
	DefinitionKey  string
	DefinitionName string
	StateKey       string
	StateName      string
	CreatedAt      time.Time
}

// ListInstancesByCustomer returns every workflow_instances row (across
// every workflow definition, not just stock_to_sale) whose payload has a
// "customer_id" field equal to customerID - Part 3c of the multi-tenant
// isolation hardening + performance batch: "the first real CRM view that
// connects sales to customers". stock_to_sale.go's own customer_id field
// (FieldTypeCustomer) is the only one that populates this today, but the
// query itself isn't hardcoded to that one definition - any future
// workflow adopting the same "customer_id" payload field name is picked
// up automatically. Member-gated, same tier as ListInstances; a customer
// with no matching instances yet returns an empty slice, not an error.
func ListInstancesByCustomer(ctx context.Context, pool *pgxpool.Pool, firmID, userID, customerID uuid.UUID) ([]InstanceSummary, error) {
	var summaries []InstanceSummary
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrDefinitionNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT wi.id, wd.key, wd.name, ws.key, ws.name, wi.created_at
			FROM workflow_instances wi
			JOIN workflow_definitions wd ON wd.id = wi.workflow_definition_id
			JOIN workflow_states ws ON ws.id = wi.current_state_id
			WHERE wi.firm_id = $1 AND wi.payload ->> 'customer_id' = $2
			ORDER BY wi.created_at DESC
		`, firmID, customerID.String())
		if err != nil {
			return fmt.Errorf("list instances by customer: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var s InstanceSummary
			if err := rows.Scan(&s.ID, &s.DefinitionKey, &s.DefinitionName, &s.StateKey, &s.StateName, &s.CreatedAt); err != nil {
				return err
			}
			summaries = append(summaries, s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return summaries, nil
}
