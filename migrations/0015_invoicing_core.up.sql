-- Invoicing, payment tracking, and receivables management: the
-- accounting, customer, and sales modules now have enough data
-- (journal_entries, customers, products) to generate real invoices
-- against a sale - this batch connects them.
--
-- Tenant model: every table is firm-scoped, mandatory RLS (Rule 3), same
-- convention as every other migration in this repo.
--
-- Scope boundary: no PDF generation (invoices as structured data first),
-- no recurring invoices, no credit notes/refunds, no multi-currency
-- invoice settlement, no customer payment portal - see this batch's own
-- scope-boundary note.

CREATE TABLE invoices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    -- Sequential per firm (INV-0001, INV-0002, ...) - see invoice_sequences
    -- below for how a collision-free number is actually allocated. Not
    -- globally unique on its own (two firms can both have "INV-0001"), so
    -- the uniqueness constraint below is scoped to (firm_id, invoice_number).
    invoice_number text NOT NULL,
    customer_id uuid REFERENCES customers (id),
    issued_date date NOT NULL,
    due_date date,
    -- app-validated (a DB CHECK, same "fixed, small, batch-defined set"
    -- pattern deliveries.status already uses) - not an open text
    -- vocabulary the way accounts.type/stock_movements.reason are.
    status text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'sent', 'paid', 'overdue', 'cancelled')),
    subtotal numeric(19,4) NOT NULL DEFAULT 0 CHECK (subtotal >= 0),
    tax_amount numeric(19,4) NOT NULL DEFAULT 0 CHECK (tax_amount >= 0),
    total numeric(19,4) NOT NULL DEFAULT 0 CHECK (total >= 0),
    currency text NOT NULL DEFAULT 'TRY',
    notes text,
    -- Set only when the invoice was auto-created by a transition's
    -- InvoiceTemplate (see internal/workflow's workflow-to-invoicing
    -- bridge) - NULL for an invoice created directly via the HTTP API,
    -- the same "distinguishes bridge-created from manually-created" role
    -- customers.source_workflow_instance already plays.
    source_workflow_instance uuid REFERENCES workflow_instances (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, invoice_number)
);

-- quantity/unit_price > 0 and tax_rate >= 0 are real DB CHECKs, not just
-- application-layer validation - the same "the CHECK is the actual
-- authoritative guarantee, RLS-adjacent defense in depth" reasoning
-- journal_lines.amount's own CHECK (amount > 0) already establishes.
CREATE TABLE invoice_lines (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id uuid NOT NULL REFERENCES invoices (id) ON DELETE CASCADE,
    description text NOT NULL,
    quantity numeric(19,4) NOT NULL CHECK (quantity > 0),
    unit_price numeric(19,4) NOT NULL CHECK (unit_price > 0),
    tax_rate numeric(5,2) NOT NULL DEFAULT 0 CHECK (tax_rate >= 0),
    line_total numeric(19,4) NOT NULL CHECK (line_total >= 0),
    product_id uuid REFERENCES products (id)
);

-- Real, recorded payments against an invoice - the closing half of the
-- receivables cycle (see internal/invoicing.RecordPayment: a payment that
-- brings total paid to >= invoices.total auto-marks the invoice 'paid'
-- and posts a real DR Cash / CR Trade Receivables journal entry in the
-- same transaction).
CREATE TABLE payments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    invoice_id uuid REFERENCES invoices (id),
    amount numeric(19,4) NOT NULL CHECK (amount > 0),
    currency text NOT NULL DEFAULT 'TRY',
    paid_at timestamptz NOT NULL,
    -- app-validated open vocabulary (like stock_movements.reason), not a
    -- DB CHECK - unlike invoice/delivery status, "how a customer actually
    -- paid" is exactly the kind of firm/region-specific detail a fixed
    -- CHECK would be wrong to constrain (docs/OPEN_POINTS.md item 24's own
    -- "not fiscal-format validation" reasoning).
    method text,
    reference text,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Per-firm invoice-numbering counter: a dedicated one-row-per-firm table,
-- not a Postgres SEQUENCE object (which is a single, database-wide
-- counter with no natural per-tenant reset point) and not
-- `count(*) + 1` against invoices itself (racy under concurrent inserts,
-- and wrong after any invoice is ever deleted). See
-- internal/invoicing.nextInvoiceNumberTx's own doc comment for the exact
-- atomic UPSERT this table backs, and this batch's report for why it's
-- collision-free under real concurrency.
CREATE TABLE invoice_sequences (
    firm_id uuid PRIMARY KEY REFERENCES firms (id) ON DELETE CASCADE,
    next_number integer NOT NULL DEFAULT 1
);

CREATE INDEX invoices_firm_id_idx ON invoices (firm_id);
CREATE INDEX invoices_status_idx ON invoices (firm_id, status);
CREATE INDEX invoices_customer_id_idx ON invoices (customer_id);
CREATE INDEX invoice_lines_invoice_id_idx ON invoice_lines (invoice_id);
CREATE INDEX payments_firm_id_idx ON payments (firm_id);
CREATE INDEX payments_invoice_id_idx ON payments (invoice_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON invoices, invoice_lines, invoice_sequences TO zonaryos_app;
-- payments is append-only (a recorded payment is a historical fact,
-- corrected by a new/reversing payment - not rewritten), the same
-- immutability principle journal_entries/journal_lines and
-- stock_movements already establish - no UPDATE/DELETE grant.
GRANT SELECT, INSERT ON payments TO zonaryos_app;

ALTER TABLE invoices ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_sequences ENABLE ROW LEVEL SECURITY;

CREATE POLICY invoices_tenant_isolation ON invoices
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

-- invoice_lines has no firm_id column of its own (it's a strict child of
-- invoices, the same shape journal_lines is to journal_entries) - its RLS
-- policy joins back to invoices to scope by tenant, mirroring
-- journal_lines' own tenant-isolation policy in
-- migrations/0009_financial_management_core.up.sql.
CREATE POLICY invoice_lines_tenant_isolation ON invoice_lines
    USING (EXISTS (
        SELECT 1 FROM invoices WHERE invoices.id = invoice_lines.invoice_id
            AND invoices.firm_id = app_current_firm_id()
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM invoices WHERE invoices.id = invoice_lines.invoice_id
            AND invoices.firm_id = app_current_firm_id()
    ));

CREATE POLICY payments_tenant_isolation ON payments
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY invoice_sequences_tenant_isolation ON invoice_sequences
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
