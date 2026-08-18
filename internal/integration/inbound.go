// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Part 3 of the data pipeline/ETL/system integrations batch: inbound
// data pull, a scheduled poller - same RunX(ctx, pool, pollInterval)/
// ProcessX(ctx, pool) split every other background scheduler in this
// codebase already uses (internal/asset.RunMaintenanceScheduler,
// internal/ai.RunAnomalyScheduler).
//
// Idempotency approach: every create-or-update decision is keyed on the
// TARGET entity's own natural key (products.sku, customers.email) - a
// SELECT-by-natural-key immediately before the create/update decision,
// every poll cycle, for every fetched record. Re-running the exact same
// poll response twice (a connector's own source didn't actually change
// anything since the last poll) is a true no-op in effect: the "update"
// path re-writes the same field values, which is safe and cheap, not
// something this batch tries to specially detect/skip - there is no
// separate "unchanged, skip" optimization, keeping this simple and
// correct rather than fast. See this file's own doc comment on
// conflict resolution below for the one real caveat: BIDIRECTIONAL
// mappings have no actual conflict detection.
//
// Conflict resolution (bidirectional sync): last-write-wins, undocumented
// beyond this comment because there IS no more sophisticated behavior to
// document - an inbound pull that updates a product ZonaryOS itself also
// pushed outbound moments earlier (via the SAME bidirectional mapping)
// simply overwrites with whatever the inbound pull found, with no
// attempt to detect or reconcile the two having disagreed. Part 3's own
// scope boundary is explicit that real conflict resolution is out of
// scope for this batch.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/crm"
	"github.com/moonstreamtech/ZonaryOS/internal/cryptutil"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// DefaultInboundPollInterval is the scheduler's own ticker cadence - how
// often ProcessInboundSyncs runs at all, NOT how often any given
// connector is actually polled (that's each connector's own
// config["pollIntervalMinutes"], checked against last_sync_at inside
// ProcessInboundSyncs - a scheduler tick that finds no connector due
// yet is a fast, cheap no-op). 5 minutes: frequent enough that a
// connector configured for e.g. "every 15 minutes" is never more than 5
// minutes late, infrequent enough this isn't a busy-poll loop.
const DefaultInboundPollInterval = 5 * time.Minute

// defaultConnectorPollIntervalMinutes is used when a connector's own
// config has no "pollIntervalMinutes" field at all (or a non-numeric
// one) - 15 minutes, a reasonable default cadence for a periodic
// entity-sync feed (frequent enough to feel "live," infrequent enough
// not to hammer a partner system that has no rate-limit guarantee).
const defaultConnectorPollIntervalMinutes = 15

// RunInboundScheduler runs ProcessInboundSyncs every pollInterval until
// ctx is cancelled.
func RunInboundScheduler(ctx context.Context, pool *pgxpool.Pool, enc *cryptutil.Encryptor, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ProcessInboundSyncs(ctx, pool, enc)
		}
	}
}

// ProcessInboundSyncs is RunInboundScheduler's testable core: for every
// firm, every active connector with at least one active inbound/
// bidirectional mapping that's actually due (config["pollIntervalMinutes"]
// since last_sync_at), fetch, map, and create/update entities - each
// connector's own poll is logged as one integration_sync_logs row (one
// row per connector per due poll, not one per mapping the way outbound
// pushes are - a connector fetch is one HTTP call shared across however
// many of its own mappings apply to the fetched records).
func ProcessInboundSyncs(ctx context.Context, pool *pgxpool.Pool, enc *cryptutil.Encryptor) {
	firmIDs, err := listAllFirmIDs(ctx, pool)
	if err != nil {
		slog.Error("integration inbound scheduler: list firms", "err", err)
		return
	}
	for _, firmID := range firmIDs {
		processInboundSyncsForFirm(ctx, pool, enc, firmID)
	}
}

