// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package warehouse is the warehouse management module foundation:
// warehouses, a collapsible aisle -> shelf -> bin location hierarchy
// under each one, inventory transfers between locations, and cycle
// counting - real structure under the location_id internal/inventory's
// stock_levels/stock_movements now reference, not just a free-text field
// (see migrations/0028_warehouse_management.up.sql's own doc comment for
// the breaking-change migration this replaced). Not a full WMS: no
// barcode scanning (Edge Agent, future), no FIFO/FEFO lot tracking, no
// multi-warehouse purchase order routing, no picking list generation.
//
// Authorization follows this codebase's usual discipline: IsMember
// before any read, IsMember then IsOwner before any write - the same
// tier internal/inventory's/internal/manufacturing's own mutations use.
package warehouse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const (
	warehouseAuditEntityType = "warehouse"
	locationAuditEntityType  = "warehouse_location"

	createAuditAction = "create"
	updateAuditAction = "update"
)

// postgresUniqueViolation is the SQLSTATE code Postgres raises for a
// UNIQUE constraint violation - same constant/reasoning as every other
// package in this codebase that redeclares it locally.
const postgresUniqueViolation = "23505"

// locationTypes mirrors warehouse_locations.type's own CHECK constraint
// (migrations/0028_warehouse_management.up.sql) - validated in Go too so
// a bad value comes back as a clean ErrInvalidLocation instead of a raw
// constraint-violation error, the same "app check first, DB constraint as
// defense in depth" pattern this codebase's other CHECK-backed columns
// (e.g. production_orders.status) already follow.
var locationTypes = map[string]bool{
	"aisle": true, "shelf": true, "bin": true, "staging": true, "dispatch": true,
}

// Warehouse is one warehouses row.
type Warehouse struct {
	ID        uuid.UUID
	FirmID    uuid.UUID
	Name      string
	Address   *string
	IsActive  bool
	CreatedAt time.Time
}

// CreateWarehouseInput is CreateWarehouse's request shape.
type CreateWarehouseInput struct {
	Name    string
	Address string
}

// CreateWarehouse adds a new warehouse to firmID. Owner-gated: defining a
// firm's warehouse structure is a structural firm decision, the same
// tier as internal/inventory.CreateProduct.
func CreateWarehouse(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateWarehouseInput) (Warehouse, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Warehouse{}, fmt.Errorf("%w: name must not be empty", ErrInvalidWarehouse)
	}

	var w Warehouse
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

		w.FirmID = firmID
		w.Name = name
		w.Address = optionalString(input.Address)
		w.IsActive = true

		if err := tx.QueryRow(ctx, `
			INSERT INTO warehouses (firm_id, name, address) VALUES ($1, $2, $3)
			RETURNING id, created_at
		`, firmID, name, w.Address).Scan(&w.ID, &w.CreatedAt); err != nil {
			return fmt.Errorf("insert warehouse: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, w.ID, warehouseAuditEntityType, createAuditAction, map[string]any{"name": name})
	})
	if err != nil {
		return Warehouse{}, err
	}
	return w, nil
}

// UpdateWarehouseInput is UpdateWarehouse's partial-update input - nil
// leaves that field unchanged, the same convention
// internal/inventory.ProductUpdate uses.
type UpdateWarehouseInput struct {
	Name     *string
	Address  *string
	IsActive *bool
}

// UpdateWarehouse applies input's changes to warehouseID. Owner-gated.
func UpdateWarehouse(ctx context.Context, pool *pgxpool.Pool, firmID, userID, warehouseID uuid.UUID, input UpdateWarehouseInput) (Warehouse, error) {
	if input.Name != nil && strings.TrimSpace(*input.Name) == "" {
		return Warehouse{}, fmt.Errorf("%w: name must not be empty", ErrInvalidWarehouse)
	}

	var w Warehouse
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

		row := tx.QueryRow(ctx, `
			UPDATE warehouses SET
				name = COALESCE($1, name),
				address = CASE WHEN $2::boolean THEN $3 ELSE address END,
				is_active = COALESCE($4, is_active)
			WHERE id = $5 AND firm_id = $6
			RETURNING id, firm_id, name, address, is_active, created_at
		`, input.Name, input.Address != nil, optionalString(derefOr(input.Address, "")), input.IsActive, warehouseID, firmID)
		w, err = scanWarehouseRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWarehouseNotFound
		}
		if err != nil {
			return fmt.Errorf("update warehouse: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, warehouseID, warehouseAuditEntityType, updateAuditAction, map[string]any{"name": w.Name})
	})
	if err != nil {
		return Warehouse{}, err
	}
	return w, nil
}

func scanWarehouseRow(row pgx.Row) (Warehouse, error) {
	var w Warehouse
	if err := row.Scan(&w.ID, &w.FirmID, &w.Name, &w.Address, &w.IsActive, &w.CreatedAt); err != nil {
		return Warehouse{}, err
	}
	return w, nil
}

