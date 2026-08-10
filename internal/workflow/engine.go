// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/crm"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	"github.com/moonstreamtech/ZonaryOS/internal/invoicing"
	"github.com/moonstreamtech/ZonaryOS/internal/logistics"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/webhook"
)

// postgresUniqueViolation is the SQLSTATE code Postgres raises for a
// UNIQUE constraint violation - used below to turn workflow_definitions'
// (firm_id, key) collision into the specific ErrDefinitionKeyExists
// instead of a generic 500.
const postgresUniqueViolation = "23505"

// workflowInstanceListEntityType is the audit_log.entity_type ListInstances
// records its view-log entry under - see ListInstances's doc comment.
const workflowInstanceListEntityType = "workflow_instance_list"

// createInstanceAction is the audit_log.action recorded for instance
// creation - a fixed label, not a permission key, since creation isn't a
// transition and has no action_key of its own to reuse (see
// CreateInstance).
const createInstanceAction = "create"

// workflowDefinitionEntityType/defineWorkflowAuditAction are the
// audit_log.entity_type/action DefineWorkflowForFirm records under - a
// gap found and closed during the Open Points item 41 batch's audit-trail
// completeness check (docs/OPEN_POINTS.md): every other mutating path in
// this package (CreateInstance/ExecuteTransition, both below) already
// wrote an audit_log entry, but the owner-only "define a new workflow"
// action - itself a structural, permission-catalog-mutating operation,
// the same tier as firm creation - did not. DefineWorkflowTx itself stays
// audit-free (it's also called from internal/wizard.CreateDefaultFirm,
// which already writes its own single "firm create" entry covering the
// whole provisioning transaction, including the two workflows it seeds -
// a second, redundant entry per seeded workflow there would just be
// noise); the write belongs in DefineWorkflowForFirm, the HTTP-reachable,
// owner-gated caller, once IsMember/IsOwner have already been checked.
const workflowDefinitionEntityType = "workflow_definition"
const defineWorkflowAuditAction = "create"

// payloadDateLayout is the layout validatePayload parses a FieldTypeDate
// value against - a plain calendar date (no time-of-day, no timezone),
// matching an HTML <input type="date"> value verbatim (CreateInstanceForm
// on the frontend renders exactly that input for a date field). Kept as
// its own named constant rather than inlined so the engine and any future
// caller (e.g. a date-typed field in a different form) parse the same way.
const payloadDateLayout = "2006-01-02"

// templatePlaceholder matches a "{{field}}" placeholder in a
// JournalTemplate.Description - see interpolateTemplate.
var templatePlaceholder = regexp.MustCompile(`\{\{\w+\}\}`)

// journalEntitySourceType is the internal/accounting journal_entries.source_type
// value the workflow-to-ledger bridge posts under - lets a reader of the
// ledger trace an entry back to the workflow_instances row that produced
// it (source_id), the same "entity_type/entity_id, validated by the
// writer" pattern audit_log itself already uses.
const journalEntitySourceType = "workflow_instance"

// payloadFieldRow is FieldSpec's jsonb wire shape for the
// workflow_definitions.payload_schema column - kept separate from
// FieldSpec itself (spec.go), the same way defineStateRequest/
// defineTransitionRequest in handlers.go stay separate from StateSpec/
// TransitionSpec: FieldSpec is this package's Go-side spec type, this is
// specifically the on-disk JSON encoding of it.
type payloadFieldRow struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	// Options/ReferenceDefinitionKey/ArrayItemType are item 38's additions
	// - each only meaningful for its own FieldType (enum/reference/array
	// respectively, see spec.go's FieldSpec doc comments), omitted from
	// the stored JSON entirely when unused so a pre-item-38 payload_schema
	// row (every definition defined before this batch) round-trips with
	// no shape change at all.
	Options                []string `json:"options,omitempty"`
	ReferenceDefinitionKey string   `json:"referenceDefinitionKey,omitempty"`
	ArrayItemType          string   `json:"arrayItemType,omitempty"`
}

// marshalPayloadSchema encodes fields for storage in
// workflow_definitions.payload_schema. A nil/empty fields returns a nil
// []byte, which pgx sends as SQL NULL - not the JSON literal `null` and
// not `[]` - so a schema-less definition's row reads back with
// payload_schema IS NULL, the unambiguous "no schema was ever set" signal
// unmarshalPayloadSchema below relies on to skip validation entirely.
func marshalPayloadSchema(fields []FieldSpec) ([]byte, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	rows := make([]payloadFieldRow, 0, len(fields))
	for _, f := range fields {
		rows = append(rows, payloadFieldRow{
			Name:                   f.Name,
			Type:                   string(f.Type),
			Required:               f.Required,
			Options:                f.Options,
			ReferenceDefinitionKey: f.ReferenceDefinitionKey,
			ArrayItemType:          f.ArrayItemType,
		})
	}
	return json.Marshal(rows)
}

// unmarshalPayloadSchema decodes workflow_definitions.payload_schema back
// into []FieldSpec. A nil/empty data (the column was SQL NULL, i.e. this
// definition has no schema) returns a nil slice and no error - the same
// "no schema" state marshalPayloadSchema produces for an empty Fields.
func unmarshalPayloadSchema(data []byte) ([]FieldSpec, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var rows []payloadFieldRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("unmarshal payload schema: %w", err)
	}
	fields := make([]FieldSpec, 0, len(rows))
	for _, r := range rows {
		fields = append(fields, FieldSpec{
			Name:                   r.Name,
			Type:                   FieldType(r.Type),
			Required:               r.Required,
			Options:                r.Options,
			ReferenceDefinitionKey: r.ReferenceDefinitionKey,
			ArrayItemType:          r.ArrayItemType,
		})
	}
	return fields, nil
}

// marshalEffects/unmarshalEffects encode/decode a transition's
// []TransitionEffect for storage in the single
// workflow_transitions.effects jsonb column (the Effects refactor - see
// migrations/0017_workflow_effects_refactor.up.sql and TransitionSpec's
// own doc comment, spec.go) - this replaces what used to be five separate
// marshalJournalTemplate/marshalStockAdjustmentTemplate/
// marshalDeliveryTemplate/marshalCustomerTemplate/marshalInvoiceTemplate
// functions, one per bridge template, each with its own storage column. A
// nil/empty slice marshals to "[]" (effects is NOT NULL DEFAULT
// '[]'::jsonb, never SQL NULL - see the migration's own doc comment for
// why), and an empty/NULL column unmarshals back to a nil slice, matching
// every "no effects" TransitionSpec's own zero-value contract.
func marshalEffects(effects []TransitionEffect) ([]byte, error) {
	if effects == nil {
		effects = []TransitionEffect{}
	}
	return json.Marshal(effects)
}

func unmarshalEffects(data []byte) ([]TransitionEffect, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var effects []TransitionEffect
	if err := json.Unmarshal(data, &effects); err != nil {
		return nil, fmt.Errorf("unmarshal transition effects: %w", err)
	}
	return effects, nil
}

// extractEffect finds effects' entry of the given kind (if any) and
// unmarshals its Payload into T - the generic replacement for what used
// to be five separate unmarshalXTemplate functions, one per bridge (see
// marshalEffects' own doc comment). A missing kind returns (nil, nil),
// the exact same "absent means this bridge doesn't apply to this
// transition" contract every one of those five functions already had for
// a NULL/absent column - every existing ExecuteTransition/
// fetchTransitionInfos call site that already checks `if journalTemplate
// != nil { ... }` (etc.) keeps working completely unchanged against the
// value this returns.
func extractEffect[T any](effects []TransitionEffect, kind string) (*T, error) {
	for _, e := range effects {
		if e.Kind != kind {
			continue
		}
		var v T
		if err := json.Unmarshal(e.Payload, &v); err != nil {
			return nil, fmt.Errorf("unmarshal %s effect: %w", kind, err)
		}
		return &v, nil
	}
	return nil, nil
}

// resolveStockAdjustment resolves sat against payload into the arguments
// internal/inventory.AdjustStockTx needs: the referenced product's ID and
// a signed quantity string (positive for Direction "increase", negative
// for "decrease"). ok=false (not an error) means ProductField or
// QuantityField was simply absent from payload - the whole template is
// skipped for this call, the same non-breaking "missing means skip, not
// fail" contract resolveAmountField/resolveJournalLines already document
// for the ledger bridge, for the identical Rule-6-in-spirit reason: many
// existing/legitimate ExecuteTransition callers for a definition that
// later gains a StockAdjustment don't supply product_id/quantity on every
// call (e.g. stock_to_sale's record_sale before this batch). An error
// return means a field WAS present but malformed - a real data problem
// worth failing the transition (and rolling back the state change) over.
func resolveStockAdjustment(sat *StockAdjustmentTemplate, payload map[string]any) (productID uuid.UUID, quantityChange string, ok bool, err error) {
	productRaw, present := payload[sat.ProductField]
	if !present || productRaw == nil {
		return uuid.UUID{}, "", false, nil
	}
	productStr, isString := productRaw.(string)
	if !isString {
		return uuid.UUID{}, "", false, fmt.Errorf("%w: stock adjustment field %q must be a string product ID", ErrPayloadValidation, sat.ProductField)
	}
	productID, err = uuid.Parse(productStr)
	if err != nil {
		return uuid.UUID{}, "", false, fmt.Errorf("%w: stock adjustment field %q must be a valid product ID (UUID)", ErrPayloadValidation, sat.ProductField)
	}

	quantity, present, err := lookupNumberField(payload, sat.QuantityField)
	if err != nil {
		return uuid.UUID{}, "", false, err
	}
	if !present {
		return uuid.UUID{}, "", false, nil
	}
	if sat.Direction == "decrease" {
		quantity = -quantity
	}
	formatted, err := formatSignedAmount(quantity)
	if err != nil {
		return uuid.UUID{}, "", false, err
	}
	return productID, formatted, true, nil
}

