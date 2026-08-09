DROP POLICY IF EXISTS edge_events_tenant_isolation ON edge_events;
DROP POLICY IF EXISTS edge_agent_tokens_tenant_isolation ON edge_agent_tokens;
DROP POLICY IF EXISTS edge_agents_tenant_isolation ON edge_agents;

DROP FUNCTION IF EXISTS app_current_edge_token_hash();

DROP TABLE IF EXISTS edge_events;
DROP TABLE IF EXISTS edge_agent_tokens;
DROP TABLE IF EXISTS edge_agents;
