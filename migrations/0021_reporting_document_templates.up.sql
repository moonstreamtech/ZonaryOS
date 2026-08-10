-- Vision §3's reporting engine (parametric report definitions, replacing
-- internal/reports' fixed KPI dashboard for anything a firm wants to
-- define itself) and document templates (internal/invoicing's own "no PDF
-- generation, structured data first" scope boundary's natural next step -
-- HTML rendering only, see internal/documents' own doc comment). All
-- three tables are firm-scoped, mandatory RLS (Rule 3), same convention
-- as every other migration in this repo.

CREATE TABLE report_definitions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    description text,
    -- query_spec is a structured descriptor (entity/filters/group_by/
    -- metrics/date_range), never raw SQL - see internal/reports.QuerySpec
    -- and BuildQuery, which translate it into a parameterized query
    -- against a hardcoded per-entity field allow-list. Users never get a
    -- general-purpose SQL interface.
    query_spec jsonb NOT NULL,
    created_by uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE saved_report_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    definition_id uuid NOT NULL REFERENCES report_definitions (id) ON DELETE CASCADE,
    run_by uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    status text NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'completed', 'failed')),
    result jsonb,
    error_text text
);

CREATE INDEX report_definitions_firm_id_idx ON report_definitions (firm_id);
CREATE INDEX saved_report_runs_firm_id_idx ON saved_report_runs (firm_id);
CREATE INDEX saved_report_runs_definition_id_idx ON saved_report_runs (definition_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON report_definitions TO zonaryos_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON saved_report_runs TO zonaryos_app;

ALTER TABLE report_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_report_runs ENABLE ROW LEVEL SECURITY;

CREATE POLICY report_definitions_tenant_isolation ON report_definitions
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY saved_report_runs_tenant_isolation ON saved_report_runs
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

-- Document templates (internal/documents): Handlebars/Mustache-*looking*
-- {{field}} syntax, but rendered with Go's stdlib html/template (auto-
-- escaping, no new dependency) - see internal/documents' own doc comment
-- for why that's a deliberate simplification, not full Mustache/Handlebars
-- semantics (no sections, no partials).
CREATE TABLE document_templates (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    type text NOT NULL CHECK (type IN ('invoice', 'delivery_note', 'report')),
    template text NOT NULL,
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX document_templates_firm_id_idx ON document_templates (firm_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON document_templates TO zonaryos_app;

ALTER TABLE document_templates ENABLE ROW LEVEL SECURITY;

CREATE POLICY document_templates_tenant_isolation ON document_templates
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
