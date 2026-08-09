-- This migration backs three independent, mostly-unrelated additions
-- bundled into one batch: (1) no schema change of its own - the payload
-- validation refactor (internal/workflow/validation.go) is pure Go
-- restructuring; (2) the multi-currency foundation (exchange_rates); (3)
-- the address/tax framework foundation (Open Points item 24, still fully
-- open - this is data model only, no VAT/GST calculation logic, no
-- fiscal reporting).

-- Multi-currency foundation (foundation only - no real-time rate API
-- integration, manual entry only for now). Platform-wide, not
-- firm-scoped: an exchange rate between two ISO 4217 currency codes is
-- the same fact for every firm on the installation, the same "global,
-- not tenant-scoped" reasoning migrations/0001_core_schema.up.sql's own
-- `permissions` catalog table already establishes - no firm_id column,
-- no RLS. valid_to is nullable: a rate with no valid_to is open-ended
-- (still the current rate) until either a later valid_from for the same
-- pair supersedes it, or an operator explicitly closes it out.
CREATE TABLE exchange_rates (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    from_currency text NOT NULL,
    to_currency text NOT NULL,
    rate numeric(19,8) NOT NULL CHECK (rate > 0),
    valid_from timestamptz NOT NULL,
    valid_to timestamptz,
    source text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX exchange_rates_pair_valid
    ON exchange_rates (from_currency, to_currency, valid_from);
-- The lookup internal/currency.GetRateTx actually performs: the most
-- recent rate for one (from, to) pair valid at a given instant.
CREATE INDEX exchange_rates_lookup_idx ON exchange_rates (from_currency, to_currency, valid_from DESC);

GRANT SELECT, INSERT, UPDATE ON exchange_rates TO zonaryos_app;

-- Address and tax framework (foundation only, Open Points item 24 stays
-- fully open beyond this data model - no VAT/GST calculation, no fiscal
-- reporting). Both firm-scoped, mandatory RLS (Rule 3), same convention
-- as every other migration in this repo.
CREATE TABLE addresses (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    label text,
    line1 text NOT NULL,
    line2 text,
    city text NOT NULL,
    state_province text,
    postal_code text,
    country_code text NOT NULL,
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tax_rates (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    rate numeric(5,4) NOT NULL CHECK (rate >= 0),
    country_code text,
    region text,
    applies_to text,
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX addresses_firm_id_idx ON addresses (firm_id);
CREATE INDEX tax_rates_firm_id_idx ON tax_rates (firm_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON addresses TO zonaryos_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON tax_rates TO zonaryos_app;

ALTER TABLE addresses ENABLE ROW LEVEL SECURITY;
ALTER TABLE tax_rates ENABLE ROW LEVEL SECURITY;

CREATE POLICY addresses_tenant_isolation ON addresses
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY tax_rates_tenant_isolation ON tax_rates
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

-- tax_rate_id: an optional reference from an invoice line to a firm's own
-- tax_rates catalog entry - when set, internal/invoicing resolves and
-- overrides the line's own tax_rate (a plain numeric(5,2) percentage,
-- unlike tax_rates.rate's numeric(5,4) fraction - see
-- internal/invoicing's own doc comment on the conversion) with the
-- referenced rate at invoice-line-write time, not re-resolved later if
-- the tax_rates row subsequently changes - the same "resolved once, not
-- re-derived from a live reference" reasoning
-- migrations/0019_notifications_approvals_scheduling.up.sql's
-- pending_approvals.permission_key comment gives for its own analogous
-- case. ON DELETE SET NULL: deleting a tax_rates row un-links any invoice
-- line that referenced it, but never touches that line's own already-
-- resolved tax_rate value - an invoice's historical tax amount must never
-- change because a firm later deleted or edited its tax rate catalog.
ALTER TABLE invoice_lines ADD COLUMN tax_rate_id uuid REFERENCES tax_rates (id) ON DELETE SET NULL;
