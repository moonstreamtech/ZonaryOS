-- Webhooks, API keys, and external integration foundation: two
-- independent additions bundled into one migration, same "one migration
-- per batch, several tables" convention earlier bundled batches use.
--
-- Tenant model: all three tables are firm-scoped, mandatory RLS (Rule 3),
-- same convention as every other migration in this repo.

-- key_hash is the SHA-256 hash of the plaintext API key handed to the
-- caller once at creation time (internal/apikey.CreateAPIKey) - the
-- plaintext itself is never stored, mirroring edge_agent_tokens'
-- token_hash convention exactly (migrations/0018_edge_agent_protocol.up.sql).
-- UNIQUE gives the key-lookup path (internal/apikey's auth fallback,
-- before any firm_id is known) an index for free, the same
-- load-bearing-uniqueness convention edge_agent_tokens.token_hash
-- documents. scopes is a subset of created_by's own held permission_keys
-- at creation time (validated then, in Go - see CreateAPIKey's own doc
-- comment; not re-validated afterward if the creating user's own
-- permissions later change, the same "resolved once" convention
-- migrations/0019_notifications_approvals_scheduling.up.sql's
-- pending_approvals.permission_key comment documents for an analogous
-- case).
CREATE TABLE api_keys (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    key_hash text NOT NULL UNIQUE,
    scopes text[] NOT NULL DEFAULT '{}',
    created_by uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    last_used_at timestamptz,
    expires_at timestamptz,
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX api_keys_firm_id_idx ON api_keys (firm_id);

GRANT SELECT, INSERT, UPDATE ON api_keys TO zonaryos_app;

ALTER TABLE api_keys ENABLE ROW LEVEL SECURITY;

-- Mirrors app_current_edge_token_hash() (migrations/0018) exactly: the
-- API-key-authenticated path (internal/identity.Middleware's Fallback,
-- backed by internal/apikey) has to look up its own api_keys row by hash
-- before any firm_id is known at all - that lookup is what establishes
-- the firm_id it then scopes the rest of the request to (see
-- internal/apikey.authenticate). key_hash is a SHA-256 digest of 32
-- random bytes, unguessable, so knowing it is equivalent to being
-- authorized to read that one row - same trust model as the edge-token
-- carve-out. WITH CHECK is NOT widened the same way: every INSERT/UPDATE
-- still requires an established firm_id context.
CREATE FUNCTION app_current_api_key_hash() RETURNS text
    LANGUAGE sql STABLE AS $$
    SELECT NULLIF(current_setting('app.current_api_key_hash', true), '');
$$;

CREATE POLICY api_keys_tenant_isolation ON api_keys
    USING (firm_id = app_current_firm_id() OR key_hash = app_current_api_key_hash())
    WITH CHECK (firm_id = app_current_firm_id());

-- Webhooks (Part 2): secret is stored PLAINTEXT (unlike api_keys.key_hash/
-- edge_agent_tokens.token_hash) - it's an HMAC signing key an owner needs
-- to read back at any time to configure their own receiving endpoint's
-- signature verification, not a bearer credential ZonaryOS itself needs
-- to authenticate a caller against (see internal/webhook's own doc
-- comment on this deliberate asymmetry with api_keys/edge_agent_tokens).
CREATE TABLE webhooks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    url text NOT NULL,
    events text[] NOT NULL DEFAULT '{}',
    secret text NOT NULL,
    is_active boolean NOT NULL DEFAULT true,
    last_triggered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE webhook_deliveries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    webhook_id uuid NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    response_status integer,
    response_body text,
    delivered_at timestamptz NOT NULL DEFAULT now(),
    success boolean NOT NULL DEFAULT false
);

CREATE INDEX webhooks_firm_id_idx ON webhooks (firm_id);
-- The dispatch path's own main query: every active webhook in a firm
-- subscribed to a given event type (GIN, since events is queried with
-- the array-containment operator `events @> ARRAY[$1]`).
CREATE INDEX webhooks_events_gin_idx ON webhooks USING GIN (events);
CREATE INDEX webhook_deliveries_firm_id_idx ON webhook_deliveries (firm_id);
CREATE INDEX webhook_deliveries_webhook_id_idx ON webhook_deliveries (webhook_id, delivered_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON webhooks TO zonaryos_app;
GRANT SELECT, INSERT ON webhook_deliveries TO zonaryos_app;

ALTER TABLE webhooks ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_deliveries ENABLE ROW LEVEL SECURITY;

CREATE POLICY webhooks_tenant_isolation ON webhooks
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY webhook_deliveries_tenant_isolation ON webhook_deliveries
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
