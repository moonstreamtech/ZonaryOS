// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Part 2 of the data pipeline/ETL/system integrations batch: outbound
// data push. Distinct from internal/webhook's own generic, event-
// subscription-based webhooks system (a firm subscribes a URL to event
// TYPES like "invoice.paid") - this is a field-mapped, entity-sync push
// (a firm maps specific fields of a specific entity onto a specific
// connector's own target shape). The two are independent, run side by
// side at the same call sites, and don't share any database row - only
// the actual HTTP delivery mechanics (internal/webhook.SendSigned).
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/cryptutil"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/webhook"
)

// DispatchOutbound is the one call site every entity-mutating handler
// that this batch cares about (internal/inventory.CreateProduct/
// UpdateProduct, internal/crm.CreateCustomer, internal/invoicing's own
// payment-status-to-paid transition) calls, right next to its EXISTING
// webhook.Dispatch call for the same event - see each of those call
// sites' own comment for why they sit side by side rather than one
// replacing the other. Looks up every ACTIVE outbound/bidirectional
// mapping for (firmID, sourceEntity), applies each one's own field
// mappings/transforms to payload, and pushes the result to that
// mapping's own connector - one HTTP push per matching mapping, each
// logged as its own integration_sync_logs row.
//
// Runs synchronously up to the "which mappings match" lookup (a single
// indexed query, negligible latency - the same
// integration_mappings_outbound_lookup_idx migrations/0038 adds
// specifically for this), then fires each matching push in its own
// goroutine (deliverOutboundOne) - the exact same "fast lookup
// synchronous, each actual delivery async" shape
// internal/webhook.Dispatch itself already uses, for the identical
// reason: an outbound push is a possibly-slow network call to a partner
// system, and must never block the request that triggered it (a product
// being created should not wait on some other company's server).
//
// A lookup failure (a transient DB error) is logged, not returned - same
// "notification, not a required side effect of the request" posture
// internal/webhook.Dispatch's own doc comment describes.
//
// ciaudit:ignore-firmid-check: internal, non-caller-facing dispatch entry
// point - the caller who triggered the underlying entity change has
// already been authorized for THAT by its own handler before
// DispatchOutbound is ever called, the same reasoning
// internal/webhook.Dispatch's own doc comment gives for its identical
// shape.
//
// Deliberately no *cryptutil.Encryptor parameter: DispatchOutbound is called
// from deep inside several unrelated packages' own entity-mutation
// functions (internal/inventory.CreateProduct/UpdateProduct,
// internal/crm.CreateCustomer, internal/invoicing.RecordPayment) -
// threading an encryptor parameter down to this one call would mean
// adding it to every one of those functions' own signatures purely to
// pass it through, a far more invasive change than this batch's own
// scope justifies. Uses the package-level pkgEncryptor instead - see
// SetEncryptor's own doc comment.
func DispatchOutbound(pool *pgxpool.Pool, firmID uuid.UUID, sourceEntity string, payload map[string]any) {
	ctx := context.Background()
	mappings, err := activeOutboundMappingsForEntity(ctx, pool, firmID, sourceEntity)
	if err != nil {
		slog.Warn("integration outbound dispatch: list active mappings failed", "firmId", firmID, "sourceEntity", sourceEntity, "err", err)
		return
	}
	for _, m := range mappings {
		go deliverOutboundOne(pool, firmID, m, payload)
	}
}

// pkgEncryptor is the package-wide AES-256-GCM encryptor DispatchOutbound
// (and its own callees) use to decrypt a connector's own config - see
// SetEncryptor's own doc comment.
var pkgEncryptor *cryptutil.Encryptor

// SetEncryptor wires pkgEncryptor - called exactly once, at startup
// (cmd/server/main.go), before any request that could trigger
// DispatchOutbound is served. The same "immutable after init" contract
// internal/plugin.Registry's own doc comment establishes for its package-
// level state applies here too - nothing after startup ever calls this
// again. Connector CRUD (connectors.go) and the inbound scheduler
// (inbound.go) both still take an explicit *cryptutil.Encryptor parameter
// instead of reading this var - both are only ever called from
// cmd/server/main.go/this package's own handlers.go, where threading an
// explicit parameter is trivial and clearer than an implicit global; this
// var exists ONLY for DispatchOutbound's own cross-package call-site
// problem.
func SetEncryptor(enc *cryptutil.Encryptor) {
	pkgEncryptor = enc
}

