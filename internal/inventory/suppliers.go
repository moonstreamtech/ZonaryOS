// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const supplierAuditEntityType = "supplier"

const (
	createSupplierAuditAction = "create"
	updateSupplierAuditAction = "update"
)

const defaultSupplierCurrency = "TRY"

// Supplier is one suppliers row.
type Supplier struct {
	ID           uuid.UUID
	FirmID       uuid.UUID
	Name         string
	ContactName  *string
	Email        *string
	Phone        *string
	Address      *string
	Currency     string
	PaymentTerms *string
	CustomFields map[string]any
	IsActive     bool
	CreatedAt    time.Time
}

// CreateSupplierInput is CreateSupplier's request shape.
type CreateSupplierInput struct {
	Name         string
	ContactName  string
	Email        string
	Phone        string
	Address      string
	Currency     string
	PaymentTerms string
	CustomFields map[string]any
}

// CreateSupplier adds a new supplier to firmID. Owner-gated: adding a
// supplier record is a structural firm decision, the same tier as
// internal/hr.CreatePerson.
func CreateSupplier(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateSupplierInput) (Supplier, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Supplier{}, fmt.Errorf("%w: name must not be empty", ErrInvalidSupplier)
	}
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = defaultSupplierCurrency
	}
	customFields := input.CustomFields
	if customFields == nil {
		customFields = map[string]any{}
	}
	customFieldsJSON, err := json.Marshal(customFields)
	if err != nil {
		return Supplier{}, fmt.Errorf("marshal custom fields: %w", err)
	}

	var supplier Supplier
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

		supplier = Supplier{
			FirmID: firmID, Name: name, ContactName: optionalString(input.ContactName),
			Email: optionalString(input.Email), Phone: optionalString(input.Phone),
			Address: optionalString(input.Address), Currency: currency,
			PaymentTerms: optionalString(input.PaymentTerms), CustomFields: customFields, IsActive: true,
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO suppliers (firm_id, name, contact_name, email, phone, address, currency, payment_terms, custom_fields)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING id, created_at
		`, firmID, name, supplier.ContactName, supplier.Email, supplier.Phone, supplier.Address, currency, supplier.PaymentTerms, customFieldsJSON,
		).Scan(&supplier.ID, &supplier.CreatedAt); err != nil {
			return fmt.Errorf("insert supplier: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, supplier.ID, supplierAuditEntityType, createSupplierAuditAction, map[string]any{"name": name})
	})
	if err != nil {
		return Supplier{}, err
	}
	return supplier, nil
}

// SupplierUpdate is UpdateSupplier's partial-update input, same
// nil-leaves-unchanged convention as ProductUpdate/PersonUpdate.
type SupplierUpdate struct {
	Name         *string
	ContactName  *string
	Email        *string
	Phone        *string
	Address      *string
	Currency     *string
	PaymentTerms *string
	IsActive     *bool
	CustomFields map[string]any
}

// UpdateSupplier changes supplierID's fields within firmID. Owner-gated,
// same tier as CreateSupplier.
func UpdateSupplier(ctx context.Context, pool *pgxpool.Pool, firmID, userID, supplierID uuid.UUID, update SupplierUpdate) (Supplier, error) {
	if update.Name != nil && strings.TrimSpace(*update.Name) == "" {
		return Supplier{}, fmt.Errorf("%w: name must not be empty", ErrInvalidSupplier)
	}

	var customFieldsJSON []byte
	if update.CustomFields != nil {
		var err error
		customFieldsJSON, err = json.Marshal(update.CustomFields)
		if err != nil {
			return Supplier{}, fmt.Errorf("marshal custom fields: %w", err)
		}
	}

	var supplier Supplier
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
			UPDATE suppliers SET
				name = COALESCE($1, name),
				contact_name = CASE WHEN $2::boolean THEN $3 ELSE contact_name END,
				email = CASE WHEN $4::boolean THEN $5 ELSE email END,
				phone = CASE WHEN $6::boolean THEN $7 ELSE phone END,
				address = CASE WHEN $8::boolean THEN $9 ELSE address END,
				currency = COALESCE($10, currency),
				payment_terms = CASE WHEN $11::boolean THEN $12 ELSE payment_terms END,
				is_active = COALESCE($13, is_active),
				custom_fields = COALESCE($14, custom_fields)
			WHERE id = $15 AND firm_id = $16
			RETURNING id, firm_id, name, contact_name, email, phone, address, currency, payment_terms, custom_fields, is_active, created_at
		`,
			update.Name,
			update.ContactName != nil, optionalString(derefOr(update.ContactName, "")),
			update.Email != nil, optionalString(derefOr(update.Email, "")),
			update.Phone != nil, optionalString(derefOr(update.Phone, "")),
			update.Address != nil, optionalString(derefOr(update.Address, "")),
			update.Currency,
			update.PaymentTerms != nil, optionalString(derefOr(update.PaymentTerms, "")),
			update.IsActive,
			nullableJSON(customFieldsJSON),
			supplierID, firmID,
		).Scan(&supplier.ID, &supplier.FirmID, &supplier.Name, &supplier.ContactName, &supplier.Email, &supplier.Phone,
			&supplier.Address, &supplier.Currency, &supplier.PaymentTerms, &customFieldsRaw, &supplier.IsActive, &supplier.CreatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSupplierNotFound
			}
			return fmt.Errorf("update supplier: %w", err)
		}
		if err := json.Unmarshal(customFieldsRaw, &supplier.CustomFields); err != nil {
			return fmt.Errorf("unmarshal custom fields: %w", err)
		}

		changes := map[string]any{}
		if update.Name != nil {
			changes["name"] = *update.Name
		}
		if update.IsActive != nil {
			changes["isActive"] = *update.IsActive
		}
		return auditlog.Write(ctx, tx, firmID, userID, supplier.ID, supplierAuditEntityType, updateSupplierAuditAction, changes)
	})
	if err != nil {
		return Supplier{}, err
	}
	return supplier, nil
}

