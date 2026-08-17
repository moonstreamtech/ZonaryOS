ALTER TABLE workflow_rules DROP CONSTRAINT workflow_rules_trigger_check;
ALTER TABLE workflow_rules
    ADD CONSTRAINT workflow_rules_trigger_check
    CHECK (trigger IN ('on_transition', 'on_create', 'schedule'));

DROP TABLE edge_agent_commands;