// formatSignedAmount is formatAmount's signed counterpart: rounds f to 4
// fraction digits and renders it as a plain decimal string, but - unlike
// formatAmount, which rejects a non-positive result because a journal
// line's amount is always positive - allows a negative result, since a
// stock decrease (a sale) is a genuine negative quantityChange (see
// internal/inventory's own signedDecimalPattern doc comment for why there
// is no natural "side" column equivalent for a quantity delta). Still
// rejects exactly zero: a stock adjustment that changes nothing is a data
// problem worth failing the transition over, the same reasoning
// formatAmount applies to a non-positive journal amount.
func formatSignedAmount(f float64) (string, error) {
	rounded := math.Round(f*10000) / 10000
	if rounded == 0 {
		return "", fmt.Errorf("%w: computed stock adjustment quantity is zero", ErrPayloadValidation)
	}
	return strconv.FormatFloat(rounded, 'f', 4, 64), nil
}

// lookupStringField reads payload[name] and requires it to be a JSON
// string if present at all - present=false (no error) when the field is
// simply absent OR name itself is empty (an unset OPTIONAL template
// field, e.g. DeliveryTemplate.CarrierField left blank by the spec
// author) - the same "missing means skip, not fail" contract
// lookupNumberField already documents for the ledger/inventory bridges.
func lookupStringField(payload map[string]any, name string) (value string, present bool, err error) {
	if name == "" {
		return "", false, nil
	}
	v, ok := payload[name]
	if !ok || v == nil {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, fmt.Errorf("%w: field %q must be a string", ErrPayloadValidation, name)
	}
	return s, true, nil
}

// resolveDelivery resolves dt against payload into the arguments
// internal/logistics.CreateDeliveryTx needs. ok=false (not an error)
// means DestinationAddressField was absent from payload - the whole
// template is skipped for this call, the same non-breaking "missing
// means skip, not fail" contract resolveStockAdjustment already documents,
// for the identical Rule-6-in-spirit reason (see DeliveryTemplate's own
// doc comment, spec.go). The four optional fields
// (Origin/Carrier/TrackingNumber/Reference) simply resolve to "" (a NULL
// column, not a skip) when absent or unset.
func resolveDelivery(dt *DeliveryTemplate, payload map[string]any) (logistics.CreateDeliveryTxInput, bool, error) {
	destination, present, err := lookupStringField(payload, dt.DestinationAddressField)
	if err != nil {
		return logistics.CreateDeliveryTxInput{}, false, err
	}
	if !present {
		return logistics.CreateDeliveryTxInput{}, false, nil
	}

	origin, _, err := lookupStringField(payload, dt.OriginAddressField)
	if err != nil {
		return logistics.CreateDeliveryTxInput{}, false, err
	}
	carrier, _, err := lookupStringField(payload, dt.CarrierField)
	if err != nil {
		return logistics.CreateDeliveryTxInput{}, false, err
	}
	tracking, _, err := lookupStringField(payload, dt.TrackingNumberField)
	if err != nil {
		return logistics.CreateDeliveryTxInput{}, false, err
	}
	reference, _, err := lookupStringField(payload, dt.ReferenceField)
	if err != nil {
		return logistics.CreateDeliveryTxInput{}, false, err
	}

	return logistics.CreateDeliveryTxInput{
		Reference: reference, OriginAddress: origin, DestinationAddress: destination,
		Carrier: carrier, TrackingNumber: tracking,
	}, true, nil
}

// resolveCustomer resolves ct against payload into the arguments
// internal/crm.CreateCustomerTx needs (minus SourceWorkflowInstance,
// which the caller - ExecuteTransition - fills in itself, since only it
// knows the instance ID). ok=false (not an error) means NameField was
// absent from payload - the whole template is skipped, same reasoning
// resolveDelivery's own doc comment gives.
func resolveCustomer(ct *CustomerTemplate, payload map[string]any) (crm.CreateCustomerTxInput, bool, error) {
	name, present, err := lookupStringField(payload, ct.NameField)
	if err != nil {
		return crm.CreateCustomerTxInput{}, false, err
	}
	if !present {
		return crm.CreateCustomerTxInput{}, false, nil
	}

	email, _, err := lookupStringField(payload, ct.EmailField)
	if err != nil {
		return crm.CreateCustomerTxInput{}, false, err
	}
	phone, _, err := lookupStringField(payload, ct.PhoneField)
	if err != nil {
		return crm.CreateCustomerTxInput{}, false, err
	}
	address, _, err := lookupStringField(payload, ct.AddressField)
	if err != nil {
		return crm.CreateCustomerTxInput{}, false, err
	}

	return crm.CreateCustomerTxInput{Name: name, Email: email, Phone: phone, Address: address}, true, nil
}

// resolveInvoice resolves it against payload into the arguments
// internal/invoicing.CreateInvoiceTx needs. ok=false (not an error) means
// QuantityField or UnitPriceField was absent from payload, or either
// resolved value wasn't numeric - the whole template is skipped for this
// call, the same non-breaking "missing means skip, not fail" contract
// resolveDelivery/resolveCustomer already document, for the identical
// Rule-6-in-spirit reason (see InvoiceTemplate's own doc comment, spec.go).
// ProductField/CustomerField are OPTIONAL and simply resolve to a nil
// pointer (a NULL column) when absent.
func resolveInvoice(it *InvoiceTemplate, payload map[string]any) (invoicing.CreateInvoiceTxInput, bool, error) {
	quantity, quantityPresent, err := lookupNumberField(payload, it.QuantityField)
	if err != nil {
		return invoicing.CreateInvoiceTxInput{}, false, err
	}
	unitPrice, unitPricePresent, err := lookupNumberField(payload, it.UnitPriceField)
	if err != nil {
		return invoicing.CreateInvoiceTxInput{}, false, err
	}
	if !quantityPresent || !unitPricePresent {
		return invoicing.CreateInvoiceTxInput{}, false, nil
	}
	quantityStr, err := formatAmount(quantity)
	if err != nil {
		return invoicing.CreateInvoiceTxInput{}, false, err
	}
	unitPriceStr, err := formatAmount(unitPrice)
	if err != nil {
		return invoicing.CreateInvoiceTxInput{}, false, err
	}

	var productID *uuid.UUID
	productIDStr, present, err := lookupStringField(payload, it.ProductField)
	if err != nil {
		return invoicing.CreateInvoiceTxInput{}, false, err
	}
	if present {
		id, err := uuid.Parse(productIDStr)
		if err != nil {
			return invoicing.CreateInvoiceTxInput{}, false, fmt.Errorf("%w: field %q must be a valid product id", ErrPayloadValidation, it.ProductField)
		}
		productID = &id
	}

	var customerID *uuid.UUID
	customerIDStr, present, err := lookupStringField(payload, it.CustomerField)
	if err != nil {
		return invoicing.CreateInvoiceTxInput{}, false, err
	}
	if present {
		id, err := uuid.Parse(customerIDStr)
		if err != nil {
			return invoicing.CreateInvoiceTxInput{}, false, fmt.Errorf("%w: field %q must be a valid customer id", ErrPayloadValidation, it.CustomerField)
		}
		customerID = &id
	}

	return invoicing.CreateInvoiceTxInput{
		CustomerID: customerID, Description: interpolateTemplate(it.Description, payload),
		Quantity: quantityStr, UnitPrice: unitPriceStr, ProductID: productID, IssuedDate: time.Now(),
	}, true, nil
}

// interpolateTemplate substitutes every "{{field}}" placeholder in s with
// payload[field]'s value (formatted via fmt.Sprint) - JournalTemplate.Description's
// only templating mechanism, deliberately simple: no expressions, no
// nested lookups, matching internal/workflow/rules.go's own
// MessageTemplate substitution style for notify actions. A placeholder
// naming a field that isn't present in payload is left as literal text
// ("{{field}}") rather than silently blanked - a visibly wrong
// description is a more honest failure mode than data quietly vanishing
// from an accounting record.
func interpolateTemplate(s string, payload map[string]any) string {
	return templatePlaceholder.ReplaceAllStringFunc(s, func(match string) string {
		field := match[2 : len(match)-2]
		v, ok := payload[field]
		if !ok {
			return match
		}
		return fmt.Sprint(v)
	})
}

// resolveAmountField resolves one LineTemplate.AmountField against
// payload: either a single field name whose value must be numeric, or two
// field names joined by "*" (e.g. "quantity*unit_price") - the one
// payload-derived computation this bridge supports natively, see
// LineTemplate.AmountField's own doc comment for why. Returns ok=false
// (not an error) when the named field(s) are simply absent from payload -
// resolveJournalLines treats that as "this template doesn't apply to this
// particular transition call", not a failure. Returns an error only when
// a named field IS present but isn't a number - genuinely malformed data,
// not merely missing.
func resolveAmountField(payload map[string]any, field string) (amount string, ok bool, err error) {
	if a, b, isProduct := strings.Cut(field, "*"); isProduct {
		x, xOK, err := lookupNumberField(payload, strings.TrimSpace(a))
		if err != nil || !xOK {
			return "", false, err
		}
		y, yOK, err := lookupNumberField(payload, strings.TrimSpace(b))
		if err != nil || !yOK {
			return "", false, err
		}
		amount, err := formatAmount(x * y)
		if err != nil {
			return "", false, err
		}
		return amount, true, nil
	}

	f, present, err := lookupNumberField(payload, field)
	if err != nil || !present {
		return "", false, err
	}
	amount, err = formatAmount(f)
	if err != nil {
		return "", false, err
	}
	return amount, true, nil
}

