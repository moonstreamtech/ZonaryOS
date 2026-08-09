// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Payload validation pipeline (Open Points items 35/38's original
// switch-per-FieldType check, refactored this batch into a proper
// validator registry as FieldType kept growing: string/number/boolean/
// date/enum/reference/array/person/product/supplier/customer/delivery).
// A pure refactor - validatePayload's own external behavior (which
// fields are required, which error text each rejection produces) is
// unchanged; only the internal structure moved from one big switch to a
// FieldType -> FieldValidator map, so a future FieldType addition is one
// RegisterFieldValidator call rather than a new arm threaded through two
// separate switches (validatePayload's old reference/person/product/
// supplier/delivery/customer special-casing, plus checkFieldType's own
// switch for everything else).
package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/crm"
	"github.com/moonstreamtech/ZonaryOS/internal/hr"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	"github.com/moonstreamtech/ZonaryOS/internal/logistics"
)

// ValidationContext is everything a FieldValidator needs beyond the raw
// value and its FieldSpec: Ctx/Tx for the reference-style validators that
// need a real database round trip (Reference/Person/Product/Supplier/
// Delivery/Customer - none of the rest touch the database at all), FirmID
// to scope that query the same "defense in depth alongside RLS" way
// every other query in this package already does. Tx is nil when
// validation runs outside any transaction - no validator registered by
// this package needs that today (CreateInstance always calls
// validatePayload from inside its own already-open transaction), but the
// field exists so a future pure/no-DB validator isn't forced to fake one.
type ValidationContext struct {
	Ctx    context.Context
	Pool   *pgxpool.Pool
	FirmID uuid.UUID
	Tx     pgx.Tx
}

// FieldValidator checks one non-nil, present payload value against spec.
// present/required/nil handling all happen once, in validatePayload
// itself, before any FieldValidator is ever called - an implementation
// only ever sees a value that's actually there to check.
type FieldValidator interface {
	Validate(value any, spec FieldSpec, vctx ValidationContext) error
}

// fieldValidators is the FieldType -> FieldValidator registry
// validatePayload dispatches through - package-level so
// RegisterFieldValidator can extend or override it (e.g. this package's
// own tests registering a mock validator, or a future FieldType addition
// needing only one call here instead of a new switch arm in two places).
var fieldValidators = map[FieldType]FieldValidator{
	FieldTypeString:    scalarValidator{FieldTypeString},
	FieldTypeNumber:    scalarValidator{FieldTypeNumber},
	FieldTypeBoolean:   scalarValidator{FieldTypeBoolean},
	FieldTypeDate:      scalarValidator{FieldTypeDate},
	FieldTypeEnum:      enumValidator{},
	FieldTypeArray:     arrayValidator{},
	FieldTypeReference: referenceValidator{},
	FieldTypePerson:    personValidator{},
	FieldTypeProduct:   productValidator{},
	FieldTypeSupplier:  supplierValidator{},
	FieldTypeDelivery:  deliveryValidator{},
	FieldTypeCustomer:  customerValidator{},
}

// RegisterFieldValidator registers (or overrides) the validator used for
// ft. Exported so a test can register a mock FieldValidator and confirm
// validatePayload's dispatch loop actually calls it - see
// validation_test.go's own TestValidatePayload_DispatchesToRegisteredValidator
// for the pattern (register, defer restoring the original, assert the
// mock's Validate was invoked with the right value/spec).
func RegisterFieldValidator(ft FieldType, v FieldValidator) {
	fieldValidators[ft] = v
}

