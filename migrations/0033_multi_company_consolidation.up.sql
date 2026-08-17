-- Multi-company, consolidation, and group accounting: a single ZonaryOS
-- installation already supports multiple firms via RLS, but those firms
-- are completely independent. Many real businesses operate as a group -
-- a holding company with subsidiaries - so this adds the relationships
-- between otherwise-isolated firms. Minimal, by design: the foundation
-- for group structures, not a full consolidation engine (no minority
-- interest / non-controlling interest accounting, no consolidation
-- adjustments beyond inter-company elimination, no transfer pricing
-- compliance).
--
-- Tenant model, deliberately different from every prior migration: none
-- of these three tables is scoped to a single owning firm, and none get
-- RLS. firm_groups/firm_group_members are platform-level structures - a
-- group inherently spans firms, so no single firm's RLS context could
-- own it, the same "not RLS-scoped, no broader tenant above it"
-- treatment firms/users/permissions already get
-- (migrations/0001_core_schema.up.sql). firm_group_members' own firm
-- reference is deliberately named member_firm_id, not firm_id: this
-- column does not mean "the firm that owns/is scoped to this row" the
-- way every genuinely tenant-scoped table's firm_id column in this
-- codebase does (and which cmd/ciaudit's RLS audit mechanically checks
-- for) - it means "which firm this membership record is about," a
-- foreign-key reference like any other, not an ownership/isolation
-- boundary. intercompany_transactions is the same category for the same
-- reason: one row inherently spans from_firm_id/to_firm_id, so it cannot
-- be scoped to either firm's RLS context alone (and neither column
-- literally matches cmd/ciaudit's `firm_id`-as-its-own-word check).
-- Isolation for these three tables comes from access control instead:
-- only internal/consolidation's platform-admin-gated functions
-- (internal/platformadmin.Allowlist, the same allowlist
-- internal/currency's platform-admin-only write already reuses) ever
-- read or write firm_groups/firm_group_members/intercompany_transactions
-- directly - no firm-scoped endpoint exposes intercompany_transactions'
-- rows or any financial figures from it (see
-- internal/consolidation.GetFirmGroupForFirm's own doc comment: a
-- subsidiary shouldn't see the parent's books, so GET
-- /api/firms/{firmID}/group returns member firm names only).

CREATE TABLE firm_groups (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE firm_group_members (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id uuid NOT NULL REFERENCES firm_groups (id) ON DELETE CASCADE,
    member_firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('parent', 'subsidiary', 'affiliate')),
    ownership_pct numeric(5,2) CHECK (ownership_pct IS NULL OR (ownership_pct >= 0 AND ownership_pct <= 100)),
    joined_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (group_id, member_firm_id)
);

-- Inter-company transactions (Part 2): a group-level record of a
-- transfer between two member firms, each side backed by a real journal
-- entry posted in that firm's own books
-- (internal/consolidation.PostIntercompanyTransfer) - see that
-- function's own doc comment for how a single atomic Postgres
-- transaction posts into two different firms' RLS contexts, a first for
-- this codebase.
CREATE TABLE intercompany_transactions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id uuid NOT NULL REFERENCES firm_groups (id) ON DELETE CASCADE,
    from_firm_id uuid NOT NULL REFERENCES firms (id),
    to_firm_id uuid NOT NULL REFERENCES firms (id),
    amount numeric(19,4) NOT NULL CHECK (amount > 0),
    currency text NOT NULL,
    description text NOT NULL,
    from_journal_entry_id uuid REFERENCES journal_entries (id),
    to_journal_entry_id uuid REFERENCES journal_entries (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (from_firm_id != to_firm_id)
);

CREATE INDEX firm_group_members_group_id_idx ON firm_group_members (group_id);
CREATE INDEX firm_group_members_member_firm_id_idx ON firm_group_members (member_firm_id);
CREATE INDEX intercompany_transactions_group_id_idx ON intercompany_transactions (group_id);
CREATE INDEX intercompany_transactions_from_firm_id_idx ON intercompany_transactions (from_firm_id);
CREATE INDEX intercompany_transactions_to_firm_id_idx ON intercompany_transactions (to_firm_id);
-- The consolidated-P&L elimination filter (internal/consolidation's
-- firmPnLTotalsTx) looks up, per journal_entries row, whether it is
-- either leg of an intercompany transfer - exactly this shape.
CREATE INDEX intercompany_transactions_from_journal_entry_id_idx ON intercompany_transactions (from_journal_entry_id);
CREATE INDEX intercompany_transactions_to_journal_entry_id_idx ON intercompany_transactions (to_journal_entry_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON firm_groups, firm_group_members, intercompany_transactions TO zonaryos_app;

-- No RLS on any of these three tables - see this migration's own doc
-- comment above for why (the same category firms/users/permissions
-- already occupy).