// activeOutboundMappingsForEntity reads firmID's own active mappings for
// sourceEntity whose sync_direction is outbound or bidirectional - not
// gated by permission.IsMember/Has, same reasoning
// internal/webhook.activeWebhooksForEvent's own doc comment gives for
// its identical shape (an internal, system-triggered read, not a
// caller-facing endpoint).
//
// ciaudit:ignore-firmid-check: see this function's own doc comment above.
func activeOutboundMappingsForEntity(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID, sourceEntity string) ([]Mapping, error) {
	var mappings []Mapping
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT `+mappingColumns+` FROM integration_mappings
			WHERE firm_id = $1 AND source_entity = $2 AND is_active AND sync_direction IN ('outbound', 'bidirectional')
		`, firmID, sourceEntity)
		if err != nil {
			return fmt.Errorf("list active outbound mappings: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanMapping(rows)
			if err != nil {
				return fmt.Errorf("scan mapping: %w", err)
			}
			mappings = append(mappings, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return mappings, nil
}

// deliverOutboundOne applies m's own field mappings to payload and
// pushes the result to m's own connector - one integration_sync_logs row
// records the outcome. Three separate steps, each its OWN short-lived
// transaction/no transaction at all - deliberately NOT one transaction
// wrapping the whole thing, the same "never hold a DB transaction open
// across a network HTTP call" discipline internal/webhook's own
// deliverOne/recordDelivery split already establishes (recordDelivery
// there is its own fresh transaction, entirely after send() has already
// returned):
//  1. Open a transaction just long enough to start the sync log row and
//     read+decrypt the connector's own config.
//  2. Outside any transaction: build the mapped payload and make the
//     actual HTTP call.
//  3. Open a second, fresh transaction just to close out the sync log
//     row with the real outcome.
//
// Only connectorType==http_webhook actually pushes anything (Part 1's
// own "only http_webhook executes in this batch" scope boundary) - any
// other connector type is logged as a failed sync with
// ErrConnectorTypeNotSupported, never a silent no-op.
// ciaudit:ignore-firmid-check: internal helper called only by
// DispatchOutbound, itself an internal system-triggered dispatch point
// (see its own doc comment) - never a caller-facing endpoint.
func deliverOutboundOne(pool *pgxpool.Pool, firmID uuid.UUID, m Mapping, payload map[string]any) {
	ctx := context.Background()

	var syncLogID uuid.UUID
	var connector Connector
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		id, err := startSyncLogTx(ctx, tx, firmID, m.ConnectorID, &m.ID)
		if err != nil {
			return err
		}
		syncLogID = id

		c, err := getConnectorWithDecryptedConfigTx(ctx, tx, pkgEncryptor, firmID, m.ConnectorID)
		if err != nil {
			return err
		}
		connector = c
		return nil
	})
	if err != nil {
		slog.Warn("integration outbound push: start sync log / read connector failed", "firmId", firmID, "connectorId", m.ConnectorID, "mappingId", m.ID, "err", err)
		return
	}

	recordsProcessed, recordsFailed, errorMessages := pushOne(ctx, connector, m, payload)

	if err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return finishSyncLogTx(ctx, tx, firmID, m.ConnectorID, syncLogID, recordsProcessed, recordsFailed, errorMessages)
	}); err != nil {
		slog.Warn("integration outbound push: finish sync log failed", "firmId", firmID, "connectorId", m.ConnectorID, "mappingId", m.ID, "err", err)
	}
}

// pushOne does the actual network-facing half of deliverOutboundOne,
// entirely outside any database transaction - returns (0, 1, [msg]) for
// any failure short of an actual 2xx response, (1, 0, nil) on success.
func pushOne(ctx context.Context, connector Connector, m Mapping, payload map[string]any) (recordsProcessed, recordsFailed int, errorMessages []string) {
	if !connector.IsActive {
		return 0, 0, nil
	}
	if connector.Type != ConnectorHTTPWebhook {
		return 0, 1, []string{ErrConnectorTypeNotSupported.Error()}
	}

	targetURL, _ := connector.Config["url"].(string)
	if strings.TrimSpace(targetURL) == "" {
		return 0, 1, []string{"connector config has no \"url\" field"}
	}
	secret, _ := connector.Config["secret"].(string)

	mapped, err := ApplyFieldMappings(payload, m.FieldMappings)
	if err != nil {
		return 0, 1, []string{err.Error()}
	}
	body, err := json.Marshal(map[string]any{
		"entity":    m.TargetEntity,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"data":      mapped,
	})
	if err != nil {
		return 0, 1, []string{fmt.Sprintf("marshal push body: %v", err)}
	}

	status, respBody, sendErr := webhook.SendSigned(ctx, targetURL, secret, body)
	if sendErr == nil && status >= 200 && status < 300 {
		return 1, 0, nil
	}
	if sendErr != nil {
		return 0, 1, []string{sendErr.Error()}
	}
	return 0, 1, []string{fmt.Sprintf("target responded with status %d: %s", status, respBody)}
}
