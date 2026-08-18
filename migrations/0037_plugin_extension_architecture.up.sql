-- Plugin/extension architecture + developer API/SDK foundation. Vision's
-- own "extensible architecture" ask: the first real extension layer over
-- what has otherwise been a pure modify-the-core-codebase monolith.
--
-- plugins is the platform-wide catalog - a plugin is registered by a
-- platform admin (compiled into the binary, Part 2's own "no dynamic
-- loading" scope boundary; this row is metadata/discoverability, not the
-- code itself), the same "global, not tenant-scoped" shape
-- migrations/0020's exchange_rates and migrations/0036's
-- fiscal_country_configs already establish - no firm_id, no RLS.
CREATE TABLE plugins (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    version text NOT NULL,
    description text,
    author text,
    homepage_url text,
    -- entry_point is documentation only in this batch (e.g.
    -- "internal/plugins/activitylog") - Part 2's own "registered at
    -- compile time" scope boundary means this column doesn't drive any
    -- actual loading/dispatch; internal/plugin.Registry's Go-level
    -- registration (cmd/server/main.go) is what really wires a plugin
    -- in. Kept anyway so the catalog is a complete, human-readable record
    -- of what's compiled into this installation, independent of reading
    -- main.go.
    entry_point text NOT NULL,
    -- capabilities is a plain text[] of hook-interface names this plugin
    -- implements (e.g. '{workflow_hook,kpi_provider}') - informational,
    -- surfaced to a platform admin deciding what a plugin does; not
    -- itself validated against internal/plugin's own registered
    -- interfaces (a mismatch here is a metadata bug, not a runtime
    -- failure - the real dispatch only ever reaches Go code that
    -- actually registered itself, regardless of what this column claims).
    capabilities text[] NOT NULL DEFAULT '{}',
    is_enabled boolean NOT NULL DEFAULT false,
    -- config_schema is a JSON Schema document describing the shape
    -- firm_plugin_configs.config is expected to have for THIS plugin -
    -- documentation/future-UI-generation only in this batch, not enforced
    -- at write time (no JSON Schema validator dependency added here).
    config_schema jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE, DELETE ON plugins TO zonaryos_app;

-- firm_plugin_configs is firm-scoped (a firm opts a platform-registered
-- plugin in/out, with its own config) - mandatory RLS (Rule 3), same
-- convention as every other firm-scoped table in this repo.
CREATE TABLE firm_plugin_configs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    plugin_id uuid NOT NULL REFERENCES plugins (id) ON DELETE CASCADE,
    config jsonb NOT NULL DEFAULT '{}',
    is_enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, plugin_id)
);

CREATE INDEX firm_plugin_configs_firm_id_idx ON firm_plugin_configs (firm_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON firm_plugin_configs TO zonaryos_app;

ALTER TABLE firm_plugin_configs ENABLE ROW LEVEL SECURITY;

CREATE POLICY firm_plugin_configs_tenant_isolation ON firm_plugin_configs
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

-- activity_log (Part 3, internal/plugins/activitylog's own table) is
-- deliberately a SEPARATE table from audit_log
-- (migrations/0003_workflow_engine.up.sql) - audit_log is this
-- codebase's own structured, every-field-change compliance/security
-- trail (Vision §3), read through one gated path
-- (internal/auditlog.List). activity_log is a higher-level, human-
-- readable feed ("Kaan sold 5 units of Widget A") a plugin author
-- builds, with no compliance guarantee and no relationship to
-- audit_log's own schema or access-control story - proving the plugin
-- hook system can introduce genuinely new, plugin-owned persistent state
-- without touching any core table. Firm-scoped, mandatory RLS, same
-- convention as every other migration in this repo.
CREATE TABLE activity_log (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    description text NOT NULL,
    -- source_instance_id is the workflow_instances.id (no FK - workflow
    -- instances belong to internal/workflow, and this plugin package must
    -- not import it, only internal/plugin's own hook-argument types) the
    -- TransitionEvent that produced this entry came from, nullable so a
    -- future plugin-authored entry with a different origin isn't forced
    -- to fabricate one.
    source_instance_id uuid,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX activity_log_firm_id_created_at_idx ON activity_log (firm_id, created_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON activity_log TO zonaryos_app;

ALTER TABLE activity_log ENABLE ROW LEVEL SECURITY;

CREATE POLICY activity_log_tenant_isolation ON activity_log
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