// lookupNumberField reads payload[name] and requires it to be a JSON
// number if present at all - present=false (no error) when the field is
// simply absent, matching resolveAmountField's own "missing means skip,
// not fail" contract.
func lookupNumberField(payload map[string]any, name string) (value float64, present bool, err error) {
	v, ok := payload[name]
	if !ok || v == nil {
		return 0, false, nil
	}
	switch n := v.(type) {
	case float64:
		return n, true, nil
	case float32:
		return float64(n), true, nil
	case int:
		return float64(n), true, nil
	case int64:
		return float64(n), true, nil
	default:
		return 0, false, fmt.Errorf("%w: field %q must be a number to use in a journal amount", ErrPayloadValidation, name)
	}
}

// formatAmount rounds f to 4 fraction digits (numeric(19,4), matching
// internal/accounting's own amountPattern) and renders it as a plain
// decimal string - never scientific notation, since strconv's 'f' format
// never produces it. Rejects a non-positive result: a journal line's
// amount is always positive (see the financial-core migration's own
// journal_lines.amount CHECK), so a zero/negative computed amount is a
// data problem worth failing the transition over, not a silent skip.
func formatAmount(f float64) (string, error) {
	rounded := math.Round(f*10000) / 10000
	if rounded <= 0 {
		return "", fmt.Errorf("%w: computed journal amount %v is not positive", ErrPayloadValidation, rounded)
	}
	return strconv.FormatFloat(rounded, 'f', 4, 64), nil
}

// resolveJournalLines resolves every LineTemplate in jt against payload
// into internal/accounting.LineInput values ready for PostJournalEntryTx.
// ok=false (not an error) means at least one line's amount field wasn't
// present in payload - the whole template is skipped for this call,
// deliberately non-fatal: many existing/legitimate ExecuteTransition
// callers for a definition that later gains a JournalTemplate (e.g.
// stock_to_sale's record_sale, wired up by this same batch) don't supply
// every financially-relevant field on every call, and this bridge must
// never turn "the caller didn't send a price" into a broken state
// transition - Never-Violate Rule 6 in spirit, even though this isn't the
// versioned HTTP contract itself. An error return, by contrast, means a
// field WAS present but malformed - a real data problem worth failing the
// transition (and rolling back the state change with it) over.
func resolveJournalLines(jt *JournalTemplate, payload map[string]any) ([]accounting.LineInput, bool, error) {
	// instanceCurrencyPayloadField (this batch's multi-currency
	// foundation) mirrors customerIDPayloadField's own "fixed, well-known
	// field name" convention below: a payload's optional "currency"
	// string field, when present, is threaded onto every resolved line
	// as its own currency, so PostJournalEntryTx (internal/accounting)
	// can auto-convert into each line's target account's own currency
	// when they differ - see that function's own doc comment for the
	// conversion/fallback logic. Absent (the overwhelmingly common case
	// today - no existing DefinitionSpec sets a "currency" field), every
	// line's Currency stays "" and PostJournalEntryTx's existing
	// behavior (default to the account's own currency, no conversion
	// attempted) is completely unchanged.
	instanceCurrency, _, err := lookupStringField(payload, instanceCurrencyPayloadField)
	if err != nil {
		return nil, false, err
	}

	lines := make([]accounting.LineInput, 0, len(jt.Lines))
	for _, lt := range jt.Lines {
		amount, ok, err := resolveAmountField(payload, lt.AmountField)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		lines = append(lines, accounting.LineInput{
			AccountCode: lt.AccountCode,
			Side:        accounting.Side(lt.Side),
			Amount:      amount,
			Currency:    instanceCurrency,
		})
	}
	return lines, true, nil
}

// instanceCurrencyPayloadField is the well-known payload field name
// resolveJournalLines looks for - see its own doc comment above for why
// this is a fixed convention rather than a new JournalTemplate field.
const instanceCurrencyPayloadField = "currency"

// customerIDPayloadField is the well-known payload field name
// resolveJournalDescription looks for - "customer_id", the exact field
// stock_to_sale.go declares as its own FieldTypeCustomer field (see its
// own Fields entry). This is deliberately a fixed convention, not a
// declarative field on JournalTemplate itself: the design brief calls for
// one specific case (a sale's journal description naming its customer),
// not a general "any JournalTemplate may reference any customer field"
// capability - adding a whole new template field for a single derived
// placeholder would be exactly the kind of unwieldy growth TransitionSpec's
// own doc comment on its four template fields already guards against.
const customerIDPayloadField = "customer_id"

// resolveJournalDescription interpolates jt.Description against payload
// (interpolateTemplate's usual {{field}} substitution, unchanged), then -
// if payload's customer_id field resolves to a real customer in firmID -
// appends " (customer: {{customer_name}})", interpolated through the
// exact SAME interpolateTemplate mechanism against a customer_name-only
// payload. This is the design brief's "update the journal entry's
// description to include the customer name (via template interpolation,
// same {{field}} pattern that already works for journal descriptions)":
// genuinely the same substitution primitive, just composed in Go rather
// than baked into a single static template string - deliberately, so the
// common case (no customer_id on this call, e.g. every record_sale call
// site that predates this batch) renders the description exactly as
// before, with no dangling "{{customer_name}}" placeholder visible - the
// one behavior interpolateTemplate's own "leave missing placeholders
// literal" contract would otherwise produce if {{customer_name}} were
// baked directly into stock_to_sale's static Description.
//
// A customer_id present but not a string, not a valid UUID, or not
// resolving to a real customer in this firm is treated the same as
// "absent" here (base description, unchanged) rather than failing the
// transition - the journal description is cosmetic, not the transition's
// correctness-critical path (checkCustomerField, run at CreateInstance
// time via the FieldTypeCustomer schema check, is what actually rejects a
// genuinely invalid customer_id before it ever reaches here).
//
// ciaudit:ignore-firmid-check: internal helper called only by
// ExecuteTransition, which has already run permission.IsMember/Has in the
// same transaction before reaching this call - firmID here scopes
// GetCustomerNameTx's read query (defense in depth alongside RLS), it is
// not itself an authorization decision.
func resolveJournalDescription(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, jt *JournalTemplate, payload map[string]any) (string, error) {
	description := interpolateTemplate(jt.Description, payload)

	customerIDStr, present, err := lookupStringField(payload, customerIDPayloadField)
	if err != nil || !present {
		return description, nil
	}
	customerID, err := uuid.Parse(customerIDStr)
	if err != nil {
		return description, nil
	}
	name, ok, err := crm.GetCustomerNameTx(ctx, tx, firmID, customerID)
	if err != nil {
		return "", err
	}
	if !ok {
		return description, nil
	}
	return interpolateTemplate(description+" (customer: {{customer_name}})", map[string]any{"customer_name": name}), nil
}

// DefineWorkflow writes spec as rows in workflow_definitions/states/
// transitions, all in one WithFirmContext transaction, upserting any
// permission keys it references into the global permissions catalog along
// the way. This is the one Go code path both this PR's concrete
// Stock-In-Sale workflow (see stock_to_sale.go) and any future, much
// larger workflow definition go through - the engine itself never
// hardcodes a specific workflow's shape. It is a thin WithFirmContext
// wrapper around DefineWorkflowTx, calling it with no granting role (see
// DefineWorkflowTx) - this pool-based entry point exists for test
// fixtures and other callers that just need a workflow's shape to exist,
// not for real firm-initiated provisioning. Callers that need workflow
// provisioning to be one step inside a larger atomic operation (e.g. the
// firm-creation wizard, which also has to insert the firm, its default
// role, and that role's permission grants in the same transaction) should
// call DefineWorkflowTx directly instead.
//
// ciaudit:ignore-firmid-check: workflow-provisioning helper, only called
// by test fixtures and internal/workflow.SeedStockToSaleWorkflow - never
// exposed via an HTTP handler that would hand it a caller-supplied
// firmID.
func DefineWorkflow(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID, spec DefinitionSpec) (uuid.UUID, error) {
	var definitionID uuid.UUID
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		id, err := DefineWorkflowTx(ctx, tx, firmID, uuid.Nil, spec)
		definitionID = id
		return err
	})
	if err != nil {
		return uuid.UUID{}, err
	}
	return definitionID, nil
}

