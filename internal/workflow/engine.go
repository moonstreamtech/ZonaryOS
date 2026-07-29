package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// createInstanceAction is the audit_log.action recorded for instance
// creation - a fixed label, not a permission key, since creation isn't a
// transition and has no action_key of its own to reuse (see
// CreateInstance).
const createInstanceAction = "create"

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

	var definitionID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO workflow_definitions (firm_id, key, name, create_permission_key)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, firmID, spec.Key, spec.Name, spec.CreatePermission.Key).Scan(&definitionID); err != nil {
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

// CreateInstance starts a new instance of definitionID in its designated
// initial state, gated by the definition's create_permission_key. Instance
// creation has no from-state, so it is deliberately not modeled as a
// transition - but it is enforced and audited identically to one.
func CreateInstance(ctx context.Context, pool *pgxpool.Pool, firmID, userID, definitionID uuid.UUID, payload map[string]any) (uuid.UUID, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("marshal payload: %w", err)
	}

	var instanceID uuid.UUID
	err = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var createPermissionKey string
		err := tx.QueryRow(ctx, `
			SELECT create_permission_key FROM workflow_definitions WHERE id = $1
		`, definitionID).Scan(&createPermissionKey)
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
func ExecuteTransition(ctx context.Context, pool *pgxpool.Pool, firmID, userID, instanceID uuid.UUID, actionKey string, payload map[string]any) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var definitionID, currentStateID uuid.UUID
		err := tx.QueryRow(ctx, `
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

// ListInstances returns every workflow_instances row for definitionID
// within firmID, each with its current state and structurally available
// next actions - the same per-instance shape CurrentState returns for
// one instance, but for a firm-scoped listing screen (e.g. Stock In ->
// Sale's stock list). userID must actually belong to firmID (see
// permission.IsMember and CurrentState's doc comment for why this check
// exists) - RLS alone doesn't prove that.
func ListInstances(ctx context.Context, pool *pgxpool.Pool, firmID, userID, definitionID uuid.UUID) ([]InstanceState, error) {
	var results []InstanceState

	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrDefinitionNotFound
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

		instanceRows, err := tx.Query(ctx, `
			SELECT wi.id, wi.current_state_id, ws.key, ws.name, wi.payload
			FROM workflow_instances wi
			JOIN workflow_states ws ON ws.id = wi.current_state_id
			WHERE wi.workflow_definition_id = $1
			ORDER BY wi.created_at
		`, definitionID)
		if err != nil {
			return fmt.Errorf("look up instances: %w", err)
		}
		defer instanceRows.Close()
		for instanceRows.Next() {
			var inst InstanceState
			var currentStateID uuid.UUID
			var payloadJSON []byte
			if err := instanceRows.Scan(&inst.InstanceID, &currentStateID, &inst.State.Key, &inst.State.Name, &payloadJSON); err != nil {
				return err
			}
			if err := json.Unmarshal(payloadJSON, &inst.Payload); err != nil {
				return fmt.Errorf("unmarshal payload: %w", err)
			}
			inst.WorkflowDefinitionID = definitionID
			inst.AvailableActions = actionsByFromState[inst.State.Key]
			results = append(results, inst)
		}
		return instanceRows.Err()
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// DefinitionInfo names one workflow_definitions row - just enough for a
// caller (e.g. a frontend page that only knows the well-known key
// "stock_to_sale") to resolve the UUID the rest of this package's
// functions take.
type DefinitionInfo struct {
	ID   uuid.UUID
	Key  string
	Name string
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

		err = tx.QueryRow(ctx, `
			SELECT id, key, name FROM workflow_definitions WHERE key = $1
		`, key).Scan(&info.ID, &info.Key, &info.Name)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDefinitionNotFound
		}
		return err
	})
	if err != nil {
		return DefinitionInfo{}, err
	}
	return info, nil
}
