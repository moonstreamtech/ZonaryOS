-- Multi-language UI, localization depth, and fiscal compliance
-- foundation. Part 3 of this batch: a narrow, genuinely useful slice of
-- Open Points item 24 (fiscal compliance), which has been deferred many
-- times - VAT/tax-number FORMAT validation per country, not tax
-- calculation, not e-invoicing compliance (item 24 stays open beyond
-- this).
--
-- Platform-wide reference data, not firm-scoped: a country's VAT number
-- format/label/currency/date format/fiscal year start is the same fact
-- for every firm on the installation, the same "global, not tenant-
-- scoped" reasoning migrations/0020's own exchange_rates table already
-- establishes for exchange rates - no firm_id column, no RLS.
-- Platform-admin manages this table (internal/localization's own
-- platform-admin-gated write routes, same allowlist-gating convention
-- internal/currency's POST /api/exchange-rates already uses).
CREATE TABLE fiscal_country_configs (
    country_code text PRIMARY KEY,
    -- vat_number_pattern is a regex pattern (Go regexp/RE2 syntax, since
    -- internal/localization validates it with Go's own regexp package,
    -- not Postgres's POSIX-flavored ~ operator) - nullable, since not
    -- every seeded country's VAT/tax-number format is known yet, and a
    -- NULL pattern means "no format validation for this country," not
    -- "reject everything."
    vat_number_pattern text,
    vat_number_label text NOT NULL,
    currency_code text NOT NULL,
    date_format text NOT NULL,
    fiscal_year_start_month integer NOT NULL DEFAULT 1
        CHECK (fiscal_year_start_month BETWEEN 1 AND 12)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON fiscal_country_configs TO zonaryos_app;

-- Seed rows for the five countries this batch's own brief names. Real
-- VAT-number regex patterns per country (kept deliberately simple/
-- permissive - format validation is a soft, non-blocking warning per
-- Part 3's own "warn if invalid pattern, don't block" contract, not a
-- rejection gate, so a slightly-too-permissive pattern is the safe
-- direction to err in):
--   TR - Turkish tax ID (Vergi Kimlik Numarası / VKN): 10 digits.
--   DE - German USt-IdNr: "DE" + 9 digits.
--   GB - UK VAT number: "GB" + 9 or 12 digits (simplified - the real
--        format also allows a 5-character government/health-authority
--        prefix, not modeled here).
--   US - no federal VAT/GST; EIN (Employer Identification Number) format
--        (NN-NNNNNNN) used as the closest equivalent "tax number" concept.
--   FR - French TVA intracommunautaire: "FR" + 2 alphanumeric + 9 digits.
INSERT INTO fiscal_country_configs (country_code, vat_number_pattern, vat_number_label, currency_code, date_format, fiscal_year_start_month) VALUES
    ('TR', '^[0-9]{10}$', 'VKN (Vergi Kimlik No)', 'TRY', 'DD.MM.YYYY', 1),
    ('DE', '^DE[0-9]{9}$', 'USt-IdNr.', 'EUR', 'DD.MM.YYYY', 1),
    ('GB', '^GB([0-9]{9}|[0-9]{12})$', 'VAT Number', 'GBP', 'DD/MM/YYYY', 4),
    ('US', '^[0-9]{2}-[0-9]{7}$', 'EIN', 'USD', 'MM/DD/YYYY', 1),
    ('FR', '^FR[0-9A-Z]{2}[0-9]{9}$', 'Numéro de TVA', 'EUR', 'DD/MM/YYYY', 1);