// ListWarehouses returns firmID's warehouses, ordered by name.
// Member-gated.
func ListWarehouses(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Warehouse, error) {
	var warehouses []Warehouse
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT id, firm_id, name, address, is_active, created_at FROM warehouses WHERE firm_id = $1 ORDER BY name`, firmID)
		if err != nil {
			return fmt.Errorf("list warehouses: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			w, err := scanWarehouseRow(rows)
			if err != nil {
				return err
			}
			warehouses = append(warehouses, w)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return warehouses, nil
}

// GetWarehouse resolves one warehouse by id within firmID. Member-gated.
func GetWarehouse(ctx context.Context, pool *pgxpool.Pool, firmID, userID, warehouseID uuid.UUID) (Warehouse, error) {
	var w Warehouse
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT id, firm_id, name, address, is_active, created_at FROM warehouses WHERE id = $1 AND firm_id = $2`, warehouseID, firmID)
		w, err = scanWarehouseRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWarehouseNotFound
		}
		return err
	})
	if err != nil {
		return Warehouse{}, err
	}
	return w, nil
}

// WarehouseLocation is one warehouse_locations row.
type WarehouseLocation struct {
	ID          uuid.UUID
	FirmID      uuid.UUID
	WarehouseID uuid.UUID
	Code        string
	Name        *string
	Type        string
	ParentID    *uuid.UUID
	IsDefault   bool
	IsActive    bool
	CreatedAt   time.Time
}

// CreateLocationInput is CreateLocation's request shape.
type CreateLocationInput struct {
	Code     string
	Name     string
	Type     string // optional, defaults to "bin" (warehouse_locations' own DB default)
	ParentID *uuid.UUID
}

// CreateLocation adds a new location to warehouseID within firmID -
// owner-gated. If ParentID is set, it must name another location within
// the SAME warehouse (a location tree can't span warehouses) - checked
// here in Go so a cross-warehouse parent comes back as a clean
// ErrInvalidLocation rather than relying on warehouse_locations' own FK
// (which only guarantees the parent row exists, not that it's in the
// same warehouse).
func CreateLocation(ctx context.Context, pool *pgxpool.Pool, firmID, userID, warehouseID uuid.UUID, input CreateLocationInput) (WarehouseLocation, error) {
	code := strings.TrimSpace(input.Code)
	if code == "" {
		return WarehouseLocation{}, fmt.Errorf("%w: code must not be empty", ErrInvalidLocation)
	}
	typ := strings.TrimSpace(input.Type)
	if typ == "" {
		typ = "bin"
	} else if !locationTypes[typ] {
		return WarehouseLocation{}, fmt.Errorf("%w: type %q is not one of aisle/shelf/bin/staging/dispatch", ErrInvalidLocation, typ)
	}

	var loc WarehouseLocation
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

		var warehouseExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND firm_id = $2)`, warehouseID, firmID).Scan(&warehouseExists); err != nil {
			return fmt.Errorf("look up warehouse: %w", err)
		}
		if !warehouseExists {
			return ErrWarehouseNotFound
		}

		if input.ParentID != nil {
			var parentWarehouseID uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT warehouse_id FROM warehouse_locations WHERE id = $1 AND firm_id = $2`, *input.ParentID, firmID).Scan(&parentWarehouseID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("%w: parent location not found", ErrInvalidLocation)
				}
				return fmt.Errorf("look up parent location: %w", err)
			}
			if parentWarehouseID != warehouseID {
				return fmt.Errorf("%w: parent location must belong to the same warehouse", ErrInvalidLocation)
			}
		}

		loc.FirmID = firmID
		loc.WarehouseID = warehouseID
		loc.Code = code
		loc.Name = optionalString(input.Name)
		loc.Type = typ
		loc.ParentID = input.ParentID

		if err := tx.QueryRow(ctx, `
			INSERT INTO warehouse_locations (firm_id, warehouse_id, code, name, type, parent_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, is_default, is_active, created_at
		`, firmID, warehouseID, code, loc.Name, typ, input.ParentID).Scan(&loc.ID, &loc.IsDefault, &loc.IsActive, &loc.CreatedAt); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == postgresUniqueViolation {
				return fmt.Errorf("%w: a location with this code already exists in this warehouse", ErrInvalidLocation)
			}
			return fmt.Errorf("insert warehouse location: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, loc.ID, locationAuditEntityType, createAuditAction, map[string]any{"code": code, "warehouseId": warehouseID})
	})
	if err != nil {
		return WarehouseLocation{}, err
	}
	return loc, nil
}

// UpdateLocationInput is UpdateLocation's partial-update input.
type UpdateLocationInput struct {
	Name     *string
	Type     *string
	IsActive *bool
}

