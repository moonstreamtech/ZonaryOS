-- Financial management core (Vision §3's Accounting/finance scope): the
-- minimal double-entry ledger backbone every other module eventually
-- posts to. A firm's chart of accounts (parametric, seeded per-firm by
-- internal/wizard.CreateDefaultFirm based on its wizard answers, editable
-- after that via /settings/accounts) plus the journal_entries/journal_lines
-- pair that records every balanced debit/credit event against it.
--
-- Tenant model: every table below is firm-scoped, mandatory RLS (Rule 3),
-- same convention as 0001/0003/0007/0008. The "debits must equal credits"
-- invariant is deliberately NOT a CHECK/trigger here - a cross-row (across
-- journal_lines, for one journal_entries.id) sum constraint is impractical
-- to express and maintain as a plain Postgres CHECK, and a trigger-based
-- equivalent would duplicate logic internal/accounting.PostJournalEntry
-- already owns as the single write path into these tables. It is enforced
-- at the application layer instead, inside the same transaction that
-- inserts the entry and its lines - see PostJournalEntry's own doc comment
-- for the full reasoning.

-- Chart of accounts: firm-specific, parametric (Vision's design brief -
-- "not hardcoded"). parent_id supports a hierarchical chart (e.g. "1000
-- Assets" -> "1100 Current Assets" -> "1110 Cash") without requiring one -
-- a flat chart (parent_id NULL everywhere) is equally valid.
CREATE TABLE accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    code text NOT NULL,
    name text NOT NULL,
    -- "asset" | "liability" | "equity" | "revenue" | "expense" - validated
    -- in internal/accounting, not a DB CHECK/enum, matching this codebase's
    -- existing convention for workflow_rules.trigger and similar text-typed
    -- "closed set of values" columns (0008_workflow_rules.up.sql).
    type text NOT NULL,
    parent_id uuid REFERENCES accounts (id) ON DELETE SET NULL,
    currency text NOT NULL DEFAULT 'TRY',
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, code)
);

-- One financial event, recorded as a set of balanced debit/credit lines
-- (journal_lines below). Immutable once posted: this batch has no
-- update/delete path for either table, matching workflow_instances'
-- own "append transitions, never rewrite history" posture for the same
-- audit-trail reasons (Vision §3).
CREATE TABLE journal_entries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    posted_at timestamptz NOT NULL DEFAULT now(),
    description text NOT NULL,
    -- source_type/source_id record where this entry originated (e.g.
    -- "workflow_instance" + a workflow_instances.id, or "manual" + NULL) -
    -- deliberately not a foreign key, since source_type varies across
    -- entity tables, the same "entity_type/entity_id, validated by the
    -- writer not the schema" pattern audit_log already uses (0003).
    source_type text,
    source_id uuid,
    created_by uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- One debit or credit leg of a journal entry. amount is always positive
-- (CHECK below) - direction is carried by `side`, not by sign, so summing
-- "every debit amount" and "every credit amount" separately (rather than
-- a signed sum) is what PostJournalEntry's balance check and
-- GetAccountBalance's normal-balance-side math both do.
CREATE TABLE journal_lines (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    entry_id uuid NOT NULL REFERENCES journal_entries (id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    amount numeric(19,4) NOT NULL CHECK (amount > 0),
    side text NOT NULL CHECK (side IN ('debit', 'credit')),
    currency text NOT NULL DEFAULT 'TRY',
    created_at timestamptz NOT NULL DEFAULT now()
);

-- The workflow-to-ledger bridge (internal/workflow/spec.go's
-- TransitionSpec.Journal): an OPTIONAL journal template a transition can
-- carry, resolved against one instance's payload at ExecuteTransition
-- time (internal/workflow/engine.go) and posted through this batch's
-- internal/accounting.PostJournalEntryTx. NULL (every transition defined
-- before this column existed) means "post nothing", unchanged behavior -
-- see TransitionSpec.Journal's own doc comment.
ALTER TABLE workflow_transitions ADD COLUMN journal_template jsonb;

CREATE INDEX accounts_firm_id_idx ON accounts (firm_id);
CREATE INDEX accounts_parent_id_idx ON accounts (parent_id);
CREATE INDEX journal_entries_firm_id_idx ON journal_entries (firm_id);
CREATE INDEX journal_entries_source_idx ON journal_entries (source_type, source_id);
CREATE INDEX journal_lines_firm_id_idx ON journal_lines (firm_id);
CREATE INDEX journal_lines_entry_id_idx ON journal_lines (entry_id);
CREATE INDEX journal_lines_account_id_idx ON journal_lines (account_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON accounts, journal_entries, journal_lines TO zonaryos_app;

ALTER TABLE accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE journal_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE journal_lines ENABLE ROW LEVEL SECURITY;

CREATE POLICY accounts_tenant_isolation ON accounts
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY journal_entries_tenant_isolation ON journal_entries
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY journal_lines_tenant_isolation ON journal_lines
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
