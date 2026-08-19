-- Mobile API optimization batch, Part 1: delta sync (?updated_since=)
-- needs an updated_at column on every entity a mobile client list-syncs -
-- products, customers, invoices lacked one (workflow_instances already
-- had it, migrations/0003). Additive, default now() - every existing row
-- gets "updated now" as its baseline, which is the correct semantics: a
-- client that has never synced treats every row as changed since any
-- ?updated_since= it might pass.
ALTER TABLE products ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE customers ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE invoices ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

-- (firm_id, updated_at) indexes make "?updated_since=" a fast range scan
-- rather than a full-table filter - the exact shape a delta-sync poll
-- performs on every call.
CREATE INDEX products_firm_id_updated_at_idx ON products (firm_id, updated_at);
CREATE INDEX customers_firm_id_updated_at_idx ON customers (firm_id, updated_at);
CREATE INDEX invoices_firm_id_updated_at_idx ON invoices (firm_id, updated_at);
