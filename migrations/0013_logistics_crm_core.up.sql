-- Logistics, delivery management, and customer-facing data: the
-- structural layer under the existing stock_to_sale/customer_pipeline
-- workflows - real delivery records (not just a state on a workflow
-- instance) and real customer records behind the customer_pipeline's
-- "convert" transition (previously just a state change with no entity
-- behind it).
--
-- Tenant model: both tables are firm-scoped, mandatory RLS (Rule 3), same
-- convention as every other migration in this repo.
--
-- Scope boundary: no route optimization or map integration, no carrier
-- API integration, no invoicing (a future batch combining customers +
-- journal entries into a formatted document), no loyalty points or
-- customer pricing tiers - see this batch's own scope-boundary note.

CREATE TABLE deliveries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    reference text,
    origin_address text,
    destination_address text,
    carrier text,
    tracking_number text,
    -- app-validated (a DB CHECK, unlike accounts.type/stock_movements.reason's
    -- open text vocabulary) - a delivery's lifecycle is a fixed, small set
    -- of states this batch itself defines, not an open-ended per-firm
    -- vocabulary the way a stock movement's "reason" is.
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'in_transit', 'delivered', 'returned', 'cancelled')),
    estimated_date date,
    actual_date date,
    -- source_type/source_id record where this delivery originated (e.g.
    -- "workflow_instance" + a workflow_instances.id) - same "not a
    -- foreign key, since source_type varies, validated by the writer not
    -- the schema" pattern audit_log/journal_entries/stock_movements
    -- already use.
    source_type text,
    source_id uuid,
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- A real customer record - previously the customer_pipeline workflow's
-- "customer" state was just a state, with no entity behind it.
-- source_workflow_instance (nullable) distinguishes a customer the CRM
-- pipeline itself converted (the "active_customers" KPI's own condition -
-- see internal/reports/kpi.go) from one an owner created directly via the
-- HTTP API with no lead behind it.
CREATE TABLE customers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    email text,
    phone text,
    address text,
    tax_id text,
    credit_limit numeric(19,4),
    currency text NOT NULL DEFAULT 'TRY',
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    source_workflow_instance uuid REFERENCES workflow_instances (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX deliveries_firm_id_idx ON deliveries (firm_id);
CREATE INDEX deliveries_status_idx ON deliveries (firm_id, status);
CREATE INDEX deliveries_source_idx ON deliveries (source_type, source_id);
CREATE INDEX customers_firm_id_idx ON customers (firm_id);
CREATE INDEX customers_source_workflow_instance_idx ON customers (source_workflow_instance);

GRANT SELECT, INSERT, UPDATE, DELETE ON deliveries, customers TO zonaryos_app;

ALTER TABLE deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE customers ENABLE ROW LEVEL SECURITY;

CREATE POLICY deliveries_tenant_isolation ON deliveries
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY customers_tenant_isolation ON customers
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
