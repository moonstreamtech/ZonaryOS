// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package portability is Vision §3's explicit call for import/export that
// avoids vendor lock-in - the first real implementation. Export produces a
// single structured JSON snapshot of a firm's complete configuration and
// data; import reads back ONLY the configuration portion of such a
// snapshot (workflow definitions, rules, chart of accounts, product
// catalog) - never historical data (transactions, instances, journal
// entries), which stays a deliberately deferred, genuinely harder problem
// (ID remapping, deduplication, referential integrity across firms - see
// this package's own report for the reasoning).
//
// This is a full-snapshot export, not a streaming/paginated one - a
// single JSON response is the right size for the dev/test scale this
// system is currently at. A firm large enough that this snapshot becomes
// impractically large (memory, response size, request duration) needs a
// genuinely different mechanism - a streaming or chunked export format -
// which is real future work, not something this package's current shape
// can be casually extended into; noted here explicitly as a known
// boundary, not a silently-accepted gap.
//
// Both export and import are owner-gated (the same tier as
// internal/workflow.DefineWorkflowForFirm/internal/accounting.CreateAccount
// - reading out or reconfiguring a firm's entire structure is a
// structural firm decision) and write one audit_log entry each.
package portability

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/crm"
	"github.com/moonstreamtech/ZonaryOS/internal/firm"
	"github.com/moonstreamtech/ZonaryOS/internal/hr"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	"github.com/moonstreamtech/ZonaryOS/internal/invoicing"
	"github.com/moonstreamtech/ZonaryOS/internal/logistics"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// ExportVersion is ExportDocument's own format version - "1" today.
// ImportConfiguration rejects any other value (ErrInvalidExportDocument)
// rather than guessing at a shape it was never tested against; a future,
// incompatible format change bumps this and ImportConfiguration gains a
// second accepted value/migration path, not a silent reinterpretation.
const ExportVersion = "1"

const (
	exportAuditEntityType = "firm_export"
	exportAuditAction     = "export"
)

// ExportDocument is the full-firm snapshot export.
type ExportDocument struct {
	Version             string                    `json:"version"`
	ExportedAt          time.Time                 `json:"exported_at"`
	Firm                firm.Metadata             `json:"firm"`
	Accounts            []accounting.Account      `json:"accounts"`
	WorkflowDefinitions []workflow.DefinitionSpec `json:"workflow_definitions"`
	Rules               []workflow.Rule           `json:"rules"`
	People              []hr.Person               `json:"people"`
	Products            []inventory.Product       `json:"products"`
	Suppliers           []inventory.Supplier      `json:"suppliers"`
	Customers           []crm.Customer            `json:"customers"`
	WorkflowInstances   []ExportWorkflowInstance  `json:"workflow_instances"`
	JournalEntries      []accounting.JournalEntry `json:"journal_entries"`
	Invoices            []invoicing.Invoice       `json:"invoices"`
	Deliveries          []logistics.Delivery      `json:"deliveries"`
	StockLevels         []ExportStockLevel        `json:"stock_levels"`
}

// ExportWorkflowInstance is one workflow_instances row in the export -
// its owning definition's key (not just its ID, since a key survives a
// round trip through a different database the way a UUID coincidentally
// wouldn't), current state, and payload.
type ExportWorkflowInstance struct {
	ID                    uuid.UUID      `json:"id"`
	WorkflowDefinitionKey string         `json:"workflowDefinitionKey"`
	StateKey              string         `json:"stateKey"`
	Payload               map[string]any `json:"payload"`
}

// ExportStockLevel is one stock_levels row in the export, identified by
// the owning product's SKU (not its ID, for the same "survives a round
// trip" reasoning ExportWorkflowInstance's own doc comment gives).
type ExportStockLevel struct {
	ProductSKU       string `json:"productSku"`
	Location         string `json:"location"`
	Quantity         string `json:"quantity"`
	ReservedQuantity string `json:"reservedQuantity"`
}

