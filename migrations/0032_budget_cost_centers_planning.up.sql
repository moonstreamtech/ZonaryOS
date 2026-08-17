-- Budget management, cost centers, and financial planning: every CFO
-- needs a budget-vs-actual view and cost-center-grouped spend, not just
-- the flat P&L internal/accounting already provides. Not a full FP&A
-- suite: single currency per budget (no multi-currency budget), no
-- budget revisions/versions (create a new budget instead of versioning
-- an old one), no rolling forecast, no cash flow forecasting.
--
-- Tenant model: every table is firm-scoped, mandatory RLS (Rule 3), same
-- convention as every other migration in this repo.

CREATE TABLE budgets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    period_type text NOT NULL CHECK (period_type IN ('monthly', 'quarterly', 'annual')),
    period_start date NOT NULL,
    period_end date NOT NULL,
    status text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'approved', 'active', 'closed')),
    currency text NOT NULL DEFAULT 'TRY',
    notes text,
    approved_by uuid REFERENCES users (id),
    approved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (period_end >= period_start)
);

CREATE TABLE budget_lines (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    budget_id uuid NOT NULL REFERENCES budgets (id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    planned_amount numeric(19,4) NOT NULL,
    notes text,
    UNIQUE (budget_id, account_id)
);

CREATE TABLE cost_centers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    code text NOT NULL,
    name text NOT NULL,
    parent_id uuid REFERENCES cost_centers (id),
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, code)
);

-- Optional cost-center tagging on journal lines (Part 2): nullable, so
-- every existing journal-posting call site (workflow bridges, manual
-- entries, every other package's PostJournalEntryTx call) keeps working
-- unchanged - only a bridged transition whose payload actually carries a
-- cost_center_id field sets this.
ALTER TABLE journal_lines ADD COLUMN cost_center_id uuid REFERENCES cost_centers (id);

CREATE INDEX budgets_firm_id_idx ON budgets (firm_id);
CREATE INDEX budgets_status_idx ON budgets (firm_id, status);
-- The active-budget-conflict check (internal/budget's own
-- deactivateOtherActiveBudgetsTx, mirroring internal/manufacturing's
-- deactivateOtherActiveBOMsTx) queries exactly this shape: other active
-- budgets for the firm, by period range.
CREATE INDEX budgets_active_period_idx ON budgets (firm_id, period_start, period_end) WHERE status = 'active';
CREATE INDEX budget_lines_firm_id_idx ON budget_lines (firm_id);
CREATE INDEX budget_lines_budget_id_idx ON budget_lines (budget_id);
CREATE INDEX cost_centers_firm_id_idx ON cost_centers (firm_id);
CREATE INDEX cost_centers_parent_id_idx ON cost_centers (parent_id);
CREATE INDEX journal_lines_cost_center_id_idx ON journal_lines (cost_center_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON budgets, budget_lines, cost_centers TO zonaryos_app;

ALTER TABLE budgets ENABLE ROW LEVEL SECURITY;
ALTER TABLE budget_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE cost_centers ENABLE ROW LEVEL SECURITY;

CREATE POLICY budgets_tenant_isolation ON budgets
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY budget_lines_tenant_isolation ON budget_lines
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY cost_centers_tenant_isolation ON cost_centers
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
