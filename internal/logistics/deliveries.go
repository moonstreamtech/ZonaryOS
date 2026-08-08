// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package logistics is the structural layer under the existing
// stock_to_sale workflow's own delivery lifecycle: real delivery records
// (origin/destination address, carrier, tracking number, a small fixed
// status vocabulary) - not just a state on a workflow instance. Deliveries
// are created two ways: directly, owner-gated, via CreateDelivery; or
// automatically, by internal/workflow's workflow-to-logistics bridge
// (TransitionSpec.Delivery, see spec.go's own doc comment) via
// CreateDeliveryTx, when a transition carrying a DeliveryTemplate fires
// with a resolvable destination address - see this batch's own
// scope-boundary note (migrations/0013_logistics_crm_core.up.sql) for
// what stays out of scope: no route optimization, no carrier API
// integration.
//
// Authorization follows this codebase's usual discipline: IsMember before
// any read, IsOwner before any direct write - the same tier as
// internal/inventory's product/supplier mutations. CreateDeliveryTx
// itself is different: it's driven by the workflow-to-logistics bridge,
// which has already checked its own transition's permission - see its own
// doc comment.
package logistics

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

const deliveryAuditEntityType = "delivery"

const (
	createDeliveryAuditAction = "create"
	updateDeliveryAuditAction = "update"
)

// DeliveryStatus is deliveries.status's closed vocabulary - a real DB
// CHECK constraint (migrations/0013_logistics_crm_core.up.sql), unlike
// stock_movements.reason's open, app-validated-only vocabulary: a
// delivery's lifecycle is a small, fixed set this batch itself defines.
type DeliveryStatus string

const (
	DeliveryStatusPending   DeliveryStatus = "pending"
	DeliveryStatusInTransit DeliveryStatus = "in_transit"
	DeliveryStatusDelivered DeliveryStatus = "delivered"
	DeliveryStatusReturned  DeliveryStatus = "returned"
	DeliveryStatusCancelled DeliveryStatus = "cancelled"
)

func validDeliveryStatus(s DeliveryStatus) bool {
	switch s {
	case DeliveryStatusPending, DeliveryStatusInTransit, DeliveryStatusDelivered, DeliveryStatusReturned, DeliveryStatusCancelled:
		return true
	default:
		return false
	}
}

// dateLayout is the plain calendar-date format estimated_date/actual_date
// parse against - matches an HTML <input type="date"> value verbatim,
// same convention internal/hr.dateLayout/internal/workflow.payloadDateLayout
// already use.
const dateLayout = "2006-01-02"

func parseOptionalDate(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return nil, fmt.Errorf("%w: date %q must be in YYYY-MM-DD format", ErrInvalidDelivery, s)
	}
	return &t, nil
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

func nullableJSON(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	return data
}

// Delivery is one deliveries row.
type Delivery struct {
	ID                 uuid.UUID
	FirmID             uuid.UUID
	Reference          *string
	OriginAddress      *string
	DestinationAddress *string
	Carrier            *string
	TrackingNumber     *string
	Status             DeliveryStatus
	EstimatedDate      *time.Time
	ActualDate         *time.Time
	SourceType         string
	SourceID           *uuid.UUID
	CustomFields       map[string]any
	CreatedAt          time.Time
}

// CreateDeliveryInput is CreateDelivery's request shape.
type CreateDeliveryInput struct {
	Reference          string
	OriginAddress      string
	DestinationAddress string
	Carrier            string
	TrackingNumber     string
	EstimatedDate      string // "YYYY-MM-DD", optional
	CustomFields       map[string]any
}

