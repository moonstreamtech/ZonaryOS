DROP INDEX IF EXISTS suppliers_search_tsv_idx;
DROP TRIGGER IF EXISTS suppliers_search_tsv_trigger ON suppliers;
DROP FUNCTION IF EXISTS suppliers_search_tsv_update();
ALTER TABLE suppliers DROP COLUMN IF EXISTS search_tsv;

DROP INDEX IF EXISTS people_search_tsv_idx;
DROP TRIGGER IF EXISTS people_search_tsv_trigger ON people;
DROP FUNCTION IF EXISTS people_search_tsv_update();
ALTER TABLE people DROP COLUMN IF EXISTS search_tsv;

DROP INDEX IF EXISTS customers_search_tsv_idx;
DROP TRIGGER IF EXISTS customers_search_tsv_trigger ON customers;
DROP FUNCTION IF EXISTS customers_search_tsv_update();
ALTER TABLE customers DROP COLUMN IF EXISTS search_tsv;

DROP INDEX IF EXISTS products_search_tsv_idx;
DROP TRIGGER IF EXISTS products_search_tsv_trigger ON products;
DROP FUNCTION IF EXISTS products_search_tsv_update();
ALTER TABLE products DROP COLUMN IF EXISTS search_tsv;

DROP INDEX IF EXISTS workflow_instances_search_tsv_idx;
DROP TRIGGER IF EXISTS workflow_instances_search_tsv_trigger ON workflow_instances;
DROP FUNCTION IF EXISTS workflow_instances_search_tsv_update();
ALTER TABLE workflow_instances DROP COLUMN IF EXISTS search_tsv;

DROP FUNCTION IF EXISTS normalize_search_text(text);
