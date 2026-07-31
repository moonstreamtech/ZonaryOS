-- Open Points item 36 (Broader Firm Metadata / Settings): five new,
-- nullable columns on the existing `firms` table (migrations/0001_core_schema.up.sql),
-- additive-only per this migration's own conventions and Never-Violate
-- Rule 6 - no new table, no change to any existing column, no backfill.
-- NULL means "never set", which is exactly the state every row this
-- table already has (every firm created before this migration ran) is
-- left in, so nothing existing changes behavior: internal/firm.UpdateName
-- (name-only) and every reader of a `firms` row keeps working unchanged
-- for a firm that never sets any of these.
--
-- This is deliberately still storage-only, matching item 4's own
-- narrowness (see internal/firm/firm.go's package doc comment) and
-- Open Points item 24 (fiscal/tax compliance) staying open:
--   * address        - free text. No structured address model exists (no
--                       street/city/postal-code split) because no fiscal-
--                       compliance system consumes it yet - inventing one
--                       now would be scope creep toward item 24.
--   * tax_id          - free text (VAT/tax ID). No country-specific
--                        format validation - see item 24.
--   * default_locale  - free text, expected to hold one of this
--                        deployment's actual locale codes ("en"/"tr" -
--                        web/src/i18n/routing.ts's `routing.locales`),
--                        but not DB-constrained to that set: the backend
--                        has no dependency on the frontend's locale list,
--                        and constraining it here would make adding a
--                        locale a migration instead of a frontend change.
--   * default_currency - free text, expected to look like an ISO 4217
--                         code, but not validated against a real currency
--                         list and not wired into any calculation -
--                         storage only, see item 24.
--   * logo_url        - free text URL. No upload/storage system - this is
--                        a link to an image hosted elsewhere, nothing else.
ALTER TABLE firms
    ADD COLUMN address text,
    ADD COLUMN tax_id text,
    ADD COLUMN default_locale text,
    ADD COLUMN default_currency text,
    ADD COLUMN logo_url text;
