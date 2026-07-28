-- Generic, graph-based workflow engine (Vision §3, "Unlimited Process
-- Flexibility"): a business's process is a state machine defined by rows
-- in these tables, not by Go code per workflow. A 7-step manufacturing
-- flow is definable later as a bigger set of rows - no schema or engine
-- change required. This migration also seeds the global permission catalog
-- entries the first concrete workflow (Stock In -> Sale, see
-- internal/workflow/stock_to_sale.go) references; per-firm instantiation of
-- that workflow happens via internal/workflow.SeedStockToSaleWorkflow, not
-- here (firms don't exist yet at migration time).
--
-- Tenant model: every table below is firm-scoped (mandatory RLS, Rule 3),
-- consistent with 0001_core_schema.up.sql - Vision §3 is explicit that the
-- workflow *shape itself* differs per firm (a corner store's single-step
-- flow vs. an aircraft factory's multi-station one), so this is firm data,
-- not a global system template.

-- One named workflow "shape" a firm has defined (e.g. "Stock In -> Sale").
-- create_permission_key gates who may start a new instance of it - this is
-- the Rule 7 permission tag for the (structurally transition-less) act of
-- creating an instance, reusing the same permissions/role_permissions
-- catalog from 0001, not a new tagging mechanism.
CREATE TABLE workflow_definitions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    key text NOT NULL,
    name text NOT NULL,
    create_permission_key text NOT NULL REFERENCES permissions (key) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, key)
);

-- A node in the state graph.
CREATE TABLE workflow_states (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    workflow_definition_id uuid NOT NULL REFERENCES workflow_definitions (id) ON DELETE CASCADE,
    key text NOT NULL,
    name text NOT NULL,
    is_initial boolean NOT NULL DEFAULT false,
    is_terminal boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workflow_definition_id, key)
);

-- Exactly one initial state per definition - CreateInstance relies on this
-- being unambiguous.
CREATE UNIQUE INDEX workflow_states_one_initial_per_definition
    ON workflow_states (workflow_definition_id) WHERE is_initial;

-- An edge in the state graph: moving from one state to another via a named
-- action. permission_key is the Rule 7 tag for this transition, reusing the
-- 0001 permissions/role_permissions catalog - a firm's own role_permissions
-- grants decide which of that firm's roles may perform this action.
CREATE TABLE workflow_transitions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    workflow_definition_id uuid NOT NULL REFERENCES workflow_definitions (id) ON DELETE CASCADE,
    from_state_id uuid NOT NULL REFERENCES workflow_states (id) ON DELETE CASCADE,
    to_state_id uuid NOT NULL REFERENCES workflow_states (id) ON DELETE CASCADE,
    action_key text NOT NULL,
    name text NOT NULL,
    permission_key text NOT NULL REFERENCES permissions (key) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workflow_definition_id, from_state_id, action_key)
);

-- A running instance of a workflow (e.g. one unit/batch of stock).
CREATE TABLE workflow_instances (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    workflow_definition_id uuid NOT NULL REFERENCES workflow_definitions (id) ON DELETE CASCADE,
    current_state_id uuid NOT NULL REFERENCES workflow_states (id) ON DELETE RESTRICT,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by_user_id uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- General-purpose data-change audit trail (Vision §3 Audit Trail
-- Infrastructure - the "data change" tier only for now; view/read logging
-- is future work). Deliberately not workflow-specific: any future module
-- (inventory, sales, ...) writes here too - the workflow engine is just
-- this table's first writer. Not FK'd to a specific entity table since
-- entity_type varies; entity_id is validated by the writer, not the schema.
CREATE TABLE audit_log (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    entity_type text NOT NULL,
    entity_id uuid NOT NULL,
    action text NOT NULL,
    changes jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX workflow_definitions_firm_id_idx ON workflow_definitions (firm_id);
CREATE INDEX workflow_states_firm_id_idx ON workflow_states (firm_id);
CREATE INDEX workflow_transitions_firm_id_idx ON workflow_transitions (firm_id);
CREATE INDEX workflow_transitions_from_state_id_idx ON workflow_transitions (from_state_id);
CREATE INDEX workflow_instances_firm_id_idx ON workflow_instances (firm_id);
CREATE INDEX workflow_instances_workflow_definition_id_idx ON workflow_instances (workflow_definition_id);
CREATE INDEX audit_log_firm_id_idx ON audit_log (firm_id);
CREATE INDEX audit_log_entity_idx ON audit_log (entity_type, entity_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON
    workflow_definitions, workflow_states, workflow_transitions,
    workflow_instances, audit_log
    TO zonaryos_app;
-- permissions itself is a global catalog (0001 grants zonaryos_app SELECT
-- only); DefineWorkflow needs to upsert new permission keys into it as
-- workflows are defined, so the app role needs INSERT there too.
GRANT INSERT ON permissions TO zonaryos_app;

ALTER TABLE workflow_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_transitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_instances ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;

CREATE POLICY workflow_definitions_tenant_isolation ON workflow_definitions
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY workflow_states_tenant_isolation ON workflow_states
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY workflow_transitions_tenant_isolation ON workflow_transitions
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY workflow_instances_tenant_isolation ON workflow_instances
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY audit_log_tenant_isolation ON audit_log
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
