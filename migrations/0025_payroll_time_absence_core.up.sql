-- Payroll foundation + time tracking + HR depth (absences) batch: three
-- more firm-scoped, RLS-mandatory tables building on migrations/0010's HR
-- core (people/contracts). Same tenant model as every other migration in
-- this repo (Rule 3): every table below is firm-scoped with RLS enabled
-- and a tenant-isolation policy.
--
-- Scope boundary (mirrors migrations/0010's own note): no tax/deduction
-- calculation, no payslip document generation, no overtime rules, no
-- shift scheduling - see this batch's own report for the full reasoning.

-- One payroll run window for a firm (e.g. "August 2026"). Entries are
-- attached below; closing a period is what posts the period's payroll to
-- the general ledger (see internal/payroll.ClosePeriod).
CREATE TABLE payroll_periods (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    period_start date NOT NULL,
    period_end date NOT NULL,
    -- 'open' (still editable) -> 'processing' (reserved for a future
    -- multi-step close workflow, not driven by any code in this batch -
    -- ClosePeriod moves straight from 'open' to 'closed') -> 'closed'
    -- (journal-posted, immutable). A small closed vocabulary, worth a
    -- real CHECK - same judgment call people.type made in migrations/0010.
    status text NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'processing', 'closed')),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- One person's payroll line within a period. UNIQUE(period_id, person_id):
-- a person gets at most one entry per period - a correction is an update
-- to the existing row, not a second row, while the period is still 'open'.
CREATE TABLE payroll_entries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    period_id uuid NOT NULL REFERENCES payroll_periods (id) ON DELETE CASCADE,
    person_id uuid NOT NULL REFERENCES people (id),
    contract_id uuid REFERENCES contracts (id),
    gross_amount numeric(19, 4) NOT NULL,
    -- Freeform breakdown (e.g. {"tax": "500.00", "insurance": "200.00"}) -
    -- no deduction *calculation* logic in this batch (Open Points item 24
    -- territory, explicitly out of scope), just a stored record of
    -- whatever deductions were applied to reach net_amount from
    -- gross_amount.
    deductions jsonb NOT NULL DEFAULT '{}',
    net_amount numeric(19, 4) NOT NULL,
    currency text NOT NULL DEFAULT 'TRY',
    notes text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (period_id, person_id)
);

-- Time tracking. created_by is additive beyond the task spec's literal
-- column list (internal/payroll's sibling package, internal/timetracking) -
-- needed to know who "owns" an entry for the edit-restriction requirement
-- ("a member may only edit/delete their OWN entries"): this codebase has
-- no existing users->people link to derive that from (people has no
-- user_id column), so created_by uuid NOT NULL REFERENCES users(id) is the
-- pragmatic interim rule - see internal/timetracking's own doc comment.
CREATE TABLE time_entries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    person_id uuid NOT NULL REFERENCES people (id),
    workflow_instance_id uuid REFERENCES workflow_instances (id),
    created_by uuid NOT NULL REFERENCES users (id),
    date date NOT NULL,
    hours numeric(4, 2) NOT NULL CHECK (hours > 0 AND hours <= 24),
    description text,
    billable boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Absences (vacations, sick leave, ...). Two additive columns beyond the
-- task spec's literal column list:
--   - workflow_instance_id links this row to the backing
--     workflow_instances row that actually drives its approval (see
--     internal/workflow/absence_approval.go and internal/absence's own
--     doc comment): approval here reuses the SAME pending_approvals +
--     notification mechanism the rest of this codebase's workflow engine
--     already has, not a bespoke parallel one.
--   - requested_by tracks who submitted the request - needed for the
--     "member-gated for own absences" edit/delete-while-pending rule
--     (internal/absence), the same reasoning migrations/0025's own
--     time_entries.created_by note gives: this codebase has no
--     users->people link to derive "this absence's person_id is this
--     caller" from, so the actual submitting user is tracked directly.
CREATE TABLE absences (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    person_id uuid NOT NULL REFERENCES people (id),
    workflow_instance_id uuid REFERENCES workflow_instances (id),
    requested_by uuid NOT NULL REFERENCES users (id),
    type text NOT NULL CHECK (type IN
        ('vacation', 'sick', 'unpaid', 'maternity', 'other')),
    start_date date NOT NULL,
    end_date date NOT NULL,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'rejected')),
    approved_by uuid,
    notes text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX payroll_periods_firm_id_idx ON payroll_periods (firm_id);
CREATE INDEX payroll_entries_firm_id_idx ON payroll_entries (firm_id);
CREATE INDEX payroll_entries_period_id_idx ON payroll_entries (period_id);
CREATE INDEX payroll_entries_person_id_idx ON payroll_entries (person_id);
CREATE INDEX time_entries_firm_id_idx ON time_entries (firm_id);
CREATE INDEX time_entries_person_id_idx ON time_entries (person_id);
CREATE INDEX time_entries_created_by_idx ON time_entries (created_by);
CREATE INDEX time_entries_date_idx ON time_entries (date);
CREATE INDEX absences_firm_id_idx ON absences (firm_id);
CREATE INDEX absences_person_id_idx ON absences (person_id);
CREATE INDEX absences_workflow_instance_id_idx ON absences (workflow_instance_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON payroll_periods, payroll_entries, time_entries, absences TO zonaryos_app;

ALTER TABLE payroll_periods ENABLE ROW LEVEL SECURITY;
ALTER TABLE payroll_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE time_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE absences ENABLE ROW LEVEL SECURITY;

CREATE POLICY payroll_periods_tenant_isolation ON payroll_periods
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY payroll_entries_tenant_isolation ON payroll_entries
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY time_entries_tenant_isolation ON time_entries
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY absences_tenant_isolation ON absences
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
