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
// wrapper around DefineWorkflowTx; callers that need workflow provisioning
// to be one step inside a larger atomic operation (e.g. the firm-creation
// wizard, which also has to insert the firm, its default role, and role
// grants in the same transaction) should call DefineWorkflowTx directly
// instead.
func DefineWorkflow(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID, spec DefinitionSpec) (uuid.UUID, error) {
	var definitionID uuid.UUID
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		id, err := DefineWorkflowTx(ctx, tx, firmID, spec)
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
func DefineWorkflowTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, spec DefinitionSpec) (uuid.UUID, error) {
	if err := spec.Validate(); err != nil {
		return uuid.UUID{}, err
	}

	if err := upsertPermission(ctx, tx, spec.CreatePermission); err != nil {
		return uuid.UUID{}, err
	}
	for _, t := range spec.Transitions {
		if err := upsertPermission(ctx, tx, t.Permission); err != nil {
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

func upsertPermission(ctx context.Context, tx pgx.Tx, p PermissionSpec) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO permissions (key, description)
		VALUES ($1, $2)
		ON CONFLICT (key) DO NOTHING
	`, p.Key, p.Description); err != nil {
		return fmt.Errorf("upsert permission %q: %w", p.Key, err)
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
// enforcement happens at ExecuteTransition time, not here.
type AvailableAction struct {
	ActionKey string
	Name      string
	ToState   StateInfo
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
// available next actions.
func CurrentState(ctx context.Context, pool *pgxpool.Pool, firmID, instanceID uuid.UUID) (InstanceState, error) {
	var result InstanceState
	var definitionID, currentStateID uuid.UUID

	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var payloadJSON []byte
		err := tx.QueryRow(ctx, `
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
			SELECT wt.action_key, wt.name, ws.key, ws.name
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
			if err := rows.Scan(&a.ActionKey, &a.Name, &a.ToState.Key, &a.ToState.Name); err != nil {
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
