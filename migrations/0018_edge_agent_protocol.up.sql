-- Vision §9's Edge Agent: a Go process the firm runs on its own hardware
-- (a firm-specific account, auto-update, bidirectional bridge to the
-- central server). This migration is the server-side protocol only - see
-- internal/edgeagent's package doc comment for the full scope boundary
-- (no device binary, no WebSocket/long-poll push, no NATS JetStream, no
-- OTA mechanism; all deferred to later batches / Open Points item 29).
--
-- Tenant model: all three tables are firm-scoped, mandatory RLS (Rule 3),
-- same convention as every other migration in this repo.

CREATE TABLE edge_agents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    description text,
    status text NOT NULL DEFAULT 'offline'
        CHECK (status IN ('offline', 'online', 'error')),
    last_seen_at timestamptz,
    version text,
    capabilities jsonb NOT NULL DEFAULT '[]'::jsonb,
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- token_hash is the SHA-256 hash of the plaintext bearer token handed to
-- the agent once at issuance time (internal/edgeagent.IssueToken) - the
-- plaintext itself is never stored, mirroring this codebase's general
-- "never store a raw secret" posture. UNIQUE gives the token-lookup path
-- (internal/edgeagent's auth middleware, before any firm_id is known) an
-- index for free, the same load-bearing-uniqueness convention
-- migrations/0007_firm_invites.up.sql's firm_invites_token_idx documents.
CREATE TABLE edge_agent_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    agent_id uuid NOT NULL REFERENCES edge_agents (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    name text NOT NULL,
    last_used_at timestamptz,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE edge_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    agent_id uuid NOT NULL REFERENCES edge_agents (id) ON DELETE CASCADE,
    event_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    received_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX edge_agents_firm_id_idx ON edge_agents (firm_id);
CREATE INDEX edge_agent_tokens_firm_id_idx ON edge_agent_tokens (firm_id);
CREATE INDEX edge_agent_tokens_agent_id_idx ON edge_agent_tokens (agent_id);
CREATE INDEX edge_events_firm_id_idx ON edge_events (firm_id);
CREATE INDEX edge_events_agent_id_idx ON edge_events (agent_id, received_at);

GRANT SELECT, INSERT, UPDATE ON edge_agents, edge_agent_tokens TO zonaryos_app;
GRANT SELECT, INSERT ON edge_events TO zonaryos_app;

ALTER TABLE edge_agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE edge_agent_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE edge_events ENABLE ROW LEVEL SECURITY;

CREATE POLICY edge_agents_tenant_isolation ON edge_agents
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

-- Mirrors app_current_invite_token() (migrations/0007_firm_invites.up.sql)
-- exactly: the token-authenticated path (POST /api/edge/heartbeat,
-- POST /api/edge/events) has to look up its own edge_agent_tokens row by
-- token hash before any firm_id is known at all - that lookup is what
-- establishes the firm_id it then scopes the rest of the request to (see
-- internal/edgeagent.WithEdgeTokenContext). token_hash is a SHA-256 digest
-- of 32 random bytes, unguessable, so knowing it is equivalent to being
-- authorized to read that one row - same trust model as the invite token
-- carve-out. WITH CHECK is NOT widened the same way: every INSERT/UPDATE
-- still requires an established firm_id context, set only after this
-- read-side lookup succeeds and its firm_id has been validated - see
-- internal/edgeagent.WithEdgeTokenContext's own two-step sequencing.
CREATE FUNCTION app_current_edge_token_hash() RETURNS text
    LANGUAGE sql STABLE AS $$
    SELECT NULLIF(current_setting('app.current_edge_token_hash', true), '');
$$;

CREATE POLICY edge_agent_tokens_tenant_isolation ON edge_agent_tokens
    USING (firm_id = app_current_firm_id() OR token_hash = app_current_edge_token_hash())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY edge_events_tenant_isolation ON edge_events
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