// validatePayload checks payload against fields - CreateInstance's server
// side of Open Points item 35 (extended by item 38): every required field
// present, and a best-effort type check of whatever fields are present (a
// JSON body decodes numbers as float64, so this accepts any Go numeric
// kind for FieldTypeNumber, not just float64, so direct Go-side callers
// like this package's own tests aren't forced to write float64(...)
// everywhere). A nil/empty fields (the overwhelmingly common case today -
// every DefinitionSpec that predates item 35) is a no-op: this function
// isn't even called in that case, see CreateInstance, but it also
// tolerates being called with nil for that reason.
//
// Takes ctx/tx/firmID because some validators (Reference/Person/Product/
// Supplier/Delivery/Customer) need a real database round trip, not a
// pure in-memory check - see ValidationContext's own doc comment.
// tx/ctx are CreateInstance's own already-open transaction, not a new
// one - this function never opens its own connection or transaction.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// CreateInstance, which has already run permission.IsMember/Has before
// reaching this call - firmID is only threaded through to
// ValidationContext to scope each validator's own query, not to make an
// authorization decision of its own.
func validatePayload(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, fields []FieldSpec, payload map[string]any) error {
	vctx := ValidationContext{Ctx: ctx, FirmID: firmID, Tx: tx}
	for _, f := range fields {
		v, present := payload[f.Name]
		if !present || v == nil {
			if f.Required {
				return fmt.Errorf("%w: missing required field %q", ErrPayloadValidation, f.Name)
			}
			continue
		}
		validator, ok := fieldValidators[f.Type]
		if !ok {
			// Unreachable for any spec that passed DefinitionSpec.Validate,
			// which rejects an unknown FieldType before it can ever be
			// persisted - defensive only.
			return fmt.Errorf("%w: field %q has unknown type %q", ErrPayloadValidation, f.Name, f.Type)
		}
		if err := validator.Validate(v, f, vctx); err != nil {
			return err
		}
	}
	return nil
}

// checkScalarValue is the type check shared by a plain scalar field
// (FieldTypeString/Number/Boolean/Date, via scalarValidator below) and,
// per-element, a FieldTypeArray field's ArrayItemType (arrayValidator) -
// pulled out on its own so the two callers share one implementation
// instead of the array validator duplicating this switch. fieldName is
// only used for error messages.
func checkScalarValue(fieldName string, ft FieldType, v any) error {
	switch ft {
	case FieldTypeString:
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%w: field %q must be a string", ErrPayloadValidation, fieldName)
		}
	case FieldTypeNumber:
		switch v.(type) {
		case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			// valid
		default:
			return fmt.Errorf("%w: field %q must be a number", ErrPayloadValidation, fieldName)
		}
	case FieldTypeBoolean:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%w: field %q must be a boolean", ErrPayloadValidation, fieldName)
		}
	case FieldTypeDate:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%w: field %q must be a date string (YYYY-MM-DD)", ErrPayloadValidation, fieldName)
		}
		if _, err := time.Parse(payloadDateLayout, s); err != nil {
			return fmt.Errorf("%w: field %q must be a valid date (YYYY-MM-DD)", ErrPayloadValidation, fieldName)
		}
	default:
		// Unreachable for any spec that passed DefinitionSpec.Validate,
		// which restricts an array field's ArrayItemType to these four
		// values before it can ever be persisted (spec.go's
		// validateFieldShape) - defensive only.
		return fmt.Errorf("%w: field %q has unsupported scalar type %q", ErrPayloadValidation, fieldName, ft)
	}
	return nil
}

// scalarValidator implements FieldValidator for the four plain scalar
// FieldTypes (String/Number/Boolean/Date) - a single type parameterized
// by which one it checks, rather than four near-identical structs.
type scalarValidator struct {
	fieldType FieldType
}

func (s scalarValidator) Validate(value any, spec FieldSpec, _ ValidationContext) error {
	return checkScalarValue(spec.Name, s.fieldType, value)
}

// enumValidator implements FieldValidator for FieldTypeEnum: value must
// be a string equal to one of spec.Options.
type enumValidator struct{}

func (enumValidator) Validate(value any, spec FieldSpec, _ ValidationContext) error {
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("%w: field %q must be a string", ErrPayloadValidation, spec.Name)
	}
	for _, opt := range spec.Options {
		if s == opt {
			return nil
		}
	}
	return fmt.Errorf("%w: field %q must be one of %v, got %q", ErrPayloadValidation, spec.Name, spec.Options, s)
}

// arrayValidator implements FieldValidator for FieldTypeArray: value must
// be a []any, each element checked against spec.ArrayItemType via the
// same checkScalarValue scalarValidator itself uses.
type arrayValidator struct{}

func (arrayValidator) Validate(value any, spec FieldSpec, _ ValidationContext) error {
	arr, ok := value.([]any)
	if !ok {
		return fmt.Errorf("%w: field %q must be an array", ErrPayloadValidation, spec.Name)
	}
	itemType := FieldType(spec.ArrayItemType)
	for i, elem := range arr {
		if err := checkScalarValue(spec.Name, itemType, elem); err != nil {
			return fmt.Errorf("%w: field %q index %d is invalid: %s", ErrPayloadValidation, spec.Name, i, err)
		}
	}
	return nil
}

