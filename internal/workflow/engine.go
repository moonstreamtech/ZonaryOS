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
		rows = append(rows, payloadFieldRow{Name: f.Name, Type: string(f.Type), Required: f.Required})
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
		fields = append(fields, FieldSpec{Name: r.Name, Type: FieldType(r.Type), Required: r.Required})
	}
	return fields, nil
}

// validatePayload checks payload against fields - CreateInstance's server
// side of Open Points item 35: every required field present, and a
// best-effort type check of whatever fields are present (a JSON body
// decodes numbers as float64, so this accepts any Go numeric kind for
// FieldTypeNumber, not just float64, so direct Go-side callers like this
// package's own tests aren't forced to write float64(...) everywhere). A
// nil/empty fields (the overwhelmingly common case today - every
// DefinitionSpec that predates item 35) is a no-op: this function isn't
// even called in that case, see CreateInstance below, but it also
// tolerates being called with nil for that reason.
func validatePayload(fields []FieldSpec, payload map[string]any) error {
	for _, f := range fields {
		v, present := payload[f.Name]
		if !present || v == nil {
			if f.Required {
				return fmt.Errorf("%w: missing required field %q", ErrPayloadValidation, f.Name)
			}
			continue
		}
		if err := checkFieldType(f, v); err != nil {
			return err
		}
	}
	return nil
}

// checkFieldType is validatePayload's single-field type check - one
// switch arm per FieldType (spec.go), matching FieldType's own "exactly
// these four, no more elaborate type system" scope.
func checkFieldType(f FieldSpec, v any) error {
	switch f.Type {
	case FieldTypeString:
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%w: field %q must be a string", ErrPayloadValidation, f.Name)
		}
	case FieldTypeNumber:
		switch v.(type) {
		case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			// valid
		default:
			return fmt.Errorf("%w: field %q must be a number", ErrPayloadValidation, f.Name)
		}
	case FieldTypeBoolean:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%w: field %q must be a boolean", ErrPayloadValidation, f.Name)
		}
	case FieldTypeDate:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%w: field %q must be a date string (YYYY-MM-DD)", ErrPayloadValidation, f.Name)
		}
		if _, err := time.Parse(payloadDateLayout, s); err != nil {
			return fmt.Errorf("%w: field %q must be a valid date (YYYY-MM-DD)", ErrPayloadValidation, f.Name)
		}
	default:
		// Unreachable for any spec that passed DefinitionSpec.Validate,
		// which rejects an unknown FieldType before it can ever be
		// persisted - defensive only.
		return fmt.Errorf("%w: field %q has unknown type %q", ErrPayloadValidation, f.Name, f.Type)
	}
	return nil
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
		if _, err := tx.Exec(ctx, `
			INSERT INTO workflow_transitions
				(firm_id, workflow_definition_id, from_state_id, to_state_id, action_key, name, permission_key)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, firmID, definitionID, stateIDs[t.FromStateKey], stateIDs[t.ToStateKey], t.ActionKey, t.Name, t.Permission.Key); err != nil {
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
			SELECT create_permission_key, payload_schema FROM workflow_definitions WHERE id = $1
		`, definitionID).Scan(&createPermissionKey, &payloadSchemaJSON)
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
			if err := validatePayload(fields, payload); err != nil {
				return err
			}
		}

		var initialStateID uuid.UUID
		var initialStateKey string
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
func ExecuteTransition(ctx context.Context, pool *pgxpool.Pool, firmID, userID, instanceID uuid.UUID, actionKey string, payload map[string]any) error {
	if payload == nil {
		// See CreateInstance's identical guard above for why: a nil map
		// marshals to JSON `null`, and `payload || $2::jsonb` below treats
		// a jsonb null operand as a scalar to append (corrupting the
		// stored payload into an array) rather than a safe no-op merge.
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrInstanceNotFound
		}

		var definitionID, currentStateID uuid.UUID
		err = tx.QueryRow(ctx, `
			SELECT workflow_definition_id, current_state_id FROM workflow_instances WHERE id = $1
		`, instanceID).Scan(&definitionID, &currentStateID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInstanceNotFound
		}
		if err != nil {
			return fmt.Errorf("look up workflow instance: %w", err)
		}

		var toStateID uuid.UUID
		var toStateKey, permissionKey string
		err = tx.QueryRow(ctx, `
			SELECT wt.to_state_id, ws.key, wt.permission_key
			FROM workflow_transitions wt
			JOIN workflow_states ws ON ws.id = wt.to_state_id
			WHERE wt.workflow_definition_id = $1 AND wt.from_state_id = $2 AND wt.action_key = $3
		`, definitionID, currentStateID, actionKey).Scan(&toStateID, &toStateKey, &permissionKey)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoSuchTransition
		}
		if err != nil {
			return fmt.Errorf("look up transition: %w", err)
		}

		allowed, err := permission.Has(ctx, tx, firmID, userID, permissionKey)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrPermissionDenied
		}

		if _, err := tx.Exec(ctx, `
			UPDATE workflow_instances
			SET current_state_id = $1, payload = payload || $2::jsonb, updated_at = now()
			WHERE id = $3
		`, toStateID, payloadJSON, instanceID); err != nil {
			return fmt.Errorf("update workflow instance: %w", err)
		}

		changes, err := json.Marshal(map[string]any{"to_state": toStateKey, "payload": payload})
		if err != nil {
			return fmt.Errorf("marshal audit changes: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_log (firm_id, user_id, entity_type, entity_id, action, changes)
			VALUES ($1, $2, 'workflow_instance', $3, $4, $5)
		`, firmID, userID, instanceID, actionKey, changes); err != nil {
			return fmt.Errorf("write audit log: %w", err)
		}

		return nil
	})
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
		info.Fields, err = unmarshalPayloadSchema(payloadSchemaJSON)
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
		defer rows.Close()
		for rows.Next() {
			var d DefinitionInfo
			var payloadSchemaJSON []byte
			if err := rows.Scan(&d.ID, &d.Key, &d.Name, &d.CreatePermissionKey, &payloadSchemaJSON); err != nil {
				return err
			}
			if d.Fields, err = unmarshalPayloadSchema(payloadSchemaJSON); err != nil {
				return err
			}
			results = append(results, d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return results, nil
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
