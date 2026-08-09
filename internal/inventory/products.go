// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package inventory is the structural layer under the existing
// stock_to_sale/purchase_order workflows: real products, per-location
// stock levels, an immutable movement log, and suppliers - not just a
// freeform "item" string in a workflow instance's payload (see
// migrations/0011_inventory_core.up.sql's own scope-boundary comment for
// what stays deliberately out of scope: warehouse picking/packing,
// barcode scanning, multi-warehouse transfer workflows, expiry/batch/lot
// tracking).
//
// Authorization follows this codebase's usual discipline
// (docs/DEVELOPMENT.md's Authorization checklist): IsMember before any
// read, IsOwner before any product/supplier write - the same tier as
// internal/accounting's chart-of-accounts mutations and internal/hr's
// people/contracts. Stock-level changes themselves (AdjustStockTx,
// stock.go) are different: they're driven by the workflow-to-inventory
// bridge (internal/workflow's record_sale/receive hooks), which has
// already checked its own transition's permission - see AdjustStockTx's
// own doc comment.
package inventory

import (
	"context"
	"encoding/json"
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

// postgresUniqueViolation is the SQLSTATE code Postgres raises for a
// UNIQUE constraint violation - same constant/reasoning as every other
// package in this codebase that redeclares it locally rather than
// importing across packages just for one string.
const postgresUniqueViolation = "23505"

const productAuditEntityType = "product"

const (
	createProductAuditAction = "create"
	updateProductAuditAction = "update"
)

// Product is one products row.
type Product struct {
	ID           uuid.UUID
	FirmID       uuid.UUID
	SKU          string
	Name         string
	Description  *string
	Unit         string
	UnitPrice    *string
	CostPrice    *string
	TaxRate      *string
	Category     *string
	MinQuantity  string
	CustomFields map[string]any
	IsActive     bool
	CreatedAt    time.Time
}

func optionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// decimalOrNil validates s against amountPattern (empty means "not set" -
// NULL) - shared by unit_price/cost_price (numeric(19,4)) and
// min_quantity/tax_rate's own looser validation below.
func decimalOrNil(s string) (*string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if !amountPattern.MatchString(s) {
		return nil, fmt.Errorf("%w: %q must be a positive decimal with at most 4 fraction digits", ErrInvalidProduct, s)
	}
	return &s, nil
}

// CreateProductInput is CreateProduct's request shape.
type CreateProductInput struct {
	SKU          string
	Name         string
	Description  string
	Unit         string
	UnitPrice    string // decimal string, optional
	CostPrice    string // decimal string, optional
	TaxRate      string // decimal string, optional (percentage, e.g. "18.00")
	Category     string
	MinQuantity  string // decimal string, optional (defaults to "0")
	CustomFields map[string]any
}

const defaultUnit = "piece"

// CreateProduct adds a new product to firmID. Owner-gated: defining a
// firm's product catalog is a structural firm decision, the same tier as
// internal/accounting.CreateAccount.
func CreateProduct(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateProductInput) (Product, error) {
	sku := strings.TrimSpace(input.SKU)
	name := strings.TrimSpace(input.Name)
	if sku == "" {
		return Product{}, fmt.Errorf("%w: sku must not be empty", ErrInvalidProduct)
	}
	if name == "" {
		return Product{}, fmt.Errorf("%w: name must not be empty", ErrInvalidProduct)
	}
	unit := strings.TrimSpace(input.Unit)
	if unit == "" {
		unit = defaultUnit
	}
	unitPrice, err := decimalOrNil(input.UnitPrice)
	if err != nil {
		return Product{}, err
	}
	costPrice, err := decimalOrNil(input.CostPrice)
	if err != nil {
		return Product{}, err
	}
	taxRate, err := decimalOrNil(input.TaxRate)
	if err != nil {
		return Product{}, err
	}
	minQuantity := strings.TrimSpace(input.MinQuantity)
	if minQuantity == "" {
		minQuantity = "0"
	} else if !amountPattern.MatchString(minQuantity) {
		return Product{}, fmt.Errorf("%w: minQuantity %q must be a positive decimal with at most 4 fraction digits", ErrInvalidProduct, minQuantity)
	}
	customFields := input.CustomFields
	if customFields == nil {
		customFields = map[string]any{}
	}
	customFieldsJSON, err := json.Marshal(customFields)
	if err != nil {
		return Product{}, fmt.Errorf("marshal custom fields: %w", err)
	}

	var product Product
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

		product = Product{
			FirmID: firmID, SKU: sku, Name: name, Description: optionalString(input.Description),
			Unit: unit, UnitPrice: unitPrice, CostPrice: costPrice, TaxRate: taxRate,
			Category: optionalString(input.Category), MinQuantity: minQuantity, CustomFields: customFields, IsActive: true,
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO products (firm_id, sku, name, description, unit, unit_price, cost_price, tax_rate, category, min_quantity, custom_fields)
			VALUES ($1, $2, $3, $4, $5, $6::numeric, $7::numeric, $8::numeric, $9, $10::numeric, $11)
			RETURNING id, created_at
		`, firmID, sku, name, product.Description, unit, unitPrice, costPrice, taxRate, product.Category, minQuantity, customFieldsJSON,
		).Scan(&product.ID, &product.CreatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == postgresUniqueViolation {
				return ErrProductSKUExists
			}
			return fmt.Errorf("insert product: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, product.ID, productAuditEntityType, createProductAuditAction, map[string]any{
			"sku": sku, "name": name,
		})
	})
	if err != nil {
		return Product{}, err
	}
	return product, nil
}

// CreateProductTx is CreateProduct's already-open-transaction counterpart,
// for callers (internal/portability's configuration import) that need
// product creation as one step inside a larger atomic operation - the
// same "*Tx, no authorization of its own, shared by every entry point"
// pattern internal/workflow.DefineWorkflowTx already establishes. Returns
// ErrProductSKUExists on a (firm_id, sku) collision, the exact same
// sentinel CreateProduct itself returns - the caller decides whether
// that's a hard failure or (as configuration import does) a natural
// "already imported, skip" signal.
//
// ciaudit:ignore-firmid-check: shared product-creation primitive; every
// caller has already run its own authorization check before calling this.
func CreateProductTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, input CreateProductInput) (Product, error) {
	sku := strings.TrimSpace(input.SKU)
	name := strings.TrimSpace(input.Name)
	if sku == "" {
		return Product{}, fmt.Errorf("%w: sku must not be empty", ErrInvalidProduct)
	}
	if name == "" {
		return Product{}, fmt.Errorf("%w: name must not be empty", ErrInvalidProduct)
	}
	unit := strings.TrimSpace(input.Unit)
	if unit == "" {
		unit = defaultUnit
	}
	unitPrice, err := decimalOrNil(input.UnitPrice)
	if err != nil {
		return Product{}, err
	}
	costPrice, err := decimalOrNil(input.CostPrice)
	if err != nil {
		return Product{}, err
	}
	taxRate, err := decimalOrNil(input.TaxRate)
	if err != nil {
		return Product{}, err
	}
	minQuantity := strings.TrimSpace(input.MinQuantity)
	if minQuantity == "" {
		minQuantity = "0"
	} else if !amountPattern.MatchString(minQuantity) {
		return Product{}, fmt.Errorf("%w: minQuantity %q must be a positive decimal with at most 4 fraction digits", ErrInvalidProduct, minQuantity)
	}
	customFields := input.CustomFields
	if customFields == nil {
		customFields = map[string]any{}
	}
	customFieldsJSON, err := json.Marshal(customFields)
	if err != nil {
		return Product{}, fmt.Errorf("marshal custom fields: %w", err)
	}

	product := Product{
		FirmID: firmID, SKU: sku, Name: name, Description: optionalString(input.Description),
		Unit: unit, UnitPrice: unitPrice, CostPrice: costPrice, TaxRate: taxRate,
		Category: optionalString(input.Category), MinQuantity: minQuantity, CustomFields: customFields, IsActive: true,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO products (firm_id, sku, name, description, unit, unit_price, cost_price, tax_rate, category, min_quantity, custom_fields)
		VALUES ($1, $2, $3, $4, $5, $6::numeric, $7::numeric, $8::numeric, $9, $10::numeric, $11)
		RETURNING id, created_at
	`, firmID, sku, name, product.Description, unit, unitPrice, costPrice, taxRate, product.Category, minQuantity, customFieldsJSON,
	).Scan(&product.ID, &product.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == postgresUniqueViolation {
			return Product{}, ErrProductSKUExists
		}
		return Product{}, fmt.Errorf("insert product: %w", err)
	}
	return product, nil
}

// ProductUpdate is UpdateProduct's partial-update input - nil leaves that
// column unchanged, matching internal/accounting.AccountUpdate/internal/hr.PersonUpdate's
// convention. SKU is immutable once created (same "identity field"
// judgment call as an account's code).
type ProductUpdate struct {
	Name         *string
	Description  *string
	Unit         *string
	UnitPrice    *string
	CostPrice    *string
	TaxRate      *string
	Category     *string
	MinQuantity  *string
	IsActive     *bool
	CustomFields map[string]any
}

// UpdateProduct changes productID's mutable fields within firmID.
// Owner-gated, same tier as CreateProduct.
func UpdateProduct(ctx context.Context, pool *pgxpool.Pool, firmID, userID, productID uuid.UUID, update ProductUpdate) (Product, error) {
	if update.Name != nil && strings.TrimSpace(*update.Name) == "" {
		return Product{}, fmt.Errorf("%w: name must not be empty", ErrInvalidProduct)
	}

	var unitPrice, costPrice, taxRate, minQuantity *string
	var priceSet, costSet, taxSet, minQtySet bool
	if update.UnitPrice != nil {
		priceSet = true
		v, err := decimalOrNil(*update.UnitPrice)
		if err != nil {
			return Product{}, err
		}
		unitPrice = v
	}
	if update.CostPrice != nil {
		costSet = true
		v, err := decimalOrNil(*update.CostPrice)
		if err != nil {
			return Product{}, err
		}
		costPrice = v
	}
	if update.TaxRate != nil {
		taxSet = true
		v, err := decimalOrNil(*update.TaxRate)
		if err != nil {
			return Product{}, err
		}
		taxRate = v
	}
	if update.MinQuantity != nil {
		minQtySet = true
		trimmed := strings.TrimSpace(*update.MinQuantity)
		if trimmed != "" && !amountPattern.MatchString(trimmed) {
			return Product{}, fmt.Errorf("%w: minQuantity %q must be a positive decimal", ErrInvalidProduct, trimmed)
		}
		if trimmed == "" {
			trimmed = "0"
		}
		minQuantity = &trimmed
	}

	var customFieldsJSON []byte
	if update.CustomFields != nil {
		var err error
		customFieldsJSON, err = json.Marshal(update.CustomFields)
		if err != nil {
			return Product{}, fmt.Errorf("marshal custom fields: %w", err)
		}
	}

	var unitParam *string
	if update.Unit != nil {
		trimmed := strings.TrimSpace(*update.Unit)
		if trimmed == "" {
			trimmed = defaultUnit
		}
		unitParam = &trimmed
	}

	var product Product
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

		var customFieldsRaw []byte
		err = tx.QueryRow(ctx, `
			UPDATE products SET
				name = COALESCE($1, name),
				description = CASE WHEN $2::boolean THEN $3 ELSE description END,
				unit = COALESCE($4, unit),
				unit_price = CASE WHEN $5::boolean THEN $6::numeric ELSE unit_price END,
				cost_price = CASE WHEN $7::boolean THEN $8::numeric ELSE cost_price END,
				tax_rate = CASE WHEN $9::boolean THEN $10::numeric ELSE tax_rate END,
				category = CASE WHEN $11::boolean THEN $12 ELSE category END,
				min_quantity = CASE WHEN $13::boolean THEN $14::numeric ELSE min_quantity END,
				is_active = COALESCE($15, is_active),
				custom_fields = COALESCE($16, custom_fields)
			WHERE id = $17 AND firm_id = $18
			RETURNING id, firm_id, sku, name, description, unit, unit_price::text, cost_price::text, tax_rate::text, category, min_quantity::text, custom_fields, is_active, created_at
		`,
			update.Name,
			update.Description != nil, optionalString(derefOr(update.Description, "")),
			unitParam,
			priceSet, unitPrice,
			costSet, costPrice,
			taxSet, taxRate,
			update.Category != nil, optionalString(derefOr(update.Category, "")),
			minQtySet, minQuantity,
			update.IsActive,
			nullableJSON(customFieldsJSON),
			productID, firmID,
		).Scan(&product.ID, &product.FirmID, &product.SKU, &product.Name, &product.Description, &product.Unit,
			&product.UnitPrice, &product.CostPrice, &product.TaxRate, &product.Category, &product.MinQuantity,
			&customFieldsRaw, &product.IsActive, &product.CreatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrProductNotFound
			}
			return fmt.Errorf("update product: %w", err)
		}
		if err := json.Unmarshal(customFieldsRaw, &product.CustomFields); err != nil {
			return fmt.Errorf("unmarshal custom fields: %w", err)
		}

		changes := map[string]any{}
		if update.Name != nil {
			changes["name"] = *update.Name
		}
		if update.IsActive != nil {
			changes["isActive"] = *update.IsActive
		}
		return auditlog.Write(ctx, tx, firmID, userID, product.ID, productAuditEntityType, updateProductAuditAction, changes)
	})
	if err != nil {
		return Product{}, err
	}
	return product, nil
}

func derefOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

func nullableJSON(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	return data
}

// ListProductsOptions filters ListProducts - a zero value returns every
// product, matching this codebase's usual convention.
type ListProductsOptions struct {
	// ActiveOnly, when true, keeps only is_active products.
	ActiveOnly bool
}

// ListProducts returns firmID's products, ordered by sku. Member-gated
// (not owner-gated): reading the catalog is ordinary firm data
// visibility, the same tier as internal/accounting.ListAccounts.
func ListProducts(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListProductsOptions) ([]Product, error) {
	var products []Product
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, sku, name, description, unit, unit_price::text, cost_price::text, tax_rate::text, category, min_quantity::text, custom_fields, is_active, created_at
			FROM products
			WHERE ($1 = false OR is_active)
			ORDER BY sku
		`, opts.ActiveOnly)
		if err != nil {
			return fmt.Errorf("list products: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var p Product
			var customFieldsRaw []byte
			if err := rows.Scan(&p.ID, &p.FirmID, &p.SKU, &p.Name, &p.Description, &p.Unit, &p.UnitPrice, &p.CostPrice,
				&p.TaxRate, &p.Category, &p.MinQuantity, &customFieldsRaw, &p.IsActive, &p.CreatedAt); err != nil {
				return err
			}
			if err := json.Unmarshal(customFieldsRaw, &p.CustomFields); err != nil {
				return fmt.Errorf("unmarshal custom fields: %w", err)
			}
			products = append(products, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return products, nil
}

// GetProduct resolves one product by id within firmID. Member-gated,
// same tier as ListProducts.
func GetProduct(ctx context.Context, pool *pgxpool.Pool, firmID, userID, productID uuid.UUID) (Product, error) {
	var product Product
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var customFieldsRaw []byte
		err = tx.QueryRow(ctx, `
			SELECT id, firm_id, sku, name, description, unit, unit_price::text, cost_price::text, tax_rate::text, category, min_quantity::text, custom_fields, is_active, created_at
			FROM products WHERE id = $1 AND firm_id = $2
		`, productID, firmID).Scan(&product.ID, &product.FirmID, &product.SKU, &product.Name, &product.Description, &product.Unit,
			&product.UnitPrice, &product.CostPrice, &product.TaxRate, &product.Category, &product.MinQuantity,
			&customFieldsRaw, &product.IsActive, &product.CreatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrProductNotFound
			}
			return fmt.Errorf("look up product: %w", err)
		}
		return json.Unmarshal(customFieldsRaw, &product.CustomFields)
	})
	if err != nil {
		return Product{}, err
	}
	return product, nil
}

// ProductExistsTx reports whether productID is a real products row
// within firmID - the cross-module check internal/workflow's
// FieldTypeProduct payload validation uses (checkProductField,
// engine.go), the same shape hr.PersonExistsTx already established for
// FieldTypePerson.
//
// ciaudit:ignore-firmid-check: internal cross-package helper, called only
// by internal/workflow.checkProductField, itself only reachable from
// CreateInstance after permission.IsMember/Has have already run in the
// same transaction - firmID here scopes the query (defense in depth
// alongside RLS), it is not itself an authorization decision.
func ProductExistsTx(ctx context.Context, tx pgx.Tx, firmID, productID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM products WHERE id = $1 AND firm_id = $2)
	`, productID, firmID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check product exists: %w", err)
	}
	return exists, nil
}
