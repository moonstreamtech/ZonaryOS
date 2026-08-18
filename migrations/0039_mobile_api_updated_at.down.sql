DROP INDEX IF EXISTS invoices_firm_id_updated_at_idx;
DROP INDEX IF EXISTS customers_firm_id_updated_at_idx;
DROP INDEX IF EXISTS products_firm_id_updated_at_idx;

ALTER TABLE invoices DROP COLUMN updated_at;
ALTER TABLE customers DROP COLUMN updated_at;
ALTER TABLE products DROP COLUMN updated_at;
