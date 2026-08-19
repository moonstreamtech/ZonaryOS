// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package crm is the structural layer behind the existing
// customer_pipeline workflow's "customer" state: a real customer record,
// not just a state a workflow instance happens to sit in. Customers are
// created two ways: directly, owner-gated, via CreateCustomer; or
// automatically, by internal/workflow's workflow-to-CRM bridge
// (TransitionSpec.Customer, see spec.go's own doc comment) via
// CreateCustomerTx, when customer_pipeline's "convert" transition fires
// with a resolvable name. source_workflow_instance distinguishes the
// latter from the former - see migrations/0013_logistics_crm_core.up.sql's
// own doc comment. See this batch's own scope-boundary note for what
// stays out of scope: no invoicing, no loyalty points or pricing tiers.
//
// Authorization follows this codebase's usual discipline: IsMember before
// any read, IsOwner before any direct write - the same tier as
// internal/inventory's product/supplier mutations. CreateCustomerTx
// itself is different: it's driven by the workflow-to-CRM bridge, which
// has already checked its own transition's permission - see its own doc
// comment.
package crm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/webhook"
)

const customerAuditEntityType = "customer"

const (
	createCustomerAuditAction = "create"
	updateCustomerAuditAction = "update"
)

const defaultCustomerCurrency = "TRY"

// amountPattern mirrors internal/accounting's/internal/inventory's own
// amountPattern - a plain positive decimal string, matching
// customers.credit_limit's numeric(19,4) column. Redeclared here rather
// than imported cross-package, same reasoning each of those packages'
// own amountPattern gives.
var amountPattern = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,4})?$`)

func optionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// decimalOrNil validates s against amountPattern (empty means "not set" -
// NULL) - shared by credit_limit.
func decimalOrNil(s string) (*string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if !amountPattern.MatchString(s) {
		return nil, fmt.Errorf("%w: %q must be a positive decimal with at most 4 fraction digits", ErrInvalidCustomer, s)
	}
	return &s, nil
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

// Customer is one customers row.
type Customer struct {
	ID                     uuid.UUID
	FirmID                 uuid.UUID
	Name                   string
	Email                  *string
	Phone                  *string
	Address                *string
	TaxID                  *string
	CreditLimit            *string
	Currency               string
	CustomFields           map[string]any
	SourceWorkflowInstance *uuid.UUID
	CreatedAt              time.Time
	// UpdatedAt (mobile API optimization batch) backs ?updated_since=
	// delta sync - bumped to now() on every UpdateCustomer, left at its
	// insert-time default (== CreatedAt) otherwise.
	UpdatedAt time.Time
	// TotalInvoiced/TotalPaid are virtual fields (Part 3 of the search/
	// filtering/enrichment batch): TotalInvoiced is SUM(invoices.total)
	// for invoices linked to this customer, TotalPaid is SUM(payments.amount)
	// across those same invoices' payments - both computed at read time
	// via one extra query each, never stored. Only ever populated by
	// GetCustomer - same "detail response only, not list" discipline as
	// Product.StockQuantity/Invoice.TotalPaid, for the same N+1 reason.
	TotalInvoiced *string
	TotalPaid     *string
}

// CreateCustomerInput is CreateCustomer's request shape.
type CreateCustomerInput struct {
	Name         string
	Email        string
	Phone        string
	Address      string
	TaxID        string
	CreditLimit  string // decimal string, optional
	Currency     string
	CustomFields map[string]any
}

// CreateCustomer adds a new customer to firmID, with no
// SourceWorkflowInstance (a manually-created customer, not one converted
// from the CRM pipeline - see this package's own doc comment). Owner-gated:
// adding a customer record directly is a structural firm decision, the
// same tier as internal/inventory.CreateSupplier.
func CreateCustomer(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateCustomerInput) (Customer, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Customer{}, fmt.Errorf("%w: name must not be empty", ErrInvalidCustomer)
	}
	creditLimit, err := decimalOrNil(input.CreditLimit)
	if err != nil {
		return Customer{}, err
	}
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = defaultCustomerCurrency
	}
	customFields := input.CustomFields
	if customFields == nil {
		customFields = map[string]any{}
	}
	customFieldsJSON, err := json.Marshal(customFields)
	if err != nil {
		return Customer{}, fmt.Errorf("marshal custom fields: %w", err)
	}

	var customer Customer
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

		customer = Customer{
			FirmID: firmID, Name: name, Email: optionalString(input.Email), Phone: optionalString(input.Phone),
			Address: optionalString(input.Address), TaxID: optionalString(input.TaxID),
			CreditLimit: creditLimit, Currency: currency, CustomFields: customFields,
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO customers (firm_id, name, email, phone, address, tax_id, credit_limit, currency, custom_fields)
			VALUES ($1, $2, $3, $4, $5, $6, $7::numeric, $8, $9)
			RETURNING id, created_at
		`, firmID, name, customer.Email, customer.Phone, customer.Address, customer.TaxID, creditLimit, currency, customFieldsJSON,
		).Scan(&customer.ID, &customer.CreatedAt); err != nil {
			return fmt.Errorf("insert customer: %w", err)
		}
		customer.UpdatedAt = customer.CreatedAt

		return auditlog.Write(ctx, tx, firmID, userID, customer.ID, customerAuditEntityType, createCustomerAuditAction, map[string]any{"name": name})
	})
	if err != nil {
		return Customer{}, err
	}

	// webhook.Dispatch (the generic, event-subscription-based webhooks
	// system) and outboundDispatch (data pipeline/ETL/system integrations
	// batch's own field-mapped entity-sync push) run side by side here,
	// after commit - see internal/inventory.CreateProduct's identical
	// pair of calls for the same "two independent consumers, neither a
	// replacement for the other" reasoning.
	webhook.Dispatch(pool, firmID, webhook.EventCustomerCreated, map[string]any{
		"customerId": customer.ID.String(), "name": name,
	})
	outboundDispatch(pool, firmID, "customers", map[string]any{
		"id": customer.ID.String(), "name": customer.Name, "email": derefStrAny(customer.Email),
		"phone": derefStrAny(customer.Phone), "address": derefStrAny(customer.Address), "taxId": derefStrAny(customer.TaxID),
		"creditLimit": derefStrAny(customer.CreditLimit), "currency": customer.Currency,
	})

	return customer, nil
}

