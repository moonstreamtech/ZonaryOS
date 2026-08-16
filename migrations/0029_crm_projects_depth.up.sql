-- Full CRM depth (interaction tracking, opportunity/pipeline management)
-- and project management: the customer_pipeline workflow only gives
-- leads and customers, and the task_approval workflow is a minimal
-- generic task - neither carries real interaction history, a sales
-- pipeline, or project structure. Not full CRM/PM suites: no email
-- integration (item 17), no calendar sync, no lead scoring (ML), no
-- Gantt charts.
--
-- Tenant model: every table is firm-scoped, mandatory RLS (Rule 3), same
-- convention as every other migration in this repo.

CREATE TABLE crm_interactions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    customer_id uuid NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    type text NOT NULL CHECK (type IN ('call', 'email', 'meeting', 'note', 'demo', 'proposal')),
    date timestamptz NOT NULL,
    summary text NOT NULL,
    outcome text,
    next_action text,
    next_action_date date,
    -- Optional link to the opportunity this interaction was about - a
    -- discovery call/demo/proposal is usually in service of moving one
    -- specific deal forward, though a general "note" might not be.
    opportunity_id uuid,
    created_by uuid NOT NULL REFERENCES users (id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE crm_opportunities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    customer_id uuid NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    name text NOT NULL,
    value numeric(19,4),
    currency text NOT NULL DEFAULT 'TRY',
    stage text NOT NULL DEFAULT 'prospect'
        CHECK (stage IN ('prospect', 'qualified', 'proposal', 'negotiation', 'won', 'lost')),
    probability integer CHECK (probability BETWEEN 0 AND 100),
    expected_close_date date,
    assigned_to_person_id uuid REFERENCES people (id),
    notes text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE crm_interactions ADD CONSTRAINT crm_interactions_opportunity_id_fkey
    FOREIGN KEY (opportunity_id) REFERENCES crm_opportunities (id) ON DELETE SET NULL;

CREATE TABLE projects (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    description text,
    status text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'on_hold', 'completed', 'cancelled')),
    start_date date,
    end_date date,
    budget numeric(19,4),
    currency text NOT NULL DEFAULT 'TRY',
    manager_person_id uuid REFERENCES people (id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX crm_interactions_firm_id_idx ON crm_interactions (firm_id);
CREATE INDEX crm_interactions_customer_id_idx ON crm_interactions (customer_id);
CREATE INDEX crm_interactions_opportunity_id_idx ON crm_interactions (opportunity_id);
CREATE INDEX crm_opportunities_firm_id_idx ON crm_opportunities (firm_id);
CREATE INDEX crm_opportunities_customer_id_idx ON crm_opportunities (customer_id);
CREATE INDEX crm_opportunities_stage_idx ON crm_opportunities (firm_id, stage);
CREATE INDEX projects_firm_id_idx ON projects (firm_id);
CREATE INDEX projects_status_idx ON projects (firm_id, status);

GRANT SELECT, INSERT, UPDATE, DELETE ON crm_interactions, crm_opportunities, projects TO zonaryos_app;

ALTER TABLE crm_interactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE crm_opportunities ENABLE ROW LEVEL SECURITY;
ALTER TABLE projects ENABLE ROW LEVEL SECURITY;

CREATE POLICY crm_interactions_tenant_isolation ON crm_interactions
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY crm_opportunities_tenant_isolation ON crm_opportunities
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY projects_tenant_isolation ON projects
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