// DefineWorkflowTx is DefineWorkflow's core logic against an already-open
// transaction. The caller is responsible for that transaction already
// being scoped to firmID (typically via WithFirmContext, or - during firm
// creation, before WithFirmContext can apply - by having just set
// app.current_firm_id itself within the same transaction).
//
// granteeRoleID implements the "self-action auto-grant" rule: when a
// firm's own action provisions a new permission key (here, by defining a
// workflow), that permission is granted to the role that triggered the
// action, in the same transaction - not left as a separate step the
// caller has to remember (and, for the firm-creation wizard, not
// something a bespoke grant loop needs to duplicate; see
// internal/wizard.CreateDefaultFirm). Pass uuid.Nil to skip granting
// entirely - what DefineWorkflow (above) does for fixture/test callers
// that only want the workflow's shape to exist, not a real grant against
// a real acting role.
//
// ciaudit:ignore-firmid-check: workflow-provisioning helper, only called
// during firm creation (internal/wizard.CreateDefaultFirm, with a firmID
// it just created in the same transaction) and test fixtures - never
// reachable with a caller-supplied firmID via an HTTP handler.
func DefineWorkflowTx(ctx context.Context, tx pgx.Tx, firmID, granteeRoleID uuid.UUID, spec DefinitionSpec) (uuid.UUID, error) {
	if err := spec.Validate(); err != nil {
		return uuid.UUID{}, err
	}

	// A FieldTypeReference field's ReferenceDefinitionKey names another
	// workflow definition (e.g. "stock_to_sale") - Validate above can only
	// confirm that key is non-empty (it's a pure, DB-free function with no
	// firm context - see FieldSpec.ReferenceDefinitionKey's and
	// validateFieldShape's own doc comments in spec.go for why). Checking
	// the key actually resolves to a real workflow_definitions row for
	// THIS firm belongs here instead: DefineWorkflowTx is the one shared
	// entry point every code path that can define a workflow goes through
	// - DefineWorkflow (test/fixture callers), DefineWorkflowForFirm (the
	// HTTP-reachable, owner-gated path), and SeedStockToSaleWorkflowTx/
	// SeedCustomerPipelineWorkflowTx (firm-creation wizard provisioning) -
	// so putting the check here, rather than only in
	// DefineWorkflowForFirm, guarantees the invariant holds regardless of
	// which of those callers is used, the same way upsertPermission just
	// below is shared by all of them rather than duplicated per caller.
	for _, f := range spec.Fields {
		if f.Type != FieldTypeReference {
			continue
		}
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM workflow_definitions WHERE firm_id = $1 AND key = $2)
		`, firmID, f.ReferenceDefinitionKey).Scan(&exists); err != nil {
			return uuid.UUID{}, fmt.Errorf("check reference target definition %q for field %q: %w", f.ReferenceDefinitionKey, f.Name, err)
		}
		if !exists {
			return uuid.UUID{}, fmt.Errorf("%w: field %q references unknown workflow definition key %q", ErrInvalidSpec, f.Name, f.ReferenceDefinitionKey)
		}
	}

	if err := upsertPermission(ctx, tx, firmID, granteeRoleID, spec.CreatePermission); err != nil {
		return uuid.UUID{}, err
	}
	for _, t := range spec.Transitions {
		if err := upsertPermission(ctx, tx, firmID, granteeRoleID, t.Permission); err != nil {
			return uuid.UUID{}, err
		}
	}

	payloadSchemaJSON, err := marshalPayloadSchema(spec.Fields)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("marshal payload schema: %w", err)
	}

	var definitionID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO workflow_definitions (firm_id, key, name, create_permission_key, payload_schema)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, firmID, spec.Key, spec.Name, spec.CreatePermission.Key, payloadSchemaJSON).Scan(&definitionID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == postgresUniqueViolation {
			return uuid.UUID{}, ErrDefinitionKeyExists
		}
		return uuid.UUID{}, fmt.Errorf("insert workflow definition: %w", err)
	}

	stateIDs := make(map[string]uuid.UUID, len(spec.States))
	for _, s := range spec.States {
		var stateID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO workflow_states (firm_id, workflow_definition_id, key, name, is_initial, is_terminal)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id
		`, firmID, definitionID, s.Key, s.Name, s.IsInitial, s.IsTerminal).Scan(&stateID); err != nil {
			return uuid.UUID{}, fmt.Errorf("insert workflow state %q: %w", s.Key, err)
		}
		stateIDs[s.Key] = stateID
	}

	for _, t := range spec.Transitions {
		effectsJSON, err := marshalEffects(t.Effects)
		if err != nil {
			return uuid.UUID{}, fmt.Errorf("marshal effects for transition %q: %w", t.ActionKey, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO workflow_transitions
				(firm_id, workflow_definition_id, from_state_id, to_state_id, action_key, name, permission_key, effects)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, firmID, definitionID, stateIDs[t.FromStateKey], stateIDs[t.ToStateKey], t.ActionKey, t.Name, t.Permission.Key,
			effectsJSON); err != nil {
			return uuid.UUID{}, fmt.Errorf("insert workflow transition %q: %w", t.ActionKey, err)
		}
	}

	return definitionID, nil
}

// upsertPermission registers p in the global permissions catalog and,
// when granteeRoleID is not uuid.Nil, grants it to that role within
// firmID in the same statement batch - see DefineWorkflowTx's doc comment
// for why. Idempotent either way: ON CONFLICT DO NOTHING on both inserts
// means calling this again for the same key/role (e.g. a second workflow
// definition referencing an already-known permission) is a no-op, not an
// error.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// DefineWorkflowTx, which is itself provisioning-only (see its own
// suppression note above).
func upsertPermission(ctx context.Context, tx pgx.Tx, firmID, granteeRoleID uuid.UUID, p PermissionSpec) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO permissions (key, description)
		VALUES ($1, $2)
		ON CONFLICT (key) DO NOTHING
	`, p.Key, p.Description); err != nil {
		return fmt.Errorf("upsert permission %q: %w", p.Key, err)
	}

	if granteeRoleID == uuid.Nil {
		return nil
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO role_permissions (firm_id, role_id, permission_key)
		VALUES ($1, $2, $3)
		ON CONFLICT (role_id, permission_key) DO NOTHING
	`, firmID, granteeRoleID, p.Key); err != nil {
		return fmt.Errorf("grant permission %q to role %s: %w", p.Key, granteeRoleID, err)
	}
	return nil
}

