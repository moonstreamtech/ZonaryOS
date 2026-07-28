package wizard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// defaultRoleKey is the key of the role every wizard-created firm starts
// with. Its holder is the firm's founder - the person who just ran the
// wizard - so it is granted every permission the wizard provisions for
// this firm (see CreateDefaultFirm), not a restricted starter role: there
// is nobody else yet to grant permissions to them, and Vision §3's
// Parametric Permission/Role System expects the firm owner to configure
// roles for everyone else from here via Permission Audit Mode. This
// intentionally does not reach for "grant every key in the global
// permissions catalog" - that catalog is shared across all firms (see
// migrations/0001_core_schema.up.sql), so it can also hold permission
// keys other firms' own custom workflows introduced, which have nothing
// to do with this firm. Granting exactly what this firm's own
// provisioning just created keeps the grant bounded and meaningful; a
// firm that defines more workflows later needs its own grant step when it
// does, same as this one.
const defaultRoleKey = "owner"

// createFirmAuditAction is the audit_log.action recorded for firm
// creation via the wizard - firm creation has no permission-gated
// "action key" of its own (there's no role/permission model to check
// against yet at the moment a firm is born), but Vision §3's audit trail
// principle applies to every entity creation, and the workflow engine
// already established the convention of auditing creation events (see
// internal/workflow.CreateInstance) - this reuses the same audit_log
// table and shape, not a bespoke one.
const createFirmAuditAction = "create"

// CreateDefaultFirmResult is what CreateDefaultFirm produces on success.
type CreateDefaultFirmResult struct {
	FirmID                  uuid.UUID
	FirmName                string
	RoleID                  uuid.UUID
	StockToSaleDefinitionID uuid.UUID
}

// CreateDefaultFirm is the wizard's one implemented terminal action
// (ActionCreateDefaultFirm): it creates a new firm, a default "owner"
// role for it, adds userID as that role's holder, seeds the firm with the
// Stock In -> Sale workflow (reusing workflow.SeedStockToSaleWorkflowTx
// as-is, not duplicating its logic), and writes one audit_log entry - all
// inside a single transaction, so a caller never ends up with a
// half-created firm.
//
// A plain WithFirmContext (internal/platform/db) can't be used here: it
// requires a firmID up front to scope the transaction to, but the firm
// doesn't exist until this function's first insert. This is the write-side
// counterpart to the read-side bootstrap problem
// migrations/0002_user_scoped_discovery.up.sql solved for firm discovery -
// the transaction below sets app.current_firm_id itself, once the new
// firm's ID is known, before touching any tenant-scoped table.
func CreateDefaultFirm(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, firmName string) (CreateDefaultFirmResult, error) {
	firmName = strings.TrimSpace(firmName)
	if firmName == "" {
		return CreateDefaultFirmResult{}, fmt.Errorf("firm name must not be empty")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return CreateDefaultFirmResult{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var result CreateDefaultFirmResult
	result.FirmName = firmName

	// firms is a global table with no RLS policy (see
	// migrations/0001_core_schema.up.sql) - no firm context is needed for
	// this first insert, since the firm doesn't exist yet for any
	// context to name.
	if err := tx.QueryRow(ctx, `
		INSERT INTO firms (name) VALUES ($1) RETURNING id
	`, firmName).Scan(&result.FirmID); err != nil {
		return CreateDefaultFirmResult{}, fmt.Errorf("insert firm: %w", err)
	}

	// Every table touched from here on is tenant-scoped and RLS-enforced
	// against app.current_firm_id (Never-Violate Rule 3). set_config's
	// third argument (true) scopes this to the current transaction only,
	// exactly like internal/platform/db.WithFirmContext does once a
	// firmID is already known.
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_firm_id', $1, true)", result.FirmID.String()); err != nil {
		return CreateDefaultFirmResult{}, fmt.Errorf("set firm context: %w", err)
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO roles (firm_id, key, name) VALUES ($1, $2, $3) RETURNING id
	`, result.FirmID, defaultRoleKey, "Owner").Scan(&result.RoleID); err != nil {
		return CreateDefaultFirmResult{}, fmt.Errorf("insert default role: %w", err)
	}

	definitionID, err := workflow.SeedStockToSaleWorkflowTx(ctx, tx, result.FirmID)
	if err != nil {
		return CreateDefaultFirmResult{}, fmt.Errorf("seed stock-to-sale workflow: %w", err)
	}
	result.StockToSaleDefinitionID = definitionID

	for _, key := range workflow.StockToSaleSpec.PermissionKeys() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (firm_id, role_id, permission_key) VALUES ($1, $2, $3)
		`, result.FirmID, result.RoleID, key); err != nil {
			return CreateDefaultFirmResult{}, fmt.Errorf("grant permission %q to default role: %w", key, err)
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)
	`, result.FirmID, userID, result.RoleID); err != nil {
		return CreateDefaultFirmResult{}, fmt.Errorf("insert user_firm_roles: %w", err)
	}

	changes, err := json.Marshal(map[string]any{"name": firmName})
	if err != nil {
		return CreateDefaultFirmResult{}, fmt.Errorf("marshal audit changes: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (firm_id, user_id, entity_type, entity_id, action, changes)
		VALUES ($1, $2, 'firm', $1, $3, $4)
	`, result.FirmID, userID, createFirmAuditAction, changes); err != nil {
		return CreateDefaultFirmResult{}, fmt.Errorf("write audit log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return CreateDefaultFirmResult{}, fmt.Errorf("commit transaction: %w", err)
	}

	return result, nil
}
