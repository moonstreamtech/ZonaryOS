-- Rule Engine data model (Open Points item 7, decided together with item
-- 12's wizard content): Vision §3's "Autonomous Decision Layer" - unlimited-
-- depth parametric "condition -> action" rules attached to a workflow.
-- Conditions are stored as an expression tree (jsonb), not a flat list, so
-- they can compose arbitrarily, including cross-workflow joins (e.g. "this
-- instance's payload AND that other-definition instance's current state") -
-- see internal/workflow/rules.go's ExpressionNode for the Go-side shape
-- this column holds.
--
-- Tenant model: firm-scoped, mandatory RLS (Rule 3), same convention as
-- 0003_workflow_engine.up.sql. definition_key (not a workflow_definitions.id
-- FK alone) plus firm_id is what a rule is actually scoped to - the
-- composite foreign key below piggybacks on workflow_definitions' own
-- UNIQUE (firm_id, key) constraint (0003) to guarantee a rule can never
-- reference a workflow definition that doesn't exist for its own firm.
CREATE TABLE workflow_rules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    definition_key text NOT NULL,
    name text NOT NULL,
    -- "on_transition" | "on_create" - schedule-triggered rules are
    -- deliberately deferred (docs/OPEN_POINTS.md), not modeled here at all
    -- rather than added as a third value nothing evaluates yet.
    trigger text NOT NULL,
    condition_tree jsonb NOT NULL,
    actions jsonb NOT NULL,
    -- Never-Violate Rule 8: "every rule has an explicit autonomous/human-
    -- approval toggle set by the end user - never hardcoded to always-
    -- autonomous." Defaults to false (requires approval) - the safer
    -- default when nothing sets it explicitly. See rules.go's EvaluateRules
    -- for what happens on each value: true runs the rule's actions inline;
    -- false only records a "pending approval" audit_log entry today - a
    -- real approval queue/UI to actually approve and run a pending rule is
    -- still open work (see docs/OPEN_POINTS.md).
    autonomous boolean NOT NULL DEFAULT false,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT workflow_rules_trigger_check CHECK (trigger IN ('on_transition', 'on_create')),
    FOREIGN KEY (firm_id, definition_key) REFERENCES workflow_definitions (firm_id, key) ON DELETE CASCADE
);

CREATE INDEX workflow_rules_firm_id_idx ON workflow_rules (firm_id);
-- The lookup EvaluateRules actually performs: every enabled rule for one
-- firm+definition+trigger combination, at the moment an instance is
-- created or transitioned.
CREATE INDEX workflow_rules_lookup_idx ON workflow_rules (firm_id, definition_key, trigger) WHERE enabled;

GRANT SELECT, INSERT, UPDATE, DELETE ON workflow_rules TO zonaryos_app;

ALTER TABLE workflow_rules ENABLE ROW LEVEL SECURITY;

CREATE POLICY workflow_rules_tenant_isolation ON workflow_rules
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