// CustomerUpdate is UpdateCustomer's partial-update input - nil leaves
// that column unchanged, matching internal/inventory.SupplierUpdate's
// convention.
type CustomerUpdate struct {
	Name         *string
	Email        *string
	Phone        *string
	Address      *string
	TaxID        *string
	CreditLimit  *string
	Currency     *string
	CustomFields map[string]any
}

// UpdateCustomer changes customerID's fields within firmID. Owner-gated,
// same tier as CreateCustomer.
func UpdateCustomer(ctx context.Context, pool *pgxpool.Pool, firmID, userID, customerID uuid.UUID, update CustomerUpdate) (Customer, error) {
	if update.Name != nil && strings.TrimSpace(*update.Name) == "" {
		return Customer{}, fmt.Errorf("%w: name must not be empty", ErrInvalidCustomer)
	}

	var creditLimit *string
	var creditLimitSet bool
	if update.CreditLimit != nil {
		creditLimitSet = true
		v, err := decimalOrNil(*update.CreditLimit)
		if err != nil {
			return Customer{}, err
		}
		creditLimit = v
	}

	var customFieldsJSON []byte
	if update.CustomFields != nil {
		var err error
		customFieldsJSON, err = json.Marshal(update.CustomFields)
		if err != nil {
			return Customer{}, fmt.Errorf("marshal custom fields: %w", err)
		}
	}

	var customer Customer
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
			UPDATE customers SET
				name = COALESCE($1, name),
				email = CASE WHEN $2::boolean THEN $3 ELSE email END,
				phone = CASE WHEN $4::boolean THEN $5 ELSE phone END,
				address = CASE WHEN $6::boolean THEN $7 ELSE address END,
				tax_id = CASE WHEN $8::boolean THEN $9 ELSE tax_id END,
				credit_limit = CASE WHEN $10::boolean THEN $11::numeric ELSE credit_limit END,
				currency = COALESCE($12, currency),
				custom_fields = COALESCE($13, custom_fields),
				updated_at = now()
			WHERE id = $14 AND firm_id = $15
			RETURNING id, firm_id, name, email, phone, address, tax_id, credit_limit::text, currency, custom_fields, source_workflow_instance, created_at, updated_at
		`,
			update.Name,
			update.Email != nil, optionalString(derefOr(update.Email, "")),
			update.Phone != nil, optionalString(derefOr(update.Phone, "")),
			update.Address != nil, optionalString(derefOr(update.Address, "")),
			update.TaxID != nil, optionalString(derefOr(update.TaxID, "")),
			creditLimitSet, creditLimit,
			update.Currency,
			nullableJSON(customFieldsJSON),
			customerID, firmID,
		).Scan(&customer.ID, &customer.FirmID, &customer.Name, &customer.Email, &customer.Phone, &customer.Address,
			&customer.TaxID, &customer.CreditLimit, &customer.Currency, &customFieldsRaw, &customer.SourceWorkflowInstance, &customer.CreatedAt, &customer.UpdatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrCustomerNotFound
			}
			return fmt.Errorf("update customer: %w", err)
		}
		if err := json.Unmarshal(customFieldsRaw, &customer.CustomFields); err != nil {
			return fmt.Errorf("unmarshal custom fields: %w", err)
		}

		changes := map[string]any{}
		if update.Name != nil {
			changes["name"] = *update.Name
		}
		return auditlog.Write(ctx, tx, firmID, userID, customer.ID, customerAuditEntityType, updateCustomerAuditAction, changes)
	})
	if err != nil {
		return Customer{}, err
	}
	return customer, nil
}

// ListCustomersOptions filters ListCustomers - a zero value returns every
// customer, matching this codebase's usual "zero value means unfiltered"
// convention.
type ListCustomersOptions struct {
	// Search, when non-empty, keeps only customers whose search_tsv
	// (migrations/0024 - name/email/phone) matches this text via
	// full-text search.
	Search string
	// UpdatedSince (mobile API optimization batch), when non-nil, keeps
	// only customers with updated_at >= this timestamp.
	UpdatedSince *time.Time
}

// ListCustomers returns firmID's customers, ordered by name. Member-gated
// (not owner-gated): reading the customer list is ordinary firm data
// visibility, the same tier as internal/inventory.ListSuppliers.
func ListCustomers(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListCustomersOptions) ([]Customer, error) {
	var customers []Customer
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, name, email, phone, address, tax_id, credit_limit::text, currency, custom_fields, source_workflow_instance, created_at, updated_at
			FROM customers
			WHERE ($1 = '' OR search_tsv @@ plainto_tsquery('simple', normalize_search_text($1)))
			  AND ($2::timestamptz IS NULL OR updated_at >= $2)
			ORDER BY name
		`, opts.Search, opts.UpdatedSince)
		if err != nil {
			return fmt.Errorf("list customers: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var c Customer
			var customFieldsRaw []byte
			if err := rows.Scan(&c.ID, &c.FirmID, &c.Name, &c.Email, &c.Phone, &c.Address, &c.TaxID,
				&c.CreditLimit, &c.Currency, &customFieldsRaw, &c.SourceWorkflowInstance, &c.CreatedAt, &c.UpdatedAt); err != nil {
				return err
			}
			if err := json.Unmarshal(customFieldsRaw, &c.CustomFields); err != nil {
				return fmt.Errorf("unmarshal custom fields: %w", err)
			}
			customers = append(customers, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return customers, nil
}

// GetCustomer resolves one customer by id within firmID. Member-gated,
// same tier as ListCustomers.
func GetCustomer(ctx context.Context, pool *pgxpool.Pool, firmID, userID, customerID uuid.UUID) (Customer, error) {
	var customer Customer
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
			SELECT id, firm_id, name, email, phone, address, tax_id, credit_limit::text, currency, custom_fields, source_workflow_instance, created_at, updated_at
			FROM customers WHERE id = $1 AND firm_id = $2
		`, customerID, firmID).Scan(&customer.ID, &customer.FirmID, &customer.Name, &customer.Email, &customer.Phone, &customer.Address,
			&customer.TaxID, &customer.CreditLimit, &customer.Currency, &customFieldsRaw, &customer.SourceWorkflowInstance, &customer.CreatedAt, &customer.UpdatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrCustomerNotFound
			}
			return fmt.Errorf("look up customer: %w", err)
		}
		if err := json.Unmarshal(customFieldsRaw, &customer.CustomFields); err != nil {
			return err
		}

		// TotalInvoiced/TotalPaid: two extra aggregation queries, only
		// for this single customer (GetCustomer is a one-row lookup, not
		// a list) - COALESCE so a customer with no invoices/payments yet
		// reads as "0", not NULL.
		var totalInvoiced string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(total), 0)::text FROM invoices WHERE customer_id = $1 AND firm_id = $2
		`, customerID, firmID).Scan(&totalInvoiced); err != nil {
			return fmt.Errorf("sum total invoiced: %w", err)
		}
		customer.TotalInvoiced = &totalInvoiced

		var totalPaid string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(p.amount), 0)::text
			FROM payments p JOIN invoices i ON i.id = p.invoice_id
			WHERE i.customer_id = $1 AND i.firm_id = $2
		`, customerID, firmID).Scan(&totalPaid); err != nil {
			return fmt.Errorf("sum total paid: %w", err)
		}
		customer.TotalPaid = &totalPaid
		return nil
	})
	if err != nil {
		return Customer{}, err
	}
	return customer, nil
}

// CreateCustomerTxInput is CreateCustomerTx's argument shape - the fields
// the workflow-to-CRM bridge (internal/workflow's TransitionSpec.Customer,
// resolved by engine.go) has to offer.
type CreateCustomerTxInput struct {
	Name                   string
	Email                  string
	Phone                  string
	Address                string
	SourceWorkflowInstance uuid.UUID
}

// CreateCustomerTx creates a new customers row on tx (an already-open
// transaction the caller controls), with SourceWorkflowInstance set - the
// workflow-to-CRM bridge's own write primitive, the same "*Tx function,
// no authorization of its own, shared by every entry point" pattern
// internal/inventory.AdjustStockTx/internal/logistics.CreateDeliveryTx
// already establish. The caller (internal/workflow.ExecuteTransition) has
// already checked the transition's own permission by the time it reaches
// here.
//
// ciaudit:ignore-firmid-check: shared customer-creation primitive; every
// caller (internal/workflow's customer-template hook) has already run its
// own authorization check before calling this.
func CreateCustomerTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, input CreateCustomerTxInput) (uuid.UUID, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return uuid.UUID{}, fmt.Errorf("%w: name must not be empty", ErrInvalidCustomer)
	}
	var customerID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO customers (firm_id, name, email, phone, address, currency, source_workflow_instance)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, firmID, name, optionalString(input.Email), optionalString(input.Phone), optionalString(input.Address),
		defaultCustomerCurrency, input.SourceWorkflowInstance,
	).Scan(&customerID); err != nil {
		return uuid.UUID{}, fmt.Errorf("insert customer: %w", err)
	}
	return customerID, nil
}

// CustomerExistsTx reports whether customerID is a real customers row
// within firmID - the cross-module check internal/workflow's
// FieldTypeCustomer payload validation uses (checkCustomerField,
// engine.go), the same shape inventory.ProductExistsTx/SupplierExistsTx
// already establish.
//
// ciaudit:ignore-firmid-check: internal cross-package helper, called only
// by internal/workflow.checkCustomerField, itself only reachable from
// CreateInstance after permission.IsMember/Has have already run in the
// same transaction - firmID here scopes the query (defense in depth
// alongside RLS), it is not itself an authorization decision.
func CustomerExistsTx(ctx context.Context, tx pgx.Tx, firmID, customerID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM customers WHERE id = $1 AND firm_id = $2)
	`, customerID, firmID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check customer exists: %w", err)
	}
	return exists, nil
}

// GetCustomerNameTx resolves customerID's name within firmID, on tx - the
// small, read-only lookup internal/workflow's own record_sale journal-
// description interpolation uses (engine.go's resolveCustomerName) to
// inject "{{customer_name}}" into a JournalTemplate's Description, the
// same {{field}} substitution mechanism already used for every other
// interpolated placeholder - see engine.go's own doc comment on this. ok
// is false (not an error) when customerID doesn't resolve to a real
// customer in this firm - the same "missing means skip, not fail"
// contract this batch's other optional lookups already use.
//
// ciaudit:ignore-firmid-check: internal cross-package helper, called only
// by internal/workflow's ExecuteTransition, which has already run
// permission.IsMember/Has in the same transaction before reaching here -
// firmID here scopes a read query (defense in depth alongside RLS), it is
// not itself an authorization decision.
func GetCustomerNameTx(ctx context.Context, tx pgx.Tx, firmID, customerID uuid.UUID) (name string, ok bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT name FROM customers WHERE id = $1 AND firm_id = $2
	`, customerID, firmID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("look up customer name: %w", err)
	}
	return name, true, nil
}