// ListSuppliersOptions filters ListSuppliers - zero value returns every
// supplier.
type ListSuppliersOptions struct {
	ActiveOnly bool
}

// ListSuppliers returns firmID's suppliers, ordered by name. Member-gated
// (not owner-gated), same tier as ListProducts.
func ListSuppliers(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListSuppliersOptions) ([]Supplier, error) {
	var suppliers []Supplier
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, name, contact_name, email, phone, address, currency, payment_terms, custom_fields, is_active, created_at
			FROM suppliers
			WHERE ($1 = false OR is_active)
			ORDER BY name
		`, opts.ActiveOnly)
		if err != nil {
			return fmt.Errorf("list suppliers: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var s Supplier
			var customFieldsRaw []byte
			if err := rows.Scan(&s.ID, &s.FirmID, &s.Name, &s.ContactName, &s.Email, &s.Phone, &s.Address,
				&s.Currency, &s.PaymentTerms, &customFieldsRaw, &s.IsActive, &s.CreatedAt); err != nil {
				return err
			}
			if err := json.Unmarshal(customFieldsRaw, &s.CustomFields); err != nil {
				return fmt.Errorf("unmarshal custom fields: %w", err)
			}
			suppliers = append(suppliers, s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return suppliers, nil
}

// GetSupplier resolves one supplier by id within firmID. Member-gated,
// same tier as ListSuppliers/GetProduct.
func GetSupplier(ctx context.Context, pool *pgxpool.Pool, firmID, userID, supplierID uuid.UUID) (Supplier, error) {
	var supplier Supplier
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
			SELECT id, firm_id, name, contact_name, email, phone, address, currency, payment_terms, custom_fields, is_active, created_at
			FROM suppliers WHERE id = $1 AND firm_id = $2
		`, supplierID, firmID).Scan(&supplier.ID, &supplier.FirmID, &supplier.Name, &supplier.ContactName, &supplier.Email,
			&supplier.Phone, &supplier.Address, &supplier.Currency, &supplier.PaymentTerms, &customFieldsRaw, &supplier.IsActive, &supplier.CreatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSupplierNotFound
			}
			return fmt.Errorf("look up supplier: %w", err)
		}
		return json.Unmarshal(customFieldsRaw, &supplier.CustomFields)
	})
	if err != nil {
		return Supplier{}, err
	}
	return supplier, nil
}

// SupplierExistsTx reports whether supplierID is a real suppliers row
// within firmID - the cross-module check internal/workflow's
// FieldTypeSupplier payload validation uses (checkSupplierField,
// engine.go), same shape as ProductExistsTx/hr.PersonExistsTx.
//
// ciaudit:ignore-firmid-check: internal cross-package helper, called only
// by internal/workflow.checkSupplierField, itself only reachable from
// CreateInstance after permission.IsMember/Has have already run in the
// same transaction - firmID here scopes the query (defense in depth
// alongside RLS), it is not itself an authorization decision.
func SupplierExistsTx(ctx context.Context, tx pgx.Tx, firmID, supplierID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM suppliers WHERE id = $1 AND firm_id = $2)
	`, supplierID, firmID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check supplier exists: %w", err)
	}
	return exists, nil
}