// ExportFirm builds the complete ExportDocument for firmID by calling
// straight into each owning module's existing member-gated List/Get
// functions (never querying another module's tables directly) - the
// same "modules own their own tables" boundary every other cross-module
// reader in this codebase (internal/reports) already respects. This is
// deliberately NOT one single database transaction across every entity
// (each called function opens and commits its own, the same as every
// other pool-based read in this codebase) - a firm actively being
// written to while an export runs could in principle see a snapshot that
// isn't perfectly point-in-time consistent across every table; acceptable
// for a read-only export at the dev/test scale this system is currently
// at (see this package's own doc comment), not something a single-shot
// full-snapshot format needs to solve. Owner-gated: exporting a firm's
// complete configuration and data is a structural, sensitive operation,
// the same tier as internal/workflow.DefineWorkflowForFirm.
//
// ciaudit:ignore-firmid-check: authorization happens via requireOwner
// (this file), which calls permission.IsMember/IsOwner directly - this
// function's own firmID parameter scopes the subsequent reads, it does
// not re-decide authorization itself.
func ExportFirm(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) (ExportDocument, error) {
	if err := requireOwner(ctx, pool, firmID, userID); err != nil {
		return ExportDocument{}, err
	}

	doc := ExportDocument{Version: ExportVersion, ExportedAt: time.Now().UTC()}

	meta, err := firm.Get(ctx, pool, firmID, userID)
	if err != nil {
		return ExportDocument{}, err
	}
	doc.Firm = meta

	if doc.Accounts, err = accounting.ListAccounts(ctx, pool, firmID, userID); err != nil {
		return ExportDocument{}, err
	}

	if doc.WorkflowDefinitions, err = workflow.ExportDefinitionSpecs(ctx, pool, firmID, userID); err != nil {
		return ExportDocument{}, err
	}
	for _, def := range doc.WorkflowDefinitions {
		rules, err := workflow.ListRulesForDefinition(ctx, pool, firmID, userID, def.Key)
		if err != nil {
			return ExportDocument{}, err
		}
		doc.Rules = append(doc.Rules, rules...)
	}

	if doc.People, err = hr.ListPeople(ctx, pool, firmID, userID, hr.ListPeopleOptions{}); err != nil {
		return ExportDocument{}, err
	}
	if doc.Products, err = inventory.ListProducts(ctx, pool, firmID, userID, inventory.ListProductsOptions{}); err != nil {
		return ExportDocument{}, err
	}
	if doc.Suppliers, err = inventory.ListSuppliers(ctx, pool, firmID, userID, inventory.ListSuppliersOptions{}); err != nil {
		return ExportDocument{}, err
	}
	// One query for every product's stock (inventory.ListStockForFirm),
	// not one inventory.GetStock call per product - fixed N+1 (see
	// docs/DEVELOPMENT.md's "Performance: N+1 query audit" section).
	skuByProductID := make(map[uuid.UUID]string, len(doc.Products))
	for _, p := range doc.Products {
		skuByProductID[p.ID] = p.SKU
	}
	allLevels, err := inventory.ListStockForFirm(ctx, pool, firmID, userID)
	if err != nil {
		return ExportDocument{}, err
	}
	for _, l := range allLevels {
		doc.StockLevels = append(doc.StockLevels, ExportStockLevel{
			ProductSKU: skuByProductID[l.ProductID], Location: l.Location, Quantity: l.Quantity, ReservedQuantity: l.ReservedQuantity,
		})
	}

	if doc.Customers, err = crm.ListCustomers(ctx, pool, firmID, userID, crm.ListCustomersOptions{}); err != nil {
		return ExportDocument{}, err
	}

	for _, def := range doc.WorkflowDefinitions {
		definitionInfo, err := workflow.LookupDefinitionByKey(ctx, pool, firmID, userID, def.Key)
		if err != nil {
			return ExportDocument{}, err
		}
		result, err := workflow.ListInstances(ctx, pool, firmID, userID, definitionInfo.ID, workflow.ListInstancesOptions{})
		if err != nil {
			return ExportDocument{}, err
		}
		for _, inst := range result.Instances {
			doc.WorkflowInstances = append(doc.WorkflowInstances, ExportWorkflowInstance{
				ID: inst.InstanceID, WorkflowDefinitionKey: def.Key, StateKey: inst.State.Key, Payload: inst.Payload,
			})
		}
	}

	entriesResult, err := accounting.ListJournalEntries(ctx, pool, firmID, userID, accounting.ListOptions{})
	if err != nil {
		return ExportDocument{}, err
	}
	doc.JournalEntries = entriesResult.Entries

	// invoicing.ListInvoicesWithLines batches every invoice's lines in with
	// one extra ANY($1) query, not one invoicing.GetInvoice call per
	// invoice - fixed N+1 (see docs/DEVELOPMENT.md's "Performance: N+1
	// query audit" section).
	if doc.Invoices, err = invoicing.ListInvoicesWithLines(ctx, pool, firmID, userID); err != nil {
		return ExportDocument{}, err
	}

	if doc.Deliveries, err = logistics.ListDeliveries(ctx, pool, firmID, userID, logistics.ListDeliveriesOptions{}); err != nil {
		return ExportDocument{}, err
	}

	if err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return auditlog.Write(ctx, tx, firmID, userID, firmID, exportAuditEntityType, exportAuditAction, map[string]any{
			"workflowDefinitions": len(doc.WorkflowDefinitions), "accounts": len(doc.Accounts), "products": len(doc.Products),
		})
	}); err != nil {
		return ExportDocument{}, err
	}

	return doc, nil
}

// requireOwner is the shared "is this caller allowed to export/import
// this firm's data at all" check both ExportFirm and ImportConfiguration
// open with.
func requireOwner(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) error {
	var outerErr error
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			outerErr = ErrFirmNotFound
			return nil
		}
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			outerErr = ErrNotOwner
		}
		return nil
	})
	if err != nil {
		return err
	}
	return outerErr
}
