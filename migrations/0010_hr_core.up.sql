-- HR core (Vision §3's end-to-end scope: every business has people).
-- Minimal, parametric people/contracts foundation - `custom_fields` jsonb
-- on `people` gives firm-specific HR attributes without a schema change
-- (the same "PostgreSQL + JSONB hybrid" principle Vision §4's Database
-- Layer decision describes), the same way `firms.attributes` already
-- does for firm-level data (migrations/0001_core_schema.up.sql).
--
-- Tenant model: both tables are firm-scoped, mandatory RLS (Rule 3), same
-- convention as every other migration in this repo.
--
-- Scope boundary: no payroll calculation/payment (tax/deduction logic,
-- Open Points item 24 territory), no time tracking, no performance
-- reviews, no organizational chart - contracts here are a plain record of
-- terms (type/amount/currency/dates), not a computation engine.

CREATE TABLE people (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    full_name text NOT NULL,
    -- "employee" | "contractor" | "partner" - a closed, small set worth a
    -- real CHECK (unlike accounts.type/workflow_rules.trigger, which stay
    -- app-validated text columns elsewhere in this codebase): this set is
    -- structural to what a "person" record means in this domain, not an
    -- open-ended catalog a firm might extend, so constraining it at the
    -- database level costs nothing and catches a real class of bugs.
    type text NOT NULL CHECK (type IN ('employee', 'contractor', 'partner')),
    email text,
    phone text,
    start_date date,
    end_date date,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- One contract term attached to a person - a firm can have several over
-- time (a renewal, a rate change), so this is append-only history, not a
-- single mutable row on `people` itself.
CREATE TABLE contracts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    person_id uuid NOT NULL REFERENCES people (id) ON DELETE CASCADE,
    -- "salary" | "hourly" | "fixed" | "commission" - app-validated (like
    -- accounts.type), not a DB CHECK: unlike people.type above, this is
    -- closer to an open, growing vocabulary of compensation structures a
    -- firm-specific need might extend, the same judgment call
    -- accounts.type already made.
    type text NOT NULL,
    amount numeric(19,4),
    currency text NOT NULL DEFAULT 'TRY',
    start_date date NOT NULL,
    end_date date,
    notes text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX people_firm_id_idx ON people (firm_id);
CREATE INDEX contracts_firm_id_idx ON contracts (firm_id);
CREATE INDEX contracts_person_id_idx ON contracts (person_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON people, contracts TO zonaryos_app;

ALTER TABLE people ENABLE ROW LEVEL SECURITY;
ALTER TABLE contracts ENABLE ROW LEVEL SECURITY;

CREATE POLICY people_tenant_isolation ON people
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY contracts_tenant_isolation ON contracts
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