// DefineWorkflowForFirm is DefineWorkflow's HTTP-reachable counterpart -
// what lets a firm actually use the generic workflow engine (this
// codebase's own DefineWorkflow/DefineWorkflowTx, already exercised by
// SeedStockToSaleWorkflow) to define its own workflow, instead of only
// ever getting the one hardcoded stock_to_sale definition every firm is
// seeded with. Owner-gated: defining a new workflow type - a new
// permission-gated action surface for the firm - is a structural firm
// decision, the same tier as firm creation itself (see
// internal/wizard.CreateDefaultFirm), not something every member can do.
//
// On success, every permission spec introduces (its create permission,
// plus each transition's) is granted to the acting owner's own
// owner-flagged role - DefineWorkflowTx's "self-action auto-grant"
// (see its own doc comment), the same mechanism CreateDefaultFirm and
// SeedStockToSaleWorkflowTx already rely on - so a firm isn't left
// holding a freshly-defined workflow nobody can actually use yet. A firm
// can extend access to other roles afterward via Permission Audit Mode,
// same as any other permission.
func DefineWorkflowForFirm(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, spec DefinitionSpec) (uuid.UUID, error) {
	var definitionID uuid.UUID
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrDefinitionNotFound
		}

		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrPermissionDenied
		}

		if err := spec.Validate(); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidSpec, err)
		}

		var granteeRoleID uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT r.id FROM user_firm_roles ufr
			JOIN roles r ON r.id = ufr.role_id
			WHERE ufr.user_id = $1 AND ufr.firm_id = $2 AND r.is_owner
			LIMIT 1
		`, userID, firmID).Scan(&granteeRoleID); err != nil {
			return fmt.Errorf("look up acting owner's role: %w", err)
		}

		id, err := DefineWorkflowTx(ctx, tx, firmID, granteeRoleID, spec)
		if err != nil {
			return err
		}
		definitionID = id

		changes := map[string]any{
			"key":                 spec.Key,
			"name":                spec.Name,
			"createPermissionKey": spec.CreatePermission.Key,
		}
		return auditlog.Write(ctx, tx, firmID, userID, definitionID, workflowDefinitionEntityType, defineWorkflowAuditAction, changes)
	})
	if err != nil {
		return uuid.UUID{}, err
	}
	return definitionID, nil
}

// CreateInstance starts a new instance of definitionID in its designated
// initial state, gated by the definition's create_permission_key. Instance
// creation has no from-state, so it is deliberately not modeled as a
// transition - but it is enforced and audited identically to one.
//
// If definitionID's own payload_schema (Open Points item 35,
// spec.go's DefinitionSpec.Fields) is set, payload is validated against
// it before anything is written - a missing required field or a
// wrong-typed field is rejected with ErrPayloadValidation, and no
// instance/audit row is created. A definition with no schema (the
// StockToSaleSpec/CustomerPipelineSpec case today) skips this check
// entirely, exactly as before item 35 existed - see validatePayload's own
// doc comment. This is deliberately a create-time-only gate: it never
// runs against an already-created instance's stored payload (CurrentState/
// ListInstances/ExecuteTransition never call validatePayload), so adding a
// schema to a definition after instances already exist can never make an
// existing row "invalid" - it only changes what a *new* CreateInstance
// call accepts going forward (item 35's question 4).
//
// The permission.IsMember check up front (Open Points item 37's audit)
// is not strictly required for correctness here - permission.Has already
// fails for a non-member, since it needs a real user_firm_roles row - but
// without it, a non-member supplying a real firmID/definitionID pair
// could still read create_permission_key and learn that a definition
// exists there before being denied, a minor existence/metadata oracle.
// Checking membership first closes that too and keeps every function in
// this file that touches a firm-scoped table doing so in the same order.
func CreateInstance(ctx context.Context, pool *pgxpool.Pool, firmID, userID, definitionID uuid.UUID, payload map[string]any) (uuid.UUID, error) {
	if payload == nil {
		// A nil map marshals to the JSON literal `null`, not `{}` - fine
		// for the plain assignment below, but ExecuteTransition's `payload
		// || $2::jsonb` merge treats a jsonb null operand as a scalar to
		// append rather than a no-op (Postgres: `'{"a":1}'::jsonb ||
		// 'null'::jsonb` = `[{"a":1}, null]`, corrupting the stored
		// payload) - found by the E2E smoke test's "add stock" step
		// omitting payload entirely, exactly like decodeJSONBody's own
		// documented "missing/empty body is treated as no payload given"
		// contract promises should be safe. Normalizing here (and in
		// ExecuteTransition below) keeps both call sites' contract intact
		// for every caller, not just ones that happen to send `{}`.
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("marshal payload: %w", err)
	}

	var instanceID uuid.UUID
	var definitionKey, initialStateKey string
	err = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrDefinitionNotFound
		}

		var createPermissionKey string
		var payloadSchemaJSON []byte
		err = tx.QueryRow(ctx, `
			SELECT create_permission_key, payload_schema, key FROM workflow_definitions WHERE id = $1
		`, definitionID).Scan(&createPermissionKey, &payloadSchemaJSON, &definitionKey)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDefinitionNotFound
		}
		if err != nil {
			return fmt.Errorf("look up workflow definition: %w", err)
		}

		allowed, err := permission.Has(ctx, tx, firmID, userID, createPermissionKey)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrPermissionDenied
		}

		fields, err := unmarshalPayloadSchema(payloadSchemaJSON)
		if err != nil {
			return err
		}
		if len(fields) > 0 {
			if err := validatePayload(ctx, tx, firmID, fields, payload); err != nil {
				return err
			}
		}

		var initialStateID uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT id, key FROM workflow_states WHERE workflow_definition_id = $1 AND is_initial
		`, definitionID).Scan(&initialStateID, &initialStateKey); err != nil {
			return fmt.Errorf("look up initial state: %w", err)
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO workflow_instances (firm_id, workflow_definition_id, current_state_id, payload, created_by_user_id)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id
		`, firmID, definitionID, initialStateID, payloadJSON, userID).Scan(&instanceID); err != nil {
			return fmt.Errorf("insert workflow instance: %w", err)
		}

		changes, err := json.Marshal(map[string]any{"to_state": initialStateKey, "payload": payload})
		if err != nil {
			return fmt.Errorf("marshal audit changes: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_log (firm_id, user_id, entity_type, entity_id, action, changes)
			VALUES ($1, $2, 'workflow_instance', $3, $4, $5)
		`, firmID, userID, instanceID, createInstanceAction, changes); err != nil {
			return fmt.Errorf("write audit log: %w", err)
		}

		return nil
	})
	if err != nil {
		return uuid.UUID{}, err
	}

	// webhook.Dispatch runs AFTER commit, same "nothing left to roll
	// back, so a failure here must never fail the request" reasoning
	// EvaluateRules' own doc comment gives just below - see
	// internal/webhook's own package doc comment for the full design.
	webhook.Dispatch(pool, firmID, webhook.EventWorkflowInstanceCreated, map[string]any{
		"instanceId":    instanceID.String(),
		"definitionKey": definitionKey,
		"stateKey":      initialStateKey,
		"payload":       payload,
	})

	// Rule evaluation runs AFTER the instance's own creation has committed
	// - see EvaluateRules' doc comment (rules.go) for why this is the seam
	// "immediately after commit where transaction boundaries prevent [inline]"
	// actually available here, and why an EvaluateRules error is logged,
	// not propagated: the instance already exists by this point, so there
	// is nothing left to roll back.
	if err := EvaluateRules(ctx, pool, firmID, userID, instanceID, definitionKey, TriggerOnCreate, initialStateKey, payload); err != nil {
		// slog's structured fields (as opposed to the old log.Printf's
		// interpolated format string) JSON-encode err/definitionKey below,
		// escaping any control character firm-authored rule content could
		// carry - this is an operator-facing server log, not content
		// returned to any caller.
		slog.Warn("workflow rule evaluation failed", "instanceID", instanceID, "definitionKey", definitionKey, "trigger", TriggerOnCreate, "err", err)
	}

	return instanceID, nil
}

// ExecuteTransition moves instanceID from its current state to whatever
// state the transition matching actionKey points to, gated by that
// transition's permission_key. payload is shallow-merged into the
// instance's stored payload (jsonb `||`) and also recorded verbatim - as
// the delta this specific call contributed - in the audit_log entry.
//
// Same permission.IsMember-first reasoning as CreateInstance (Open
// Points item 37's audit): permission.Has still fails a non-member on
// its own, but without this check first, a non-member supplying a real
// firmID/instanceID pair could read the instance's current state and
// which transitions structurally exist from it before being denied.
//
// ciaudit:ignore-firmid-check: this is now a thin pool-opening wrapper -
// the real permission.IsMember/Has checks live in executeTransitionTx,
// which this function calls with the transaction WithFirmContext opens.
func ExecuteTransition(ctx context.Context, pool *pgxpool.Pool, firmID, userID, instanceID uuid.UUID, actionKey string, payload map[string]any) error {
	var definitionKey, toStateKey string
	var mergedPayload map[string]any
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		definitionKey, toStateKey, mergedPayload, err = executeTransitionTx(ctx, tx, firmID, userID, instanceID, actionKey, payload)
		return err
	})
	if err != nil {
		return err
	}

	webhook.Dispatch(pool, firmID, webhook.EventWorkflowTransitionExecuted, map[string]any{
		"instanceId":    instanceID.String(),
		"definitionKey": definitionKey,
		"actionKey":     actionKey,
		"toState":       toStateKey,
		"payload":       mergedPayload,
	})

	// See CreateInstance's identical post-commit EvaluateRules call for why
	// this runs after the transition has already committed, and why an
	// error here is logged rather than propagated.
	if err := EvaluateRules(ctx, pool, firmID, userID, instanceID, definitionKey, TriggerOnTransition, toStateKey, mergedPayload); err != nil {
		// See CreateInstance's identical logging call above for why this
		// is logged (structured, via slog) rather than propagated.
		slog.Warn("workflow rule evaluation failed", "instanceID", instanceID, "definitionKey", definitionKey, "trigger", TriggerOnTransition, "err", err)
	}

	return nil
}

// executeTransitionTx is ExecuteTransition's own core logic, taking an
// already-open tx instead of opening its own - extracted so
// BulkExecuteTransition (bulk-transition.go) can run several instances'
// worth of this same logic inside ONE shared transaction (true
// atomicity: any instance's failure rolls back every instance already
// transitioned in that same call, via zdb.WithFirmContext's own
// defer tx.Rollback(ctx)), rather than calling the pool-opening
// ExecuteTransition once per instance, which would each commit
// independently. Every other caller of this logic still goes through
// ExecuteTransition itself, unchanged.
func executeTransitionTx(ctx context.Context, tx pgx.Tx, firmID, userID, instanceID uuid.UUID, actionKey string, payload map[string]any) (definitionKey, toStateKey string, mergedPayload map[string]any, err error) {
	if payload == nil {
		// See CreateInstance's identical guard above for why: a nil map
		// marshals to JSON `null`, and `payload || $2::jsonb` below treats
		// a jsonb null operand as a scalar to append (corrupting the
		// stored payload into an array) rather than a safe no-op merge.
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", "", nil, fmt.Errorf("marshal payload: %w", err)
	}

	isMember, err := permission.IsMember(ctx, tx, firmID, userID)
	if err != nil {
		return "", "", nil, err
	}
	if !isMember {
		return "", "", nil, ErrInstanceNotFound
	}

	var definitionID, currentStateID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT wi.workflow_definition_id, wi.current_state_id, wd.key
		FROM workflow_instances wi
		JOIN workflow_definitions wd ON wd.id = wi.workflow_definition_id
		WHERE wi.id = $1
	`, instanceID).Scan(&definitionID, &currentStateID, &definitionKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil, ErrInstanceNotFound
	}
	if err != nil {
		return "", "", nil, fmt.Errorf("look up workflow instance: %w", err)
	}

	var toStateID uuid.UUID
	var permissionKey string
	var effectsJSON []byte
	err = tx.QueryRow(ctx, `
		SELECT wt.to_state_id, ws.key, wt.permission_key, wt.effects
		FROM workflow_transitions wt
		JOIN workflow_states ws ON ws.id = wt.to_state_id
		WHERE wt.workflow_definition_id = $1 AND wt.from_state_id = $2 AND wt.action_key = $3
	`, definitionID, currentStateID, actionKey).Scan(&toStateID, &toStateKey, &permissionKey, &effectsJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil, ErrNoSuchTransition
	}
	if err != nil {
		return "", "", nil, fmt.Errorf("look up transition: %w", err)
	}
	effects, err := unmarshalEffects(effectsJSON)
	if err != nil {
		return "", "", nil, err
	}
	journalTemplate, err := extractEffect[JournalTemplate](effects, EffectKindJournal)
	if err != nil {
		return "", "", nil, err
	}
	stockAdjustment, err := extractEffect[StockAdjustmentTemplate](effects, EffectKindStock)
	if err != nil {
		return "", "", nil, err
	}
	deliveryTemplate, err := extractEffect[DeliveryTemplate](effects, EffectKindDelivery)
	if err != nil {
		return "", "", nil, err
	}
	customerTemplate, err := extractEffect[CustomerTemplate](effects, EffectKindCustomer)
	if err != nil {
		return "", "", nil, err
	}
	invoiceTemplate, err := extractEffect[InvoiceTemplate](effects, EffectKindInvoice)
	if err != nil {
		return "", "", nil, err
	}

	allowed, err := permission.Has(ctx, tx, firmID, userID, permissionKey)
	if err != nil {
		return "", "", nil, err
	}
	if !allowed {
		return "", "", nil, ErrPermissionDenied
	}

	var mergedPayloadJSON []byte
	if err := tx.QueryRow(ctx, `
		UPDATE workflow_instances
		SET current_state_id = $1, payload = payload || $2::jsonb, updated_at = now()
		WHERE id = $3
		RETURNING payload
	`, toStateID, payloadJSON, instanceID).Scan(&mergedPayloadJSON); err != nil {
		return "", "", nil, fmt.Errorf("update workflow instance: %w", err)
	}
	if err := json.Unmarshal(mergedPayloadJSON, &mergedPayload); err != nil {
		return "", "", nil, fmt.Errorf("unmarshal merged payload: %w", err)
	}

	changes, err := json.Marshal(map[string]any{"to_state": toStateKey, "payload": payload})
	if err != nil {
		return "", "", nil, fmt.Errorf("marshal audit changes: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (firm_id, user_id, entity_type, entity_id, action, changes)
		VALUES ($1, $2, 'workflow_instance', $3, $4, $5)
	`, firmID, userID, instanceID, actionKey, changes); err != nil {
		return "", "", nil, fmt.Errorf("write audit log: %w", err)
	}

	// The workflow-to-ledger bridge: when this transition carries a
	// JournalTemplate, resolve it against the merged payload and post
	// the resulting entry in the SAME transaction as the state change
	// itself - either both commit together or neither does, so the
	// ledger can never end up out of sync with the workflow instance
	// that produced it. resolveJournalLines' ok=false (not an error)
	// means the template's fields simply weren't supplied on this
	// call - skipped, not failed, see its own doc comment for why
	// that matters for Rule 6. A genuine resolution error (a present
	// field with the wrong type) or a posting failure (unknown
	// account code, or - structurally unreachable here since this
	// bridge always emits a balanced set of lines, but still handled
	// - an unbalanced entry) fails the whole transition, rolling
	// back the state change with it: once there's enough information
	// to post, a broken posting is a real error, not something to
	// silently swallow the way EvaluateRules' post-commit failures
	// are (see CreateInstance's doc comment for that different case).
	if journalTemplate != nil {
		lines, ok, err := resolveJournalLines(journalTemplate, mergedPayload)
		if err != nil {
			return "", "", nil, err
		}
		if ok {
			description, err := resolveJournalDescription(ctx, tx, firmID, journalTemplate, mergedPayload)
			if err != nil {
				return "", "", nil, err
			}
			if _, err := accounting.PostJournalEntryTx(ctx, tx, firmID, userID, description, journalEntitySourceType, &instanceID, lines); err != nil {
				return "", "", nil, err
			}
		}
	}

	// The workflow-to-inventory bridge: same "resolve against the
	// merged payload, apply in the SAME transaction as the state
	// change" shape as the journal bridge just above, through
	// internal/inventory.AdjustStockTx instead of
	// accounting.PostJournalEntryTx - not a new mechanism (see
	// StockAdjustmentTemplate's own doc comment, spec.go). ok=false
	// means product_id/quantity simply weren't supplied on this call -
	// skipped, not failed (resolveStockAdjustment's own doc comment).
	// A resolution error, or AdjustStockTx itself returning
	// ErrInsufficientStock, fails the whole transition - rolling back
	// the state change with it, so a rejected sale (insufficient
	// stock) never partially applies: no state change, no stock
	// change, exactly the design brief's "reject the transition, not
	// just warn".
	if stockAdjustment != nil {
		productID, quantityChange, ok, err := resolveStockAdjustment(stockAdjustment, mergedPayload)
		if err != nil {
			return "", "", nil, err
		}
		if ok {
			if err := inventory.AdjustStockTx(ctx, tx, firmID, productID, "", quantityChange, stockAdjustment.Reason, journalEntitySourceType, &instanceID); err != nil {
				return "", "", nil, err
			}
		}
	}

	// The workflow-to-logistics bridge: same "resolve against the
	// merged payload, apply in the SAME transaction as the state
	// change" shape as the bridges above, through
	// internal/logistics.CreateDeliveryTx - not a new mechanism (see
	// DeliveryTemplate's own doc comment, spec.go). ok=false means
	// DestinationAddressField simply wasn't supplied on this call -
	// skipped, not failed (resolveDelivery's own doc comment).
	if deliveryTemplate != nil {
		deliveryInput, ok, err := resolveDelivery(deliveryTemplate, mergedPayload)
		if err != nil {
			return "", "", nil, err
		}
		if ok {
			deliveryInput.SourceType = journalEntitySourceType
			deliveryInput.SourceID = &instanceID
			if _, err := logistics.CreateDeliveryTx(ctx, tx, firmID, deliveryInput); err != nil {
				return "", "", nil, err
			}
		}
	}

	// The workflow-to-CRM bridge: same shape, through
	// internal/crm.CreateCustomerTx - customer_pipeline's own "convert"
	// transition is this batch's real working example (see
	// customer_pipeline.go): a lead converting to a customer state now
	// creates an actual customers row, with SourceWorkflowInstance set
	// to this instance, in the SAME transaction as the state change.
	// ok=false means NameField simply wasn't supplied - skipped, not
	// failed (resolveCustomer's own doc comment).
	if customerTemplate != nil {
		customerInput, ok, err := resolveCustomer(customerTemplate, mergedPayload)
		if err != nil {
			return "", "", nil, err
		}
		if ok {
			customerInput.SourceWorkflowInstance = instanceID
			if _, err := crm.CreateCustomerTx(ctx, tx, firmID, customerInput); err != nil {
				return "", "", nil, err
			}
		}
	}

	// The workflow-to-invoicing bridge: same shape, through
	// internal/invoicing.CreateInvoiceTx - stock_to_sale's own
	// record_sale transition is this batch's real working example
	// (see stock_to_sale.go): a sale now auto-creates a draft
	// invoice, sourced to this instance, in the SAME transaction as
	// the state change. ok=false means QuantityField/UnitPriceField
	// simply weren't both supplied - skipped, not failed
	// (resolveInvoice's own doc comment).
	if invoiceTemplate != nil {
		invoiceInput, ok, err := resolveInvoice(invoiceTemplate, mergedPayload)
		if err != nil {
			return "", "", nil, err
		}
		if ok {
			if _, err := invoicing.CreateInvoiceTx(ctx, tx, firmID, instanceID, invoiceInput); err != nil {
				return "", "", nil, err
			}
		}
	}

	return definitionKey, toStateKey, mergedPayload, nil
}

// StateInfo names a single state node.
type StateInfo struct {
	Key  string
	Name string
}

// AvailableAction is one transition the instance could take from its
// current state - listed regardless of the caller's own permissions;
// enforcement happens at ExecuteTransition time, not here. PermissionKey
// is included so a UI rendering this action can carry the Never-Violate
// Rule 7 "permission tag" (Vision §3 Permission Audit Mode) without
// hardcoding a duplicate of the key ExecuteTransition actually checks.
type AvailableAction struct {
	ActionKey     string
	Name          string
	ToState       StateInfo
	PermissionKey string
}

// InstanceState is the current state of one workflow instance, plus what
// it could do next.
type InstanceState struct {
	InstanceID           uuid.UUID
	WorkflowDefinitionID uuid.UUID
	State                StateInfo
	Payload              map[string]any
	AvailableActions     []AvailableAction
}

// CurrentState reads instanceID's current state and its structurally
// available next actions. userID must actually belong to firmID (see
// permission.IsMember) - WithFirmContext's RLS scoping alone only
// confines the query to rows whose firm_id matches firmID, which by
// itself doesn't prove the caller has any right to that firmID at all;
// without this check, any authenticated user could read any firm's
// instances just by supplying that firm's real ID.
func CurrentState(ctx context.Context, pool *pgxpool.Pool, firmID, userID, instanceID uuid.UUID) (InstanceState, error) {
	var result InstanceState
	var definitionID, currentStateID uuid.UUID

	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrInstanceNotFound
		}

		var payloadJSON []byte
		err = tx.QueryRow(ctx, `
			SELECT wi.id, wi.workflow_definition_id, wi.current_state_id, ws.key, ws.name, wi.payload
			FROM workflow_instances wi
			JOIN workflow_states ws ON ws.id = wi.current_state_id
			WHERE wi.id = $1
		`, instanceID).Scan(&result.InstanceID, &definitionID, &currentStateID, &result.State.Key, &result.State.Name, &payloadJSON)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInstanceNotFound
		}
		if err != nil {
			return fmt.Errorf("look up workflow instance: %w", err)
		}
		result.WorkflowDefinitionID = definitionID
		if err := json.Unmarshal(payloadJSON, &result.Payload); err != nil {
			return fmt.Errorf("unmarshal payload: %w", err)
		}

		rows, err := tx.Query(ctx, `
			SELECT wt.action_key, wt.name, ws.key, ws.name, wt.permission_key
			FROM workflow_transitions wt
			JOIN workflow_states ws ON ws.id = wt.to_state_id
			WHERE wt.workflow_definition_id = $1 AND wt.from_state_id = $2
			ORDER BY wt.action_key
		`, definitionID, currentStateID)
		if err != nil {
			return fmt.Errorf("look up available actions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var a AvailableAction
			if err := rows.Scan(&a.ActionKey, &a.Name, &a.ToState.Key, &a.ToState.Name, &a.PermissionKey); err != nil {
				return err
			}
			result.AvailableActions = append(result.AvailableActions, a)
		}
		return rows.Err()
	})
	if err != nil {
		return InstanceState{}, err
	}
	return result, nil
}

// ListInstancesOptions controls paging and text filtering for
// ListInstances - kept generic (a raw search string matched against
// whatever's actually stored per-instance) rather than any
// definition-specific field, since this must work identically for
// stock_to_sale, purchase_order, or any other definition. The zero value
// (Limit 0, Offset 0, Search "") returns every instance, unfiltered - the
// same behavior ListInstances always had, so existing callers (the
// integration tests, WorkflowHistory's own correlation fetch below) don't
// need to opt into pagination to keep working.
type ListInstancesOptions struct {
	// Limit caps how many instances are returned. 0 means unlimited.
	Limit int
	// Offset skips this many instances (after filtering), for paging
	// past Limit-sized pages. Ignored when Limit is 0.
	Offset int
	// Search, when non-empty, keeps only instances whose current state's
	// key/name or whose payload (serialized) contains this substring,
	// case-insensitively. There's no fixed set of "searchable fields" -
	// the payload is arbitrary per-definition JSON, so this matches
	// against its raw text rather than assuming any particular key
	// exists.
	Search string
}

// ListInstancesResult is ListInstances's return shape: the (possibly
// paged/filtered) instances themselves, plus Total - the count that
// would match Search with no Limit/Offset applied, so a caller can
// render "page X of Y" without a second round trip.
type ListInstancesResult struct {
	Instances []InstanceState
	Total     int
}

// ListInstances returns workflow_instances rows for definitionID within
// firmID, each with its current state and structurally available next
// actions - the same per-instance shape CurrentState returns for one
// instance, but for a firm-scoped listing screen (e.g. Stock In -> Sale's
// stock list). userID must actually belong to firmID (see
// permission.IsMember and CurrentState's doc comment for why this check
// exists) - RLS alone doesn't prove that. opts controls paging/filtering -
// see ListInstancesOptions.
//
// This is Vision §3's Audit Trail Infrastructure's one wired-up view/read
// log call site (internal/auditlog.LogView) for this PR: view/read logging
// is explicitly called for in the vision but is also the part flagged as
// legally uncertain (docs/OPEN_POINTS.md item 33 - whether retaining
// detailed view records creates KVKK exposure is an open question requiring
// legal counsel). Rather than wiring every read endpoint in the system
// (CurrentState, LookupDefinitionByKey, Permission Audit Mode's own reads,
// ...) into this mechanism now, only this one representative path - the
// stock list, PR #8's read endpoint - does, as a proof of the mechanism.
// Broader rollout is future work pending that legal-review decision, not an
// oversight.
func ListInstances(ctx context.Context, pool *pgxpool.Pool, firmID, userID, definitionID uuid.UUID, opts ListInstancesOptions) (ListInstancesResult, error) {
	var result ListInstancesResult

	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrDefinitionNotFound
		}

		if err := auditlog.LogView(ctx, tx, firmID, userID, definitionID, workflowInstanceListEntityType); err != nil {
			return err
		}

		// One query for every transition this definition has, grouped by
		// its from-state key, instead of one query per instance - the
		// number of transitions is small and fixed per definition
		// regardless of how many instances exist.
		actionsByFromState := make(map[string][]AvailableAction)
		transitionRows, err := tx.Query(ctx, `
			SELECT src.key, wt.action_key, wt.name, dst.key, dst.name, wt.permission_key
			FROM workflow_transitions wt
			JOIN workflow_states src ON src.id = wt.from_state_id
			JOIN workflow_states dst ON dst.id = wt.to_state_id
			WHERE wt.workflow_definition_id = $1
			ORDER BY wt.action_key
		`, definitionID)
		if err != nil {
			return fmt.Errorf("look up transitions: %w", err)
		}
		for transitionRows.Next() {
			var fromStateKey string
			var a AvailableAction
			if err := transitionRows.Scan(&fromStateKey, &a.ActionKey, &a.Name, &a.ToState.Key, &a.ToState.Name, &a.PermissionKey); err != nil {
				transitionRows.Close()
				return err
			}
			actionsByFromState[fromStateKey] = append(actionsByFromState[fromStateKey], a)
		}
		if err := transitionRows.Err(); err != nil {
			return err
		}
		transitionRows.Close()

		// COUNT(*) OVER() rides along with the paged rows so Total
		// reflects the filtered-but-unpaged count in the same round
		// trip, rather than a separate COUNT query. NULLIF($3, 0) turns
		// a Limit of 0 into a SQL NULL, and `LIMIT NULL` in Postgres
		// means "no limit" - so the zero-value ListInstancesOptions
		// keeps returning everything, unpaged.
		instanceRows, err := tx.Query(ctx, `
			SELECT wi.id, wi.current_state_id, ws.key, ws.name, wi.payload, COUNT(*) OVER()
			FROM workflow_instances wi
			JOIN workflow_states ws ON ws.id = wi.current_state_id
			WHERE wi.workflow_definition_id = $1
			  AND ($2 = '' OR ws.key ILIKE '%' || $2 || '%' OR ws.name ILIKE '%' || $2 || '%' OR wi.payload::text ILIKE '%' || $2 || '%')
			ORDER BY wi.created_at
			LIMIT NULLIF($3, 0) OFFSET $4
		`, definitionID, opts.Search, opts.Limit, opts.Offset)
		if err != nil {
			return fmt.Errorf("look up instances: %w", err)
		}
		defer instanceRows.Close()
		var instances []InstanceState
		for instanceRows.Next() {
			var inst InstanceState
			var currentStateID uuid.UUID
			var payloadJSON []byte
			var total int
			if err := instanceRows.Scan(&inst.InstanceID, &currentStateID, &inst.State.Key, &inst.State.Name, &payloadJSON, &total); err != nil {
				return err
			}
			if err := json.Unmarshal(payloadJSON, &inst.Payload); err != nil {
				return fmt.Errorf("unmarshal payload: %w", err)
			}
			inst.WorkflowDefinitionID = definitionID
			inst.AvailableActions = actionsByFromState[inst.State.Key]
			instances = append(instances, inst)
			result.Total = total
		}
		result.Instances = instances
		return instanceRows.Err()
	})
	if err != nil {
		return ListInstancesResult{}, err
	}
	return result, nil
}

// DefinitionInfo names one workflow_definitions row - just enough for a
// caller (e.g. a frontend page that only knows the well-known key
// "stock_to_sale") to resolve the UUID the rest of this package's
// functions take. CreatePermissionKey is included for the same reason
// AvailableAction.PermissionKey is: a UI rendering the "create instance"
// action (e.g. "add stock") needs the real key CreateInstance actually
// checks to carry a Never-Violate Rule 7 permission tag, instead of
// hardcoding a duplicate of it.
type DefinitionInfo struct {
	ID                  uuid.UUID
	Key                 string
	Name                string
	CreatePermissionKey string
	// Fields is this definition's OPTIONAL payload schema (Open Points
	// item 35) - nil for a schema-less definition (StockToSaleSpec/
	// CustomerPipelineSpec today), exactly the same "field absent means
	// freeform" contract DefinitionSpec.Fields itself documents. Included
	// here so a caller resolving a definition (e.g. the frontend's
	// CreateInstanceForm) can render typed fields instead of the freeform
	// key/value editor without a second round trip.
	Fields []FieldSpec
	// Transitions is every transition this definition has, including its
	// OPTIONAL Journal template - the read-only surface the workflow
	// definition page (item: extend /workflows/[key] to show the journal
	// template for each transition that has one) renders from, without a
	// second endpoint dedicated just to transitions.
	Transitions []TransitionInfo
}

// TransitionInfo is one workflow_transitions row, read back with its
// endpoints' state keys resolved and its Effects attached - the
// structural counterpart to AvailableAction (which is scoped to one
// instance's current state) at the definition level, scoped to the whole
// transition graph instead. handlers.go's toTransitionInfoResponses
// extracts each bridge kind out of Effects to build the same
// journal/stockAdjustment/delivery/customer/invoice response fields the
// HTTP API returned before the Effects refactor - this struct itself no
// longer carries one field per bridge.
type TransitionInfo struct {
	ActionKey     string
	Name          string
	FromState     StateInfo
	ToState       StateInfo
	PermissionKey string
	Effects       []TransitionEffect
}

// fetchTransitionInfos loads every workflow_transitions row for
// definitionID, in action-key order - shared by LookupDefinitionByKey and
// ListDefinitions so both expose the exact same transition/effects read
// path.
func fetchTransitionInfos(ctx context.Context, tx pgx.Tx, definitionID uuid.UUID) ([]TransitionInfo, error) {
	rows, err := tx.Query(ctx, `
		SELECT wt.action_key, wt.name, src.key, src.name, dst.key, dst.name, wt.permission_key, wt.effects
		FROM workflow_transitions wt
		JOIN workflow_states src ON src.id = wt.from_state_id
		JOIN workflow_states dst ON dst.id = wt.to_state_id
		WHERE wt.workflow_definition_id = $1
		ORDER BY wt.action_key
	`, definitionID)
	if err != nil {
		return nil, fmt.Errorf("look up transitions: %w", err)
	}
	defer rows.Close()

	var infos []TransitionInfo
	for rows.Next() {
		var ti TransitionInfo
		var effectsJSON []byte
		if err := rows.Scan(&ti.ActionKey, &ti.Name, &ti.FromState.Key, &ti.FromState.Name, &ti.ToState.Key, &ti.ToState.Name,
			&ti.PermissionKey, &effectsJSON); err != nil {
			return nil, err
		}
		ti.Effects, err = unmarshalEffects(effectsJSON)
		if err != nil {
			return nil, err
		}
		infos = append(infos, ti)
	}
	return infos, rows.Err()
}

// LookupDefinitionByKey resolves firmID's workflow_definitions row by its
// key (e.g. "stock_to_sale", see stock_to_sale.go's StockToSaleKey).
// userID must actually belong to firmID (see permission.IsMember and
// CurrentState's doc comment for why) - a key that doesn't exist for
// firmID, or a firmID the caller isn't a member of at all, both resolve
// to the same ErrDefinitionNotFound, by design (see writeEngineError).
func LookupDefinitionByKey(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, key string) (DefinitionInfo, error) {
	var info DefinitionInfo
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrDefinitionNotFound
		}

		var payloadSchemaJSON []byte
		err = tx.QueryRow(ctx, `
			SELECT id, key, name, create_permission_key, payload_schema FROM workflow_definitions WHERE key = $1
		`, key).Scan(&info.ID, &info.Key, &info.Name, &info.CreatePermissionKey, &payloadSchemaJSON)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDefinitionNotFound
		}
		if err != nil {
			return err
		}
		if info.Fields, err = unmarshalPayloadSchema(payloadSchemaJSON); err != nil {
			return err
		}
		info.Transitions, err = fetchTransitionInfos(ctx, tx, info.ID)
		return err
	})
	if err != nil {
		return DefinitionInfo{}, err
	}
	return info, nil
}

// ListDefinitions returns every workflow_definitions row for firmID,
// ordered by name - the data source for a firm-level "Workflows" view
// (a firm's second, third, ... workflow definition, whenever one exists,
// should show up here automatically rather than needing a hardcoded
// frontend card per workflow the way the Stock In -> Sale page originally
// did). This complements LookupDefinitionByKey rather than replacing it:
// a caller that already knows a well-known key (e.g. the stock page)
// still resolves it directly, without listing everything first. Same
// IsMember-first treatment as every other read in this file - see
// CurrentState's doc comment for why RLS scoping alone isn't enough.
func ListDefinitions(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]DefinitionInfo, error) {
	var results []DefinitionInfo
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrDefinitionNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, key, name, create_permission_key, payload_schema
			FROM workflow_definitions
			ORDER BY name
		`)
		if err != nil {
			return fmt.Errorf("list workflow definitions: %w", err)
		}
		for rows.Next() {
			var d DefinitionInfo
			var payloadSchemaJSON []byte
			if err := rows.Scan(&d.ID, &d.Key, &d.Name, &d.CreatePermissionKey, &payloadSchemaJSON); err != nil {
				rows.Close()
				return err
			}
			if d.Fields, err = unmarshalPayloadSchema(payloadSchemaJSON); err != nil {
				rows.Close()
				return err
			}
			results = append(results, d)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()

		// A second round trip per definition, only after the first
		// result set is fully drained and closed - pgx does not support
		// an interleaved Query() on the same transaction/connection
		// while a previous Rows is still open (the same reason
		// ListInstances, further down this file, fully closes its own
		// transitionRows before opening instanceRows).
		for i := range results {
			if results[i].Transitions, err = fetchTransitionInfos(ctx, tx, results[i].ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// ExportDefinitionSpecs reads every workflow_definitions row for firmID
// back out as a real DefinitionSpec - the exact shape DefineWorkflow/
// DefineWorkflowTx themselves consume - rather than a second, parallel
// export-only representation. This is what makes the import side of
// internal/portability's configuration import/export engine simple: it
// marshals these values directly for export, and for import it feeds
// them straight back into DefineWorkflowTx, reusing that function's own
// validation and permission upsert logic rather than duplicating it.
// Includes every state (not just ones a transition happens to
// reference - unlike TransitionInfo.FromState/ToState, which only cover
// states actually connected by an edge) and every transition's Effects
// and Permission.Description (joined from the global permissions
// catalog, since workflow_transitions.permission_key is only a foreign
// key - DefineWorkflowTx needs the description text too, to upsert the
// permission catalog entry on the importing firm). Member-gated (not
// owner-gated): mirrors ListDefinitions' own tier - this is a read.
func ExportDefinitionSpecs(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]DefinitionSpec, error) {
	var specs []DefinitionSpec
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrDefinitionNotFound
		}

		type defRow struct {
			id             uuid.UUID
			key, name      string
			createPermKey  string
			createPermDesc string
			schemaJSON     []byte
		}
		defRows, err := tx.Query(ctx, `
			SELECT wd.id, wd.key, wd.name, wd.create_permission_key, p.description, wd.payload_schema
			FROM workflow_definitions wd
			JOIN permissions p ON p.key = wd.create_permission_key
			ORDER BY wd.name
		`)
		if err != nil {
			return fmt.Errorf("list workflow definitions: %w", err)
		}
		var defs []defRow
		for defRows.Next() {
			var d defRow
			if err := defRows.Scan(&d.id, &d.key, &d.name, &d.createPermKey, &d.createPermDesc, &d.schemaJSON); err != nil {
				defRows.Close()
				return err
			}
			defs = append(defs, d)
		}
		if err := defRows.Err(); err != nil {
			return err
		}
		defRows.Close()

		// Two more round trips per definition (states, then transitions),
		// only after the previous result set is fully drained and closed -
		// pgx does not support an interleaved Query() on the same
		// transaction while a previous Rows is still open (the same
		// reason ListDefinitions' own two-pass shape, just above, closes
		// its first Rows before opening a second).
		for _, d := range defs {
			fields, err := unmarshalPayloadSchema(d.schemaJSON)
			if err != nil {
				return err
			}

			stateRows, err := tx.Query(ctx, `
				SELECT key, name, is_initial, is_terminal FROM workflow_states WHERE workflow_definition_id = $1
			`, d.id)
			if err != nil {
				return fmt.Errorf("list states for definition %q: %w", d.key, err)
			}
			var states []StateSpec
			for stateRows.Next() {
				var s StateSpec
				if err := stateRows.Scan(&s.Key, &s.Name, &s.IsInitial, &s.IsTerminal); err != nil {
					stateRows.Close()
					return err
				}
				states = append(states, s)
			}
			if err := stateRows.Err(); err != nil {
				return err
			}
			stateRows.Close()

			transitionRows, err := tx.Query(ctx, `
				SELECT wt.action_key, wt.name, src.key, dst.key, wt.permission_key, p.description, wt.effects
				FROM workflow_transitions wt
				JOIN workflow_states src ON src.id = wt.from_state_id
				JOIN workflow_states dst ON dst.id = wt.to_state_id
				JOIN permissions p ON p.key = wt.permission_key
				WHERE wt.workflow_definition_id = $1
				ORDER BY wt.action_key
			`, d.id)
			if err != nil {
				return fmt.Errorf("list transitions for definition %q: %w", d.key, err)
			}
			var transitions []TransitionSpec
			for transitionRows.Next() {
				var t TransitionSpec
				var permDesc string
				var effectsJSON []byte
				if err := transitionRows.Scan(&t.ActionKey, &t.Name, &t.FromStateKey, &t.ToStateKey, &t.Permission.Key, &permDesc, &effectsJSON); err != nil {
					transitionRows.Close()
					return err
				}
				t.Permission.Description = permDesc
				if t.Effects, err = unmarshalEffects(effectsJSON); err != nil {
					transitionRows.Close()
					return err
				}
				transitions = append(transitions, t)
			}
			if err := transitionRows.Err(); err != nil {
				return err
			}
			transitionRows.Close()

			specs = append(specs, DefinitionSpec{
				Key:  d.key,
				Name: d.name,
				CreatePermission: PermissionSpec{
					Key:         d.createPermKey,
					Description: d.createPermDesc,
				},
				States:      states,
				Transitions: transitions,
				Fields:      fields,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return specs, nil
}

// StateCount is one state's instance count within a single workflow
// definition - one row of DefinitionInstanceCounts.Counts.
type StateCount struct {
	StateKey  string
	StateName string
	Count     int
}

// DefinitionInstanceCounts is InstanceCountsByDefinition's per-definition
// result: which definition, and how many instances currently sit in each
// of its states. Every state the definition has appears here, even ones
// with zero instances (see InstanceCountsByDefinition's query) - a
// dashboard rendering "0 sold" for a brand new firm is more honest than
// silently omitting a row a caller might otherwise read as "state
// doesn't exist."
type DefinitionInstanceCounts struct {
	DefinitionID uuid.UUID
	Key          string
	Name         string
	Counts       []StateCount
}

// InstanceCountsByDefinition is the dashboard's data source (item 2 of
// this batch): for every workflow definition in firmID, how many
// instances currently sit in each state - "Stock: 12 in stock, 3 sold" /
// "Customers: 5 leads, 2 customers, 1 lost" - computed as one grouped
// COUNT query across every definition at once, not by fetching every
// instance and counting client-side (ListInstances's own full-fetch path
// is the wrong tool for a dashboard summary once a firm has thousands of
// instances). A LEFT JOIN from workflow_states out to workflow_instances
// means a state with zero instances still gets a zero-count row instead
// of silently vanishing - see DefinitionInstanceCounts's doc comment.
// Same IsMember-first treatment as every other read in this file.
func InstanceCountsByDefinition(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]DefinitionInstanceCounts, error) {
	var results []DefinitionInstanceCounts
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrDefinitionNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT wd.id, wd.key, wd.name, ws.key, ws.name, count(wi.id)
			FROM workflow_definitions wd
			JOIN workflow_states ws ON ws.workflow_definition_id = wd.id
			LEFT JOIN workflow_instances wi ON wi.current_state_id = ws.id
			GROUP BY wd.id, wd.key, wd.name, ws.key, ws.name
			ORDER BY wd.name, ws.key
		`)
		if err != nil {
			return fmt.Errorf("count workflow instances by state: %w", err)
		}
		defer rows.Close()

		byDefinition := make(map[uuid.UUID]*DefinitionInstanceCounts)
		var order []uuid.UUID
		for rows.Next() {
			var defID uuid.UUID
			var defKey, defName string
			var sc StateCount
			if err := rows.Scan(&defID, &defKey, &defName, &sc.StateKey, &sc.StateName, &sc.Count); err != nil {
				return err
			}
			entry, ok := byDefinition[defID]
			if !ok {
				entry = &DefinitionInstanceCounts{DefinitionID: defID, Key: defKey, Name: defName}
				byDefinition[defID] = entry
				order = append(order, defID)
			}
			entry.Counts = append(entry.Counts, sc)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		results = make([]DefinitionInstanceCounts, 0, len(order))
		for _, id := range order {
			results = append(results, *byDefinition[id])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}
