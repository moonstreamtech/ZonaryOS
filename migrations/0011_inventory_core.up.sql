-- Inventory management, warehouse stock, and supplier management: the
-- structural layer under the existing stock_to_sale/purchase_order
-- workflows - real product/quantity/location depth, not just a
-- freeform "item" string in a workflow instance's payload.
--
-- Tenant model: every table is firm-scoped, mandatory RLS (Rule 3), same
-- convention as every other migration in this repo. `stock_movements` is
-- append-only (no UPDATE/DELETE grant - see the GRANT statement below) -
-- the same immutability principle `journal_entries`/`journal_lines`
-- already establish for the ledger: a stock movement is a historical
-- fact, corrected by a new opposing movement, never rewritten in place.
--
-- Scope boundary: no warehouse picking/packing flows, no barcode
-- scanning, no multi-warehouse transfer UI (the data model supports
-- locations via stock_levels.location, but no transfer workflow yet), no
-- expiry/batch/lot tracking - see this batch's own scope-boundary note.

CREATE TABLE products (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    sku text NOT NULL,
    name text NOT NULL,
    description text,
    unit text NOT NULL DEFAULT 'piece',
    unit_price numeric(19,4),
    cost_price numeric(19,4),
    tax_rate numeric(5,2) DEFAULT 0,
    category text,
    -- min_quantity: the /inventory page's low-stock threshold (design
    -- brief item 4) - a plain per-product number, not a per-location or
    -- time-windowed reorder-point model (that's real MRP territory, out
    -- of scope here). Defaults to 0, meaning "never flagged low" unless
    -- an owner explicitly sets a real threshold - the safe, no-surprise
    -- default for a product nobody has configured yet.
    min_quantity numeric(19,4) NOT NULL DEFAULT 0,
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, sku)
);

-- One product's on-hand quantity at one location. UNIQUE (firm_id,
-- product_id, location) means "add stock" for a product/location pair
-- that already has a row is an UPDATE (upsert), not a second row - see
-- internal/inventory's own AdjustStockTx.
CREATE TABLE stock_levels (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    product_id uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    location text NOT NULL DEFAULT 'default',
    -- CHECK (quantity >= 0): the design brief's "if quantity would go
    -- below 0, fail the transition (not just a warning)" as a real
    -- database-level guarantee, not only an application-layer check -
    -- the same defense-in-depth reasoning Never-Violate Rule 3 applies
    -- to RLS: the application check (AdjustStockTx, internal/inventory)
    -- gives a clean, typed ErrInsufficientStock before this constraint
    -- would ever fire, but the constraint itself is what actually makes
    -- "stock can never go negative" true regardless of code path.
    quantity numeric(19,4) NOT NULL DEFAULT 0 CHECK (quantity >= 0),
    reserved_quantity numeric(19,4) NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, product_id, location)
);

-- An immutable, append-only log of every quantity change - the
-- inventory equivalent of journal_lines: quantity_change is signed
-- (positive for a purchase/increase, negative for a sale/decrease), so
-- summing quantity_change for a product/location reconstructs its
-- current stock_levels.quantity independently, the same "the ledger is
-- the source of truth, the balance is a derived cache" relationship
-- journal_entries/journal_lines has to an account's own computed
-- balance.
CREATE TABLE stock_movements (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    product_id uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    location text NOT NULL,
    quantity_change numeric(19,4) NOT NULL,
    -- "purchase" | "sale" | "adjustment" | "transfer" - app-validated
    -- (like accounts.type/contracts.type), not a DB CHECK: an open,
    -- growing vocabulary a firm-specific integration might extend.
    reason text NOT NULL,
    -- source_type/source_id record where this movement originated (e.g.
    -- "workflow_instance" + a workflow_instances.id) - same "not a
    -- foreign key, since source_type varies, validated by the writer not
    -- the schema" pattern audit_log/journal_entries already use.
    source_type text,
    source_id uuid,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE suppliers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    contact_name text,
    email text,
    phone text,
    address text,
    currency text NOT NULL DEFAULT 'TRY',
    payment_terms text,
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX products_firm_id_idx ON products (firm_id);
CREATE INDEX stock_levels_firm_id_idx ON stock_levels (firm_id);
CREATE INDEX stock_levels_product_id_idx ON stock_levels (product_id);
CREATE INDEX stock_movements_firm_id_idx ON stock_movements (firm_id);
CREATE INDEX stock_movements_product_id_idx ON stock_movements (product_id);
CREATE INDEX stock_movements_source_idx ON stock_movements (source_type, source_id);
CREATE INDEX suppliers_firm_id_idx ON suppliers (firm_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON products, stock_levels, suppliers TO zonaryos_app;
-- stock_movements is append-only (see this migration's own doc comment) -
-- zonaryos_app gets SELECT/INSERT only, no UPDATE/DELETE, enforced at the
-- database grant level, not merely by application-code convention.
GRANT SELECT, INSERT ON stock_movements TO zonaryos_app;

ALTER TABLE products ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_levels ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_movements ENABLE ROW LEVEL SECURITY;
ALTER TABLE suppliers ENABLE ROW LEVEL SECURITY;

CREATE POLICY products_tenant_isolation ON products
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY stock_levels_tenant_isolation ON stock_levels
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY stock_movements_tenant_isolation ON stock_movements
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY suppliers_tenant_isolation ON suppliers
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
