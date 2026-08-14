-- Manufacturing module foundation: Bill of Materials (BOM) and
-- production orders - the data model and operations foundation for
-- Vision's manufacturing domain (previously a wizard dead end, "coming
-- soon"). Not a full MES: no MRP, no capacity planning, no
-- machine/workstation routing, no batch/lot tracking.
--
-- Tenant model: every table is firm-scoped, mandatory RLS (Rule 3), same
-- convention as every other migration in this repo.

CREATE TABLE bom_headers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    product_id uuid NOT NULL REFERENCES products (id),
    name text NOT NULL,
    version text NOT NULL DEFAULT '1.0',
    -- Only one BOM version may be active per (firm, product) at a time -
    -- enforced in Go (internal/manufacturing.SetBOMActive), not by a
    -- partial unique index here: Postgres has no direct "at most one row
    -- WHERE is_active" constraint expressible as a plain UNIQUE, and a
    -- partial unique index (UNIQUE ... WHERE is_active) would need the
    -- deactivate-others step and the activate step to be two separate
    -- statements anyway - the same atomicity a single transaction already
    -- gives without one, so the app-level enforcement is both necessary
    -- and sufficient here.
    is_active boolean NOT NULL DEFAULT true,
    unit_yield numeric(10,4) NOT NULL DEFAULT 1 CHECK (unit_yield > 0),
    notes text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, product_id, version)
);

CREATE TABLE bom_lines (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    bom_id uuid NOT NULL REFERENCES bom_headers (id) ON DELETE CASCADE,
    component_product_id uuid NOT NULL REFERENCES products (id),
    quantity_per_unit numeric(10,4) NOT NULL CHECK (quantity_per_unit > 0),
    unit text NOT NULL DEFAULT 'piece',
    notes text
);

CREATE TABLE production_orders (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    -- Sequential per firm (MO-0001, MO-0002, ...) - see
    -- production_order_sequences below, the same atomic-UPSERT-counter
    -- shape invoice_sequences/sales_order_sequences already established.
    order_number text NOT NULL,
    product_id uuid NOT NULL REFERENCES products (id),
    bom_id uuid NOT NULL REFERENCES bom_headers (id),
    quantity_planned numeric(10,4) NOT NULL CHECK (quantity_planned > 0),
    quantity_produced numeric(10,4) NOT NULL DEFAULT 0 CHECK (quantity_produced >= 0),
    status text NOT NULL DEFAULT 'planned'
        CHECK (status IN ('planned', 'in_progress', 'completed', 'cancelled')),
    planned_start date,
    planned_end date,
    actual_start timestamptz,
    actual_end timestamptz,
    notes text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, order_number)
);

-- Append-only log of what was actually deducted from stock when a
-- production order started - same immutability principle
-- journal_entries/stock_movements/payments already establish: a material
-- issue is a historical fact, not something later edited in place.
CREATE TABLE production_order_material_issues (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    production_order_id uuid NOT NULL REFERENCES production_orders (id) ON DELETE CASCADE,
    product_id uuid NOT NULL REFERENCES products (id),
    quantity_issued numeric(10,4) NOT NULL CHECK (quantity_issued > 0),
    issued_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE production_order_sequences (
    firm_id uuid PRIMARY KEY REFERENCES firms (id) ON DELETE CASCADE,
    next_number integer NOT NULL DEFAULT 1
);

CREATE INDEX bom_headers_firm_id_idx ON bom_headers (firm_id);
CREATE INDEX bom_headers_product_id_idx ON bom_headers (firm_id, product_id);
CREATE INDEX bom_lines_bom_id_idx ON bom_lines (bom_id);
CREATE INDEX production_orders_firm_id_idx ON production_orders (firm_id);
CREATE INDEX production_orders_status_idx ON production_orders (firm_id, status);
CREATE INDEX production_orders_product_id_idx ON production_orders (product_id);
CREATE INDEX production_order_material_issues_order_id_idx ON production_order_material_issues (production_order_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON bom_headers, bom_lines TO zonaryos_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON production_orders, production_order_sequences TO zonaryos_app;
-- production_order_material_issues is append-only, same reasoning
-- payments/stock_movements/journal_lines already establish - no
-- UPDATE/DELETE grant.
GRANT SELECT, INSERT ON production_order_material_issues TO zonaryos_app;

ALTER TABLE bom_headers ENABLE ROW LEVEL SECURITY;
ALTER TABLE bom_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE production_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE production_order_material_issues ENABLE ROW LEVEL SECURITY;
ALTER TABLE production_order_sequences ENABLE ROW LEVEL SECURITY;

CREATE POLICY bom_headers_tenant_isolation ON bom_headers
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY bom_lines_tenant_isolation ON bom_lines
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY production_orders_tenant_isolation ON production_orders
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY production_order_material_issues_tenant_isolation ON production_order_material_issues
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY production_order_sequences_tenant_isolation ON production_order_sequences
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