// referenceValidator implements FieldValidator for FieldTypeReference:
// value must be a string that parses as a UUID, and that UUID must be an
// existing workflow_instances row, in THIS firm (vctx.FirmID), belonging
// to the definition named by spec.ReferenceDefinitionKey - not just any
// instance ID that happens to exist somewhere in the database. Runs
// inside CreateInstance's own already-open transaction (vctx.Tx), so this
// check and the INSERT it's gating are atomic with each other.
//
// Race-condition reasoning (unchanged from before this batch's refactor -
// see also the same reasoning in the batch that introduced this check's
// own final report): as of today, there is no delete path anywhere in
// this codebase for workflow_instances (grepped: no
// `DELETE FROM workflow_instances`, no DeleteInstance function) - so the
// literal "the referenced instance gets deleted between this check and
// the INSERT below" race is not reachable today. This validator is still
// written defensively for if that ever changes: `FOR SHARE OF wi` below
// takes a shared row lock on the referenced workflow_instances row for
// the rest of THIS transaction. A future DELETE (which needs its own row
// lock to remove the row) would block until this transaction commits or
// rolls back, rather than being able to remove the row out from under a
// reference that was just validated against it. This is a single
// query-level lock modifier, not bespoke distributed-locking machinery -
// deliberately proportionate to a threat that doesn't exist yet, not
// built out further than that.
//
// firmID is included explicitly in the WHERE clause even though this
// transaction's RLS session variable (app.current_firm_id, set by
// WithFirmContext - see zdb.WithFirmContext) already confines every query
// on this connection to firmID's own rows: RLS is the actual enforcement
// mechanism (Never-Violate Rule 3), but stating the same constraint
// explicitly here keeps this specific isolation property (a reference
// can't cross firm boundaries) legible by reading this one query, not
// only by trusting the connection-level session state is correctly set -
// the same "defense in depth, not defense instead of" reasoning this
// package's own doc comments use elsewhere for IsMember checks alongside
// RLS.
type referenceValidator struct{}

func (referenceValidator) Validate(value any, spec FieldSpec, vctx ValidationContext) error {
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("%w: field %q must be a string instance ID", ErrPayloadValidation, spec.Name)
	}
	refID, err := uuid.Parse(s)
	if err != nil {
		return fmt.Errorf("%w: field %q must be a valid instance ID (UUID)", ErrPayloadValidation, spec.Name)
	}

	var lockedID uuid.UUID
	err = vctx.Tx.QueryRow(vctx.Ctx, `
		SELECT wi.id
		FROM workflow_instances wi
		JOIN workflow_definitions wd ON wd.id = wi.workflow_definition_id
		WHERE wi.id = $1 AND wi.firm_id = $2 AND wd.key = $3
		FOR SHARE OF wi
	`, refID, vctx.FirmID, spec.ReferenceDefinitionKey).Scan(&lockedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: field %q references a nonexistent %q instance in this firm", ErrPayloadValidation, spec.Name, spec.ReferenceDefinitionKey)
	}
	if err != nil {
		return fmt.Errorf("check reference field %q: %w", spec.Name, err)
	}
	return nil
}

// personValidator implements FieldValidator for FieldTypePerson: value
// must be a string that parses as a UUID, and that UUID must name a real
// people row in THIS firm - the HR core batch's cross-module analog of
// referenceValidator above (a workflow instance referencing a `people`
// row instead of another workflow instance). No row-locking equivalent
// to referenceValidator's `FOR SHARE OF wi`: this codebase has no delete
// path for `people` either (mirroring referenceValidator's own
// reasoning), so the same "delete the referenced row out from under a
// just-validated reference" race isn't reachable today.
type personValidator struct{}

func (personValidator) Validate(value any, spec FieldSpec, vctx ValidationContext) error {
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("%w: field %q must be a string person ID", ErrPayloadValidation, spec.Name)
	}
	personID, err := uuid.Parse(s)
	if err != nil {
		return fmt.Errorf("%w: field %q must be a valid person ID (UUID)", ErrPayloadValidation, spec.Name)
	}

	exists, err := hr.PersonExistsTx(vctx.Ctx, vctx.Tx, vctx.FirmID, personID)
	if err != nil {
		return fmt.Errorf("check person field %q: %w", spec.Name, err)
	}
	if !exists {
		return fmt.Errorf("%w: field %q references a nonexistent person in this firm", ErrPayloadValidation, spec.Name)
	}
	return nil
}

