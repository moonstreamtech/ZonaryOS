-- NATS JetStream + offline resilience + Edge Agent completion (Vision
-- §9, Open Points item 29): the Edge Agent protocol foundation
-- (migrations/0018_edge_agent_protocol.up.sql) had no command dispatch
-- (server -> agent) and no message-queue-backed offline resilience -
-- this migration adds the command queue's schema and the
-- on_edge_event rule trigger's schema change. The optional NATS
-- JetStream bridge itself (internal/edgeagent/nats.go) needs no schema
-- of its own - it publishes/consumes into a NATS stream, not a Postgres
-- table, and still ends up calling the same RecordEvent that inserts
-- into edge_events either way.
--
-- Tenant model: edge_agent_commands is firm-scoped, mandatory RLS (Rule
-- 3), same convention every other migration in this repo uses, and the
-- same shape edge_agents/edge_events already establish.

CREATE TABLE edge_agent_commands (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    agent_id uuid NOT NULL REFERENCES edge_agents (id) ON DELETE CASCADE,
    command_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'delivered', 'acknowledged', 'failed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz,
    acknowledged_at timestamptz
);

CREATE INDEX edge_agent_commands_firm_id_idx ON edge_agent_commands (firm_id);
-- GET /api/edge/commands' own query shape: pending commands for one
-- agent, oldest first.
CREATE INDEX edge_agent_commands_agent_id_status_idx ON edge_agent_commands (agent_id, status, created_at);

GRANT SELECT, INSERT, UPDATE ON edge_agent_commands TO zonaryos_app;

ALTER TABLE edge_agent_commands ENABLE ROW LEVEL SECURITY;

CREATE POLICY edge_agent_commands_tenant_isolation ON edge_agent_commands
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

-- on_edge_event (Part 3): the third rule trigger type after on_transition/
-- on_create (migrations/0008_workflow_rules.up.sql) and schedule
-- (migrations/0019_notifications_approvals_scheduling.up.sql) - same
-- "DROP then re-ADD the CHECK constraint" migration shape that batch's
-- own trigger addition used.
ALTER TABLE workflow_rules DROP CONSTRAINT workflow_rules_trigger_check;
ALTER TABLE workflow_rules
    ADD CONSTRAINT workflow_rules_trigger_check
    CHECK (trigger IN ('on_transition', 'on_create', 'schedule', 'on_edge_event'));
