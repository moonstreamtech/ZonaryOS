ALTER TABLE invoice_lines DROP COLUMN tax_rate_id;

DROP POLICY IF EXISTS tax_rates_tenant_isolation ON tax_rates;
DROP POLICY IF EXISTS addresses_tenant_isolation ON addresses;

DROP TABLE IF EXISTS tax_rates;
DROP TABLE IF EXISTS addresses;

DROP TABLE IF EXISTS exchange_rates;