// listAllFirmIDs mirrors every other scheduler's own function of the
// same name (internal/asset/scheduler.go, internal/ai/anomaly.go) -
// firms carries no RLS at all, it IS the tenant boundary, so a plain
// unscoped read is correct here too. Redeclared per-package rather than
// shared, the same "no shared helper, each scheduler package has its
// own tiny copy" convention those other schedulers already establish.
func listAllFirmIDs(ctx context.Context, pool *pgxpool.Pool) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `SELECT id FROM firms`)
	if err != nil {
		return nil, fmt.Errorf("list firms: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan firm id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ciaudit:ignore-firmid-check: called only by ProcessInboundSyncs, with
// firmID drawn from listAllFirmIDs (an internal, unscoped read of the
// firms table itself) - never from an HTTP caller.
func processInboundSyncsForFirm(ctx context.Context, pool *pgxpool.Pool, enc *cryptutil.Encryptor, firmID uuid.UUID) {
	connectorIDs, err := dueInboundConnectorIDsTx(ctx, pool, firmID)
	if err != nil {
		slog.Error("integration inbound scheduler: list due connectors", "firmId", firmID, "err", err)
		return
	}
	for _, connectorID := range connectorIDs {
		processInboundConnector(ctx, pool, enc, firmID, connectorID)
	}
}

// dueInboundConnectorIDsTx returns every active connector for firmID
// that (a) has at least one active inbound/bidirectional mapping, and
// (b) is actually due - last_sync_at is NULL (never synced) or older
// than its own config["pollIntervalMinutes"] (defaultConnectorPollIntervalMinutes
// if unset/non-numeric).
// ciaudit:ignore-firmid-check: internal helper called only by
// processInboundSyncsForFirm, itself only reachable from the background
// scheduler with a firmID drawn from listAllFirmIDs - never from an HTTP
// caller.
func dueInboundConnectorIDsTx(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT DISTINCT c.id, c.config, c.last_sync_at
			FROM integration_connectors c
			JOIN integration_mappings m ON m.connector_id = c.id
			WHERE c.firm_id = $1 AND c.is_active AND m.is_active
				AND m.sync_direction IN ('inbound', 'bidirectional')
		`, firmID)
		if err != nil {
			return fmt.Errorf("list connectors with active inbound mappings: %w", err)
		}
		defer rows.Close()

		type candidate struct {
			id         uuid.UUID
			configRaw  []byte
			lastSyncAt *time.Time
		}
		var candidates []candidate
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.configRaw, &c.lastSyncAt); err != nil {
				return fmt.Errorf("scan candidate connector: %w", err)
			}
			candidates = append(candidates, c)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		for _, c := range candidates {
			config, err := unmarshalConfig(c.configRaw)
			if err != nil {
				return err
			}
			if isDue(c.lastSyncAt, pollIntervalMinutesFromConfig(config)) {
				ids = append(ids, c.id)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func pollIntervalMinutesFromConfig(config map[string]any) int {
	switch v := config["pollIntervalMinutes"].(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case string:
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultConnectorPollIntervalMinutes
}

func isDue(lastSyncAt *time.Time, pollIntervalMinutes int) bool {
	if lastSyncAt == nil {
		return true
	}
	return time.Since(*lastSyncAt) >= time.Duration(pollIntervalMinutes)*time.Minute
}

// processInboundConnector fetches connectorID's own source once and
// applies every one of its active inbound/bidirectional mappings to the
// fetched records - one integration_sync_logs row for the whole poll.
// ciaudit:ignore-firmid-check: internal helper called only by
// processInboundSyncsForFirm - see dueInboundConnectorIDsTx's own comment
// above for why firmID here is never caller-supplied.
func processInboundConnector(ctx context.Context, pool *pgxpool.Pool, enc *cryptutil.Encryptor, firmID, connectorID uuid.UUID) {
	var connector Connector
	var mappings []Mapping
	var syncLogID uuid.UUID
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		id, err := startSyncLogTx(ctx, tx, firmID, connectorID, nil)
		if err != nil {
			return err
		}
		syncLogID = id

		c, err := getConnectorWithDecryptedConfigTx(ctx, tx, enc, firmID, connectorID)
		if err != nil {
			return err
		}
		connector = c

		rows, err := tx.Query(ctx, `
			SELECT `+mappingColumns+` FROM integration_mappings
			WHERE firm_id = $1 AND connector_id = $2 AND is_active AND sync_direction IN ('inbound', 'bidirectional')
		`, firmID, connectorID)
		if err != nil {
			return fmt.Errorf("list active inbound mappings: %w", err)
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
		slog.Warn("integration inbound sync: start sync log / read connector failed", "firmId", firmID, "connectorId", connectorID, "err", err)
		return
	}

	recordsProcessed, recordsFailed, errorMessages := pullOne(ctx, pool, firmID, connector, mappings)

	if err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return finishSyncLogTx(ctx, tx, firmID, connectorID, syncLogID, recordsProcessed, recordsFailed, errorMessages)
	}); err != nil {
		slog.Warn("integration inbound sync: finish sync log failed", "firmId", firmID, "connectorId", connectorID, "err", err)
	}
}

// pullOne does the actual fetch-and-apply half of processInboundConnector,
// entirely outside any single long-lived transaction (each create/update
// call below opens its own short transaction, via
// inventory.CreateProduct/UpdateProduct/crm.CreateCustomer/UpdateCustomer
// themselves) - same "never hold a transaction open across a network
// call" discipline outbound.go's own pushOne follows.
// ciaudit:ignore-firmid-check: internal helper called only by
// processInboundConnector - see dueInboundConnectorIDsTx's own comment
// above for why firmID here is never caller-supplied.
func pullOne(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID, connector Connector, mappings []Mapping) (recordsProcessed, recordsFailed int, errorMessages []string) {
	if connector.Type != ConnectorHTTPWebhook {
		return 0, 0, []string{ErrConnectorTypeNotSupported.Error()}
	}
	sourceURL, _ := connector.Config["url"].(string)
	if sourceURL == "" {
		return 0, 0, []string{"connector config has no \"url\" field"}
	}

	records, err := fetchJSONRecords(ctx, sourceURL)
	if err != nil {
		return 0, 0, []string{err.Error()}
	}

	ownerID, err := resolveFirmOwnerUserID(ctx, pool, firmID)
	if err != nil {
		return 0, 0, []string{fmt.Sprintf("resolve firm owner for inbound sync: %v", err)}
	}
	if ownerID == uuid.Nil {
		return 0, 0, []string{"firm has no owner user to attribute inbound-created/updated records to"}
	}

	for _, m := range mappings {
		for _, record := range records {
			mapped, err := ApplyFieldMappings(record, m.FieldMappings)
			if err != nil {
				recordsFailed++
				errorMessages = append(errorMessages, err.Error())
				continue
			}
			if err := applyInboundRecord(ctx, pool, firmID, ownerID, m.TargetEntity, mapped); err != nil {
				recordsFailed++
				errorMessages = append(errorMessages, err.Error())
				continue
			}
			recordsProcessed++
		}
	}
	return recordsProcessed, recordsFailed, errorMessages
}

// applyInboundRecord creates or updates one entity from mapped (already
// field-mapped/transformed) - dispatches on targetEntity, Part 3's own
// "products, customers" scope (suppliers/anything else returns
// ErrTargetEntityNotSupported, never a silent no-op - see this
// package's own doc comment).
// ciaudit:ignore-firmid-check: internal helper called only by pullOne -
// see dueInboundConnectorIDsTx's own comment above for why firmID here is
// never caller-supplied.
func applyInboundRecord(ctx context.Context, pool *pgxpool.Pool, firmID, ownerID uuid.UUID, targetEntity string, mapped map[string]any) error {
	switch targetEntity {
	case "products":
		return applyInboundProduct(ctx, pool, firmID, ownerID, mapped)
	case "customers":
		return applyInboundCustomer(ctx, pool, firmID, ownerID, mapped)
	default:
		return fmt.Errorf("%w: %q", ErrTargetEntityNotSupported, targetEntity)
	}
}

// ciaudit:ignore-firmid-check: internal helper called only by
// applyInboundRecord - see dueInboundConnectorIDsTx's own comment above
// for why firmID here is never caller-supplied. CreateProduct/UpdateProduct
// (called below) run their own IsMember/IsOwner check against ownerID.
func applyInboundProduct(ctx context.Context, pool *pgxpool.Pool, firmID, ownerID uuid.UUID, mapped map[string]any) error {
	sku, _ := mapped["sku"].(string)
	if sku == "" {
		return fmt.Errorf("inbound product record has no \"sku\" field mapped (sku is this entity's natural key)")
	}

	existingID, err := findProductIDBySKU(ctx, pool, firmID, sku)
	if err != nil {
		return err
	}

	if existingID == uuid.Nil {
		_, err := inventory.CreateProduct(ctx, pool, firmID, ownerID, inventory.CreateProductInput{
			SKU: sku, Name: mapString(mapped["name"], sku), Description: mapString(mapped["description"], ""),
			Unit: mapString(mapped["unit"], ""), UnitPrice: mapString(mapped["unitPrice"], ""),
			CostPrice: mapString(mapped["costPrice"], ""), TaxRate: mapString(mapped["taxRate"], ""),
			Category: mapString(mapped["category"], ""), MinQuantity: mapString(mapped["minQuantity"], ""),
		})
		if err != nil {
			return fmt.Errorf("create product %q: %w", sku, err)
		}
		return nil
	}

	update := inventory.ProductUpdate{}
	if v, ok := mapped["name"]; ok {
		s := mapString(v, "")
		update.Name = &s
	}
	if v, ok := mapped["description"]; ok {
		s := mapString(v, "")
		update.Description = &s
	}
	if v, ok := mapped["unit"]; ok {
		s := mapString(v, "")
		update.Unit = &s
	}
	if v, ok := mapped["unitPrice"]; ok {
		s := mapString(v, "")
		update.UnitPrice = &s
	}
	if v, ok := mapped["costPrice"]; ok {
		s := mapString(v, "")
		update.CostPrice = &s
	}
	if v, ok := mapped["taxRate"]; ok {
		s := mapString(v, "")
		update.TaxRate = &s
	}
	if v, ok := mapped["category"]; ok {
		s := mapString(v, "")
		update.Category = &s
	}
	if v, ok := mapped["minQuantity"]; ok {
		s := mapString(v, "")
		update.MinQuantity = &s
	}
	if _, err := inventory.UpdateProduct(ctx, pool, firmID, ownerID, existingID, update); err != nil {
		return fmt.Errorf("update product %q: %w", sku, err)
	}
	return nil
}

// ciaudit:ignore-firmid-check: internal helper called only by
// applyInboundRecord - see dueInboundConnectorIDsTx's own comment above
// for why firmID here is never caller-supplied. CreateCustomer/UpdateCustomer
// (called below) run their own IsMember/IsOwner check against ownerID.
func applyInboundCustomer(ctx context.Context, pool *pgxpool.Pool, firmID, ownerID uuid.UUID, mapped map[string]any) error {
	email, _ := mapped["email"].(string)
	if email == "" {
		return fmt.Errorf("inbound customer record has no \"email\" field mapped (email is this entity's natural key)")
	}

	existingID, err := findCustomerIDByEmail(ctx, pool, firmID, email)
	if err != nil {
		return err
	}

	if existingID == uuid.Nil {
		_, err := crm.CreateCustomer(ctx, pool, firmID, ownerID, crm.CreateCustomerInput{
			Name: mapString(mapped["name"], email), Email: email, Phone: mapString(mapped["phone"], ""),
			Address: mapString(mapped["address"], ""), TaxID: mapString(mapped["taxId"], ""),
			CreditLimit: mapString(mapped["creditLimit"], ""), Currency: mapString(mapped["currency"], ""),
		})
		if err != nil {
			return fmt.Errorf("create customer %q: %w", email, err)
		}
		return nil
	}

	update := crm.CustomerUpdate{}
	if v, ok := mapped["name"]; ok {
		s := mapString(v, "")
		update.Name = &s
	}
	if v, ok := mapped["phone"]; ok {
		s := mapString(v, "")
		update.Phone = &s
	}
	if v, ok := mapped["address"]; ok {
		s := mapString(v, "")
		update.Address = &s
	}
	if v, ok := mapped["taxId"]; ok {
		s := mapString(v, "")
		update.TaxID = &s
	}
	if v, ok := mapped["creditLimit"]; ok {
		s := mapString(v, "")
		update.CreditLimit = &s
	}
	if v, ok := mapped["currency"]; ok {
		s := mapString(v, "")
		update.Currency = &s
	}
	if _, err := crm.UpdateCustomer(ctx, pool, firmID, ownerID, existingID, update); err != nil {
		return fmt.Errorf("update customer %q: %w", email, err)
	}
	return nil
}

// findProductIDBySKU/findCustomerIDByEmail are the natural-key lookups
// this file's own doc comment describes as the idempotency mechanism -
// plain, unauthenticated (system-triggered background reads, same
// reasoning as this file's other internal helpers) RLS-scoped queries.
// ciaudit:ignore-firmid-check: internal helper called only by
// applyInboundProduct - see dueInboundConnectorIDsTx's own comment above
// for why firmID here is never caller-supplied.
func findProductIDBySKU(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID, sku string) (uuid.UUID, error) {
	var id uuid.UUID
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id FROM products WHERE firm_id = $1 AND sku = $2`, firmID, sku).Scan(&id)
		if err == pgx.ErrNoRows {
			id = uuid.Nil
			return nil
		}
		return err
	})
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("look up product by sku: %w", err)
	}
	return id, nil
}

