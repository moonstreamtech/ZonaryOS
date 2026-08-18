-- Data pipeline, ETL, and system integrations batch. Vision mentions
-- import/export without vendor lock-in; ZonaryOS already has JSON
-- import/export (internal/portability). This batch adds a real,
-- ongoing data pipeline on top of that: connectors, field mappings, and
-- sync history. Firm-scoped, mandatory RLS (Rule 3), same convention as
-- every other migration in this repo.

CREATE TABLE integration_connectors (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    type text NOT NULL CHECK (type IN ('http_webhook', 'ftp', 'sftp', 'database', 'email_imap', 'custom')),
    -- config holds connection params as plain jsonb - a sensitive key
    -- (any of "apiKey", "password", "secret", "token", by fixed
    -- convention, see internal/integration/crypto.go's own doc comment)
    -- is stored AES-256-GCM-encrypted in place, the rest (url, username,
    -- pollIntervalMinutes, ...) in plaintext - same
    -- ZONARYOS_INTEGRATION_ENCRYPTION_KEY-gated, default-off pattern
    -- internal/ai's own api_key_encrypted column already establishes for
    -- AI provider keys, reusing the identical AES-256-GCM primitive
    -- (internal/ai.Encryptor), not a separate crypto implementation.
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    is_active boolean NOT NULL DEFAULT true,
    last_sync_at timestamptz,
    last_sync_status text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX integration_connectors_firm_id_idx ON integration_connectors (firm_id);
CREATE INDEX integration_connectors_active_idx ON integration_connectors (firm_id) WHERE is_active;

GRANT SELECT, INSERT, UPDATE, DELETE ON integration_connectors TO zonaryos_app;

ALTER TABLE integration_connectors ENABLE ROW LEVEL SECURITY;

CREATE POLICY integration_connectors_tenant_isolation ON integration_connectors
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

-- integration_mappings.target_entity uses the SAME vocabulary
-- internal/reports' own entityRegistry does NOT need to match exactly -
-- Part 3 only ever executes "products"/"customers" for actual create/
-- update (documented in internal/integration/inbound.go); any other
-- target_entity value is accepted by this table (no CHECK constraint on
-- target_entity - deliberately open-ended, since a future batch may wire
-- up more entities without a migration) but the scheduler returns a
-- clear "target entity not yet supported for inbound sync" error rather
-- than silently doing nothing.
CREATE TABLE integration_mappings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    connector_id uuid NOT NULL REFERENCES integration_connectors (id) ON DELETE CASCADE,
    source_entity text NOT NULL,
    target_entity text NOT NULL,
    -- field_mappings: [{source_field, target_field, transform}], transform
    -- is either a single string ("uppercase") or an array of strings
    -- (["trim","uppercase"], applied left to right) - see
    -- internal/integration/transform.go's own TransformChain type for the
    -- flexible-unmarshal handling of both shapes.
    field_mappings jsonb NOT NULL,
    sync_direction text NOT NULL CHECK (sync_direction IN ('inbound', 'outbound', 'bidirectional')),
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX integration_mappings_firm_id_idx ON integration_mappings (firm_id);
CREATE INDEX integration_mappings_connector_id_idx ON integration_mappings (connector_id);
-- The exact lookup DispatchOutbound (outbound.go) performs on every
-- product/customer/invoice change: "which active outbound/bidirectional
-- mappings exist for this firm + source entity."
CREATE INDEX integration_mappings_outbound_lookup_idx
    ON integration_mappings (firm_id, source_entity)
    WHERE is_active;

GRANT SELECT, INSERT, UPDATE, DELETE ON integration_mappings TO zonaryos_app;

ALTER TABLE integration_mappings ENABLE ROW LEVEL SECURITY;

CREATE POLICY integration_mappings_tenant_isolation ON integration_mappings
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE TABLE integration_sync_logs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    connector_id uuid NOT NULL REFERENCES integration_connectors (id) ON DELETE CASCADE,
    mapping_id uuid REFERENCES integration_mappings (id) ON DELETE SET NULL,
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    records_processed integer DEFAULT 0,
    records_failed integer DEFAULT 0,
    status text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'success', 'partial', 'failed')),
    error_log jsonb
);

CREATE INDEX integration_sync_logs_firm_id_started_at_idx ON integration_sync_logs (firm_id, started_at DESC);
CREATE INDEX integration_sync_logs_connector_id_idx ON integration_sync_logs (connector_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON integration_sync_logs TO zonaryos_app;

ALTER TABLE integration_sync_logs ENABLE ROW LEVEL SECURITY;

CREATE POLICY integration_sync_logs_tenant_isolation ON integration_sync_logs
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