// productValidator implements FieldValidator for FieldTypeProduct: value
// must be a string that parses as a UUID, and that UUID must name a real
// products row in THIS firm - the Inventory management batch's
// counterpart to personValidator above (a workflow instance referencing a
// `products` row instead of a `people` row).
type productValidator struct{}

func (productValidator) Validate(value any, spec FieldSpec, vctx ValidationContext) error {
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("%w: field %q must be a string product ID", ErrPayloadValidation, spec.Name)
	}
	productID, err := uuid.Parse(s)
	if err != nil {
		return fmt.Errorf("%w: field %q must be a valid product ID (UUID)", ErrPayloadValidation, spec.Name)
	}

	exists, err := inventory.ProductExistsTx(vctx.Ctx, vctx.Tx, vctx.FirmID, productID)
	if err != nil {
		return fmt.Errorf("check product field %q: %w", spec.Name, err)
	}
	if !exists {
		return fmt.Errorf("%w: field %q references a nonexistent product in this firm", ErrPayloadValidation, spec.Name)
	}
	return nil
}

// supplierValidator implements FieldValidator for FieldTypeSupplier -
// same shape as productValidator, against `suppliers` instead of
// `products`.
type supplierValidator struct{}

func (supplierValidator) Validate(value any, spec FieldSpec, vctx ValidationContext) error {
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("%w: field %q must be a string supplier ID", ErrPayloadValidation, spec.Name)
	}
	supplierID, err := uuid.Parse(s)
	if err != nil {
		return fmt.Errorf("%w: field %q must be a valid supplier ID (UUID)", ErrPayloadValidation, spec.Name)
	}

	exists, err := inventory.SupplierExistsTx(vctx.Ctx, vctx.Tx, vctx.FirmID, supplierID)
	if err != nil {
		return fmt.Errorf("check supplier field %q: %w", spec.Name, err)
	}
	if !exists {
		return fmt.Errorf("%w: field %q references a nonexistent supplier in this firm", ErrPayloadValidation, spec.Name)
	}
	return nil
}

// deliveryValidator implements FieldValidator for FieldTypeDelivery -
// same shape as productValidator, against `deliveries` instead of
// `products`.
type deliveryValidator struct{}

func (deliveryValidator) Validate(value any, spec FieldSpec, vctx ValidationContext) error {
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("%w: field %q must be a string delivery ID", ErrPayloadValidation, spec.Name)
	}
	deliveryID, err := uuid.Parse(s)
	if err != nil {
		return fmt.Errorf("%w: field %q must be a valid delivery ID (UUID)", ErrPayloadValidation, spec.Name)
	}

	exists, err := logistics.DeliveryExistsTx(vctx.Ctx, vctx.Tx, vctx.FirmID, deliveryID)
	if err != nil {
		return fmt.Errorf("check delivery field %q: %w", spec.Name, err)
	}
	if !exists {
		return fmt.Errorf("%w: field %q references a nonexistent delivery in this firm", ErrPayloadValidation, spec.Name)
	}
	return nil
}

// customerValidator implements FieldValidator for FieldTypeCustomer -
// same shape as productValidator, against `customers` instead of
// `products`.
type customerValidator struct{}

func (customerValidator) Validate(value any, spec FieldSpec, vctx ValidationContext) error {
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("%w: field %q must be a string customer ID", ErrPayloadValidation, spec.Name)
	}
	customerID, err := uuid.Parse(s)
	if err != nil {
		return fmt.Errorf("%w: field %q must be a valid customer ID (UUID)", ErrPayloadValidation, spec.Name)
	}

	exists, err := crm.CustomerExistsTx(vctx.Ctx, vctx.Tx, vctx.FirmID, customerID)
	if err != nil {
		return fmt.Errorf("check customer field %q: %w", spec.Name, err)
	}
	if !exists {
		return fmt.Errorf("%w: field %q references a nonexistent customer in this firm", ErrPayloadValidation, spec.Name)
	}
	return nil
}
