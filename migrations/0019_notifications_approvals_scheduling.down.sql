DROP POLICY IF EXISTS scheduled_rule_runs_tenant_isolation ON scheduled_rule_runs;
DROP POLICY IF EXISTS pending_approvals_tenant_isolation ON pending_approvals;
DROP POLICY IF EXISTS notifications_tenant_isolation ON notifications;

DROP TABLE IF EXISTS scheduled_rule_runs;
DROP TABLE IF EXISTS pending_approvals;
DROP TABLE IF EXISTS notifications;

ALTER TABLE workflow_rules DROP CONSTRAINT workflow_rules_trigger_check;
ALTER TABLE workflow_rules ADD CONSTRAINT workflow_rules_trigger_check
    CHECK (trigger IN ('on_transition', 'on_create'));
ALTER TABLE workflow_rules DROP COLUMN schedule_interval;