// ciaudit:ignore-firmid-check: internal helper called only by
// applyInboundCustomer - see dueInboundConnectorIDsTx's own comment above
// for why firmID here is never caller-supplied.
func findCustomerIDByEmail(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID, email string) (uuid.UUID, error) {
	var id uuid.UUID
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id FROM customers WHERE firm_id = $1 AND email = $2`, firmID, email).Scan(&id)
		if err == pgx.ErrNoRows {
			id = uuid.Nil
			return nil
		}
		return err
	})
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("look up customer by email: %w", err)
	}
	return id, nil
}

// resolveFirmOwnerUserID returns one of firmID's own owner users -
// inbound sync is a background, system-triggered operation with no real
// human caller, but CreateProduct/UpdateProduct/CreateCustomer/
// UpdateCustomer are all owner-gated; attributing the resulting
// audit_log entries to a real owner user is the same "the connector's
// own configuration was itself an owner-authorized decision, acting on
// its behalf is consistent" reasoning internal/ai/anomaly.go's own
// writeAnomalyAuditTx establishes for its identical no-human-caller
// case. Returns uuid.Nil (not an error) if the firm somehow has no
// owner at all - callers treat that as "nothing to attribute to,"
// logged as a sync failure rather than silently skipped.
//
// ciaudit:ignore-firmid-check: internal helper called only by pullOne -
// see dueInboundConnectorIDsTx's own comment above for why firmID here is
// never caller-supplied.
func resolveFirmOwnerUserID(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) (uuid.UUID, error) {
	var ownerID uuid.UUID
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT ufr.user_id
			FROM user_firm_roles ufr
			JOIN roles r ON r.id = ufr.role_id AND r.firm_id = ufr.firm_id
			WHERE ufr.firm_id = $1 AND r.is_owner
			ORDER BY ufr.user_id
			LIMIT 1
		`, firmID).Scan(&ownerID)
		if err == pgx.ErrNoRows {
			ownerID = uuid.Nil
			return nil
		}
		return err
	})
	if err != nil {
		return uuid.UUID{}, err
	}
	return ownerID, nil
}

func unmarshalConfig(raw []byte) (map[string]any, error) {
	config := map[string]any{}
	if len(raw) == 0 {
		return config, nil
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("unmarshal connector config: %w", err)
	}
	return config, nil
}

// mapString renders a field-mapped value as a string, matching
// transform.go's own toString conversion (a JSON number becomes its
// plain decimal text, not Go's default float formatting) - fallback is
// returned for a nil/absent value, distinguishing "field genuinely maps
// to empty" (fallback="") from "field maps to something meaningful even
// when absent" (a caller can pass a non-empty fallback, e.g. reusing sku
// as a product's own name when no name field was mapped at all).
func mapString(value any, fallback string) string {
	if value == nil {
		return fallback
	}
	return toString(value)
}