// UpdateLocation applies input's changes to locationID. Owner-gated. Code
// and ParentID are immutable once created (same "identity/structural
// field" judgment call CreateLocation's own doc comment gives for why
// parent validation only happens at creation time).
func UpdateLocation(ctx context.Context, pool *pgxpool.Pool, firmID, userID, locationID uuid.UUID, input UpdateLocationInput) (WarehouseLocation, error) {
	var typ *string
	if input.Type != nil {
		trimmed := strings.TrimSpace(*input.Type)
		if !locationTypes[trimmed] {
			return WarehouseLocation{}, fmt.Errorf("%w: type %q is not one of aisle/shelf/bin/staging/dispatch", ErrInvalidLocation, trimmed)
		}
		typ = &trimmed
	}

	var loc WarehouseLocation
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

		row := tx.QueryRow(ctx, `
			UPDATE warehouse_locations SET
				name = CASE WHEN $1::boolean THEN $2 ELSE name END,
				type = COALESCE($3, type),
				is_active = COALESCE($4, is_active)
			WHERE id = $5 AND firm_id = $6
			RETURNING id, firm_id, warehouse_id, code, name, type, parent_id, is_default, is_active, created_at
		`, input.Name != nil, optionalString(derefOr(input.Name, "")), typ, input.IsActive, locationID, firmID)
		loc, err = scanLocationRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLocationNotFound
		}
		if err != nil {
			return fmt.Errorf("update warehouse location: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, loc.ID, locationAuditEntityType, updateAuditAction, map[string]any{"code": loc.Code})
	})
	if err != nil {
		return WarehouseLocation{}, err
	}
	return loc, nil
}

func scanLocationRow(row pgx.Row) (WarehouseLocation, error) {
	var l WarehouseLocation
	if err := row.Scan(&l.ID, &l.FirmID, &l.WarehouseID, &l.Code, &l.Name, &l.Type, &l.ParentID, &l.IsDefault, &l.IsActive, &l.CreatedAt); err != nil {
		return WarehouseLocation{}, err
	}
	return l, nil
}

// ListLocations returns warehouseID's locations within firmID, ordered
// by code - a flat list, not a nested tree: parent_id is enough for a
// caller (the frontend's collapsible aisle -> shelf -> bin view) to build
// the tree client-side, the same "flat rows, client builds hierarchy"
// shape internal/workflow.DefinitionSpec's states/transitions already use
// for their own graph structure. Member-gated.
func ListLocations(ctx context.Context, pool *pgxpool.Pool, firmID, userID, warehouseID uuid.UUID) ([]WarehouseLocation, error) {
	var locations []WarehouseLocation
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, warehouse_id, code, name, type, parent_id, is_default, is_active, created_at
			FROM warehouse_locations WHERE firm_id = $1 AND warehouse_id = $2 ORDER BY code
		`, firmID, warehouseID)
		if err != nil {
			return fmt.Errorf("list warehouse locations: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			l, err := scanLocationRow(rows)
			if err != nil {
				return err
			}
			locations = append(locations, l)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return locations, nil
}

// LocationStock is one product's on-hand quantity at one location within
// a warehouse - GetWarehouseStock's own row shape.
type LocationStock struct {
	LocationID   uuid.UUID
	LocationCode string
	ProductID    uuid.UUID
	ProductSKU   string
	Quantity     string
}

// GetWarehouseStock returns every (location, product) on-hand quantity
// within warehouseID - a direct SQL join across stock_levels/products
// (internal/inventory's own tables) and this package's own
// warehouse_locations, the same "cross-cutting reader joins directly
// instead of looping the owning module's exported functions" precedent
// internal/reports.kpi.go already establishes for its own inventory KPIs
// (an N+1 of one GetStock call per product would be far worse here, and
// this is a read-only reporting view, not a write). Member-gated.
func GetWarehouseStock(ctx context.Context, pool *pgxpool.Pool, firmID, userID, warehouseID uuid.UUID) ([]LocationStock, error) {
	var results []LocationStock
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var warehouseExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND firm_id = $2)`, warehouseID, firmID).Scan(&warehouseExists); err != nil {
			return fmt.Errorf("look up warehouse: %w", err)
		}
		if !warehouseExists {
			return ErrWarehouseNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT wl.id, wl.code, p.id, p.sku, sl.quantity::text
			FROM stock_levels sl
			JOIN warehouse_locations wl ON wl.id = sl.location_id
			JOIN products p ON p.id = sl.product_id
			WHERE sl.firm_id = $1 AND wl.warehouse_id = $2
			ORDER BY wl.code, p.sku
		`, firmID, warehouseID)
		if err != nil {
			return fmt.Errorf("get warehouse stock: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var s LocationStock
			if err := rows.Scan(&s.LocationID, &s.LocationCode, &s.ProductID, &s.ProductSKU, &s.Quantity); err != nil {
				return err
			}
			results = append(results, s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func optionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func derefOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}