// CreateDelivery adds a new delivery to firmID, in DeliveryStatusPending.
// Owner-gated: manually creating a delivery record is a structural firm
// decision, the same tier as internal/inventory.CreateProduct.
func CreateDelivery(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateDeliveryInput) (Delivery, error) {
	estimatedDate, err := parseOptionalDate(input.EstimatedDate)
	if err != nil {
		return Delivery{}, err
	}
	customFields := input.CustomFields
	if customFields == nil {
		customFields = map[string]any{}
	}
	customFieldsJSON, err := json.Marshal(customFields)
	if err != nil {
		return Delivery{}, fmt.Errorf("marshal custom fields: %w", err)
	}

	var delivery Delivery
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

		delivery = Delivery{
			FirmID: firmID, Reference: optionalString(input.Reference),
			OriginAddress: optionalString(input.OriginAddress), DestinationAddress: optionalString(input.DestinationAddress),
			Carrier: optionalString(input.Carrier), TrackingNumber: optionalString(input.TrackingNumber),
			Status: DeliveryStatusPending, EstimatedDate: estimatedDate, CustomFields: customFields,
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO deliveries (firm_id, reference, origin_address, destination_address, carrier, tracking_number, status, estimated_date, custom_fields)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING id, created_at
		`, firmID, delivery.Reference, delivery.OriginAddress, delivery.DestinationAddress, delivery.Carrier, delivery.TrackingNumber,
			string(DeliveryStatusPending), estimatedDate, customFieldsJSON,
		).Scan(&delivery.ID, &delivery.CreatedAt); err != nil {
			return fmt.Errorf("insert delivery: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, delivery.ID, deliveryAuditEntityType, createDeliveryAuditAction, map[string]any{
			"destinationAddress": input.DestinationAddress,
		})
	})
	if err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

// DeliveryUpdate is UpdateDelivery's partial-update input - nil leaves
// that column unchanged, matching internal/inventory.ProductUpdate's
// convention.
type DeliveryUpdate struct {
	Reference          *string
	OriginAddress      *string
	DestinationAddress *string
	Carrier            *string
	TrackingNumber     *string
	Status             *DeliveryStatus
	EstimatedDate      *string
	ActualDate         *string
	CustomFields       map[string]any
}

// UpdateDelivery changes deliveryID's mutable fields within firmID -
// primarily its status as it moves through pending -> in_transit ->
// delivered (or returned/cancelled). Owner-gated, same tier as
// CreateDelivery.
func UpdateDelivery(ctx context.Context, pool *pgxpool.Pool, firmID, userID, deliveryID uuid.UUID, update DeliveryUpdate) (Delivery, error) {
	if update.Status != nil && !validDeliveryStatus(*update.Status) {
		return Delivery{}, fmt.Errorf("%w: status %q is not a recognized delivery status", ErrInvalidDelivery, *update.Status)
	}

	var estimatedDate, actualDate *time.Time
	var estimatedSet, actualSet bool
	if update.EstimatedDate != nil {
		estimatedSet = true
		v, err := parseOptionalDate(*update.EstimatedDate)
		if err != nil {
			return Delivery{}, err
		}
		estimatedDate = v
	}
	if update.ActualDate != nil {
		actualSet = true
		v, err := parseOptionalDate(*update.ActualDate)
		if err != nil {
			return Delivery{}, err
		}
		actualDate = v
	}

	var customFieldsJSON []byte
	if update.CustomFields != nil {
		var err error
		customFieldsJSON, err = json.Marshal(update.CustomFields)
		if err != nil {
			return Delivery{}, fmt.Errorf("marshal custom fields: %w", err)
		}
	}

	var statusParam *string
	if update.Status != nil {
		s := string(*update.Status)
		statusParam = &s
	}

	var delivery Delivery
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

		var statusRaw string
		var customFieldsRaw []byte
		err = tx.QueryRow(ctx, `
			UPDATE deliveries SET
				reference = CASE WHEN $1::boolean THEN $2 ELSE reference END,
				origin_address = CASE WHEN $3::boolean THEN $4 ELSE origin_address END,
				destination_address = CASE WHEN $5::boolean THEN $6 ELSE destination_address END,
				carrier = CASE WHEN $7::boolean THEN $8 ELSE carrier END,
				tracking_number = CASE WHEN $9::boolean THEN $10 ELSE tracking_number END,
				status = COALESCE($11, status),
				estimated_date = CASE WHEN $12::boolean THEN $13 ELSE estimated_date END,
				actual_date = CASE WHEN $14::boolean THEN $15 ELSE actual_date END,
				custom_fields = COALESCE($16, custom_fields)
			WHERE id = $17 AND firm_id = $18
			RETURNING id, firm_id, reference, origin_address, destination_address, carrier, tracking_number, status, estimated_date, actual_date, COALESCE(source_type, ''), source_id, custom_fields, created_at
		`,
			update.Reference != nil, optionalString(derefOr(update.Reference, "")),
			update.OriginAddress != nil, optionalString(derefOr(update.OriginAddress, "")),
			update.DestinationAddress != nil, optionalString(derefOr(update.DestinationAddress, "")),
			update.Carrier != nil, optionalString(derefOr(update.Carrier, "")),
			update.TrackingNumber != nil, optionalString(derefOr(update.TrackingNumber, "")),
			statusParam,
			estimatedSet, estimatedDate,
			actualSet, actualDate,
			nullableJSON(customFieldsJSON),
			deliveryID, firmID,
		).Scan(&delivery.ID, &delivery.FirmID, &delivery.Reference, &delivery.OriginAddress, &delivery.DestinationAddress,
			&delivery.Carrier, &delivery.TrackingNumber, &statusRaw, &delivery.EstimatedDate, &delivery.ActualDate,
			&delivery.SourceType, &delivery.SourceID, &customFieldsRaw, &delivery.CreatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrDeliveryNotFound
			}
			return fmt.Errorf("update delivery: %w", err)
		}
		delivery.Status = DeliveryStatus(statusRaw)
		if err := json.Unmarshal(customFieldsRaw, &delivery.CustomFields); err != nil {
			return fmt.Errorf("unmarshal custom fields: %w", err)
		}

		changes := map[string]any{}
		if update.Status != nil {
			changes["status"] = string(*update.Status)
		}
		return auditlog.Write(ctx, tx, firmID, userID, delivery.ID, deliveryAuditEntityType, updateDeliveryAuditAction, changes)
	})
	if err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

// ListDeliveriesOptions filters ListDeliveries - a zero value returns
// every delivery, matching this codebase's usual convention.
type ListDeliveriesOptions struct {
	// Status, when non-empty, keeps only deliveries in that status.
	Status DeliveryStatus
}

// ListDeliveries returns firmID's deliveries, most recent first.
// Member-gated (not owner-gated): reading the delivery board is ordinary
// firm data visibility, the same tier as internal/inventory.ListProducts.
func ListDeliveries(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListDeliveriesOptions) ([]Delivery, error) {
	var deliveries []Delivery
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, reference, origin_address, destination_address, carrier, tracking_number, status, estimated_date, actual_date, COALESCE(source_type, ''), source_id, custom_fields, created_at
			FROM deliveries
			WHERE ($1 = '' OR status = $1)
			ORDER BY created_at DESC
		`, string(opts.Status))
		if err != nil {
			return fmt.Errorf("list deliveries: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var d Delivery
			var statusRaw string
			var customFieldsRaw []byte
			if err := rows.Scan(&d.ID, &d.FirmID, &d.Reference, &d.OriginAddress, &d.DestinationAddress, &d.Carrier,
				&d.TrackingNumber, &statusRaw, &d.EstimatedDate, &d.ActualDate, &d.SourceType, &d.SourceID, &customFieldsRaw, &d.CreatedAt); err != nil {
				return err
			}
			d.Status = DeliveryStatus(statusRaw)
			if err := json.Unmarshal(customFieldsRaw, &d.CustomFields); err != nil {
				return fmt.Errorf("unmarshal custom fields: %w", err)
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

// GetDelivery resolves one delivery by id within firmID. Member-gated,
// same tier as ListDeliveries.
func GetDelivery(ctx context.Context, pool *pgxpool.Pool, firmID, userID, deliveryID uuid.UUID) (Delivery, error) {
	var delivery Delivery
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var statusRaw string
		var customFieldsRaw []byte
		err = tx.QueryRow(ctx, `
			SELECT id, firm_id, reference, origin_address, destination_address, carrier, tracking_number, status, estimated_date, actual_date, COALESCE(source_type, ''), source_id, custom_fields, created_at
			FROM deliveries WHERE id = $1 AND firm_id = $2
		`, deliveryID, firmID).Scan(&delivery.ID, &delivery.FirmID, &delivery.Reference, &delivery.OriginAddress, &delivery.DestinationAddress,
			&delivery.Carrier, &delivery.TrackingNumber, &statusRaw, &delivery.EstimatedDate, &delivery.ActualDate,
			&delivery.SourceType, &delivery.SourceID, &customFieldsRaw, &delivery.CreatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrDeliveryNotFound
			}
			return fmt.Errorf("look up delivery: %w", err)
		}
		delivery.Status = DeliveryStatus(statusRaw)
		return json.Unmarshal(customFieldsRaw, &delivery.CustomFields)
	})
	if err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

// CreateDeliveryTxInput is CreateDeliveryTx's argument shape - the fields
// the workflow-to-logistics bridge (internal/workflow's
// TransitionSpec.Delivery, resolved by engine.go) has to offer.
type CreateDeliveryTxInput struct {
	Reference          string
	OriginAddress      string
	DestinationAddress string
	Carrier            string
	TrackingNumber     string
	SourceType         string
	SourceID           *uuid.UUID
}

// CreateDeliveryTx creates a new deliveries row in DeliveryStatusPending,
// on tx (an already-open transaction the caller controls) - the
// workflow-to-logistics bridge's own write primitive, the same "*Tx
// function, no authorization of its own, shared by every entry point"
// pattern internal/accounting.PostJournalEntryTx and
// internal/inventory.AdjustStockTx already establish. The caller
// (internal/workflow.ExecuteTransition) has already checked the
// transition's own permission by the time it reaches here.
//
// ciaudit:ignore-firmid-check: shared delivery-creation primitive; every
// caller (internal/workflow's delivery-template hook) has already run its
// own authorization check before calling this.
func CreateDeliveryTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, input CreateDeliveryTxInput) (uuid.UUID, error) {
	var deliveryID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO deliveries (firm_id, reference, origin_address, destination_address, carrier, tracking_number, status, source_type, source_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, firmID, optionalString(input.Reference), optionalString(input.OriginAddress), optionalString(input.DestinationAddress),
		optionalString(input.Carrier), optionalString(input.TrackingNumber), string(DeliveryStatusPending),
		optionalString(input.SourceType), input.SourceID,
	).Scan(&deliveryID); err != nil {
		return uuid.UUID{}, fmt.Errorf("insert delivery: %w", err)
	}
	return deliveryID, nil
}

// DeliveryExistsTx reports whether deliveryID is a real deliveries row
// within firmID - the cross-module check internal/workflow's
// FieldTypeDelivery payload validation uses (checkDeliveryField,
// engine.go), the same shape inventory.ProductExistsTx/hr.PersonExistsTx
// already establish.
//
// ciaudit:ignore-firmid-check: internal cross-package helper, called only
// by internal/workflow.checkDeliveryField, itself only reachable from
// CreateInstance after permission.IsMember/Has have already run in the
// same transaction - firmID here scopes the query (defense in depth
// alongside RLS), it is not itself an authorization decision.
func DeliveryExistsTx(ctx context.Context, tx pgx.Tx, firmID, deliveryID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM deliveries WHERE id = $1 AND firm_id = $2)
	`, deliveryID, firmID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check delivery exists: %w", err)
	}
	return exists, nil
}
