-- Sales orders (quote -> order -> fulfilled) and structured purchase
-- orders: both workflows (stock_to_sale's record_sale, purchase_order's
-- send) already exist as bare state machines - this batch adds the real
-- structured order record each one drives, the same "workflow instance
-- plus a real entity table" shape internal/invoicing already established
-- for stock_to_sale's record_sale -> draft invoice bridge.
--
-- Tenant model: every table is firm-scoped, mandatory RLS (Rule 3), same
-- convention as every other migration in this repo.
--
-- Separate tables, not one polymorphic orders table with a `type`
-- discriminator: sales_orders and purchase_orders reference different
-- counterparty tables (customers vs suppliers) and carry different,
-- already-fixed status vocabularies (this table's own
-- draft/confirmed/picking/shipped/delivered/cancelled vs
-- purchase_order's existing draft/sent/received/cancelled workflow
-- states) - a shared table would need both foreign keys nullable and a
-- CHECK enforcing "exactly one of customer_id/supplier_id is set",
-- trading one clean FK + CHECK per table for a weaker, doubly-nullable
-- one. See this batch's own report for the fuller design-choice writeup.
--
-- Scope boundary: no shipping carrier integration, no returns/refunds,
-- no multi-warehouse fulfillment routing.

CREATE TABLE sales_orders (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    -- Sequential per firm (SO-0001, SO-0002, ...) - see
    -- sales_order_sequences below, the same atomic-UPSERT-counter shape
    -- invoice_sequences already established for invoices.
    order_number text NOT NULL,
    customer_id uuid REFERENCES customers (id),
    status text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'confirmed', 'picking', 'shipped', 'delivered', 'cancelled')),
    shipping_address text,
    notes text,
    currency text NOT NULL DEFAULT 'TRY',
    subtotal numeric(19,4) NOT NULL DEFAULT 0 CHECK (subtotal >= 0),
    tax_amount numeric(19,4) NOT NULL DEFAULT 0 CHECK (tax_amount >= 0),
    total numeric(19,4) NOT NULL DEFAULT 0 CHECK (total >= 0),
    -- Set only when this order was auto-created by a transition's
    -- SalesOrderTemplate (stock_to_sale's record_sale, this batch) - NULL
    -- for an order created directly via the HTTP API, same
    -- "distinguishes bridge-created from manually-created" role
    -- invoices.source_workflow_instance already plays.
    source_workflow_instance uuid REFERENCES workflow_instances (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, order_number)
);

CREATE TABLE sales_order_lines (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id uuid NOT NULL REFERENCES sales_orders (id) ON DELETE CASCADE,
    product_id uuid REFERENCES products (id),
    description text NOT NULL,
    quantity numeric(19,4) NOT NULL CHECK (quantity > 0),
    unit_price numeric(19,4) NOT NULL CHECK (unit_price > 0),
    tax_rate numeric(5,4) NOT NULL DEFAULT 0 CHECK (tax_rate >= 0),
    line_total numeric(19,4) NOT NULL CHECK (line_total >= 0)
);

CREATE TABLE sales_order_sequences (
    firm_id uuid PRIMARY KEY REFERENCES firms (id) ON DELETE CASCADE,
    next_number integer NOT NULL DEFAULT 1
);

-- Purchase-facing counterpart, structurally parallel to sales_orders
-- (see this file's own doc comment for why it's a separate table) -
-- auto-created by purchase_order's own `send` transition via a
-- PurchaseOrderTemplate effect, the same bridge shape SalesOrderTemplate
-- uses for stock_to_sale's record_sale.
CREATE TABLE purchase_orders (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    order_number text NOT NULL,
    supplier_id uuid REFERENCES suppliers (id),
    status text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'sent', 'received', 'cancelled')),
    notes text,
    currency text NOT NULL DEFAULT 'TRY',
    subtotal numeric(19,4) NOT NULL DEFAULT 0 CHECK (subtotal >= 0),
    tax_amount numeric(19,4) NOT NULL DEFAULT 0 CHECK (tax_amount >= 0),
    total numeric(19,4) NOT NULL DEFAULT 0 CHECK (total >= 0),
    source_workflow_instance uuid REFERENCES workflow_instances (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, order_number)
);

CREATE TABLE purchase_order_lines (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id uuid NOT NULL REFERENCES purchase_orders (id) ON DELETE CASCADE,
    product_id uuid REFERENCES products (id),
    description text NOT NULL,
    quantity numeric(19,4) NOT NULL CHECK (quantity > 0),
    unit_price numeric(19,4) NOT NULL CHECK (unit_price > 0),
    tax_rate numeric(5,4) NOT NULL DEFAULT 0 CHECK (tax_rate >= 0),
    line_total numeric(19,4) NOT NULL CHECK (line_total >= 0)
);

CREATE TABLE purchase_order_sequences (
    firm_id uuid PRIMARY KEY REFERENCES firms (id) ON DELETE CASCADE,
    next_number integer NOT NULL DEFAULT 1
);

CREATE INDEX sales_orders_firm_id_idx ON sales_orders (firm_id);
CREATE INDEX sales_orders_status_idx ON sales_orders (firm_id, status);
CREATE INDEX sales_orders_customer_id_idx ON sales_orders (customer_id);
CREATE INDEX sales_orders_created_at_idx ON sales_orders (firm_id, created_at);
CREATE INDEX sales_order_lines_order_id_idx ON sales_order_lines (order_id);

CREATE INDEX purchase_orders_firm_id_idx ON purchase_orders (firm_id);
CREATE INDEX purchase_orders_status_idx ON purchase_orders (firm_id, status);
CREATE INDEX purchase_orders_supplier_id_idx ON purchase_orders (supplier_id);
CREATE INDEX purchase_order_lines_order_id_idx ON purchase_order_lines (order_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON sales_orders, sales_order_lines, sales_order_sequences TO zonaryos_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON purchase_orders, purchase_order_lines, purchase_order_sequences TO zonaryos_app;

ALTER TABLE sales_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_order_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_order_sequences ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_order_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_order_sequences ENABLE ROW LEVEL SECURITY;

CREATE POLICY sales_orders_tenant_isolation ON sales_orders
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

-- sales_order_lines has no firm_id column of its own (a strict child of
-- sales_orders, the same shape invoice_lines is to invoices) - its RLS
-- policy joins back to sales_orders to scope by tenant.
CREATE POLICY sales_order_lines_tenant_isolation ON sales_order_lines
    USING (EXISTS (SELECT 1 FROM sales_orders WHERE sales_orders.id = sales_order_lines.order_id AND sales_orders.firm_id = app_current_firm_id()))
    WITH CHECK (EXISTS (SELECT 1 FROM sales_orders WHERE sales_orders.id = sales_order_lines.order_id AND sales_orders.firm_id = app_current_firm_id()));

CREATE POLICY sales_order_sequences_tenant_isolation ON sales_order_sequences
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY purchase_orders_tenant_isolation ON purchase_orders
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY purchase_order_lines_tenant_isolation ON purchase_order_lines
    USING (EXISTS (SELECT 1 FROM purchase_orders WHERE purchase_orders.id = purchase_order_lines.order_id AND purchase_orders.firm_id = app_current_firm_id()))
    WITH CHECK (EXISTS (SELECT 1 FROM purchase_orders WHERE purchase_orders.id = purchase_order_lines.order_id AND purchase_orders.firm_id = app_current_firm_id()));

CREATE POLICY purchase_order_sequences_tenant_isolation ON purchase_order_sequences
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
