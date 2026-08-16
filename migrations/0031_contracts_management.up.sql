-- Contracts management, document workflows, and legal foundation: every
-- business manages contracts with suppliers, customers, and employees.
-- internal/hr's own `contracts` table is an employment/compensation-term
-- record scoped to one `people` row - not a general-purpose legal
-- contract. This adds a separate registry for that: customer/supplier/
-- employment/NDA/service contracts, with attached documents and expiry
-- tracking. Not a full legal suite: no e-signature integration (Open
-- Points item 17), no version control for contract text (a URL to an
-- external document, not stored content), no new approval mechanism (the
-- existing pending_approvals is workflow-instance-backed and not wired
-- here - see docs/DEVELOPMENT.md's own reasoning for this batch).
--
-- Tenant model: every table is firm-scoped, mandatory RLS (Rule 3), same
-- convention as every other migration in this repo.

CREATE TABLE contract_registry (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    reference_number text NOT NULL,
    title text NOT NULL,
    type text NOT NULL CHECK (type IN ('customer', 'supplier', 'employment', 'nda', 'service', 'other')),
    counterparty_name text NOT NULL,
    counterparty_id uuid,
    counterparty_type text CHECK (counterparty_type IN ('customer', 'supplier', 'person', 'other')),
    status text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'review', 'active', 'expired', 'terminated', 'renewed')),
    value numeric(19,4),
    currency text NOT NULL DEFAULT 'TRY',
    start_date date,
    end_date date,
    auto_renewal boolean NOT NULL DEFAULT false,
    renewal_notice_days integer DEFAULT 30,
    signed_at timestamptz,
    signed_by uuid REFERENCES users (id),
    notes text,
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, reference_number)
);

CREATE TABLE contract_documents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    contract_id uuid NOT NULL REFERENCES contract_registry (id) ON DELETE CASCADE,
    name text NOT NULL,
    document_url text,
    type text NOT NULL CHECK (type IN ('original', 'amendment', 'annex', 'correspondence')),
    uploaded_at timestamptz NOT NULL DEFAULT now(),
    uploaded_by uuid NOT NULL REFERENCES users (id)
);

CREATE INDEX contract_registry_firm_id_idx ON contract_registry (firm_id);
CREATE INDEX contract_registry_status_idx ON contract_registry (firm_id, status);
-- The expiry scheduler's own due-check query filters status='active' AND
-- auto_renewal=false first (a partial index, not a plain btree on
-- end_date alone) since expired/terminated/draft contracts and
-- auto-renewing ones should never surface as "expiring soon" regardless
-- of their end_date.
CREATE INDEX contract_registry_expiry_idx ON contract_registry (end_date) WHERE status = 'active' AND NOT auto_renewal;
CREATE INDEX contract_documents_firm_id_idx ON contract_documents (firm_id);
CREATE INDEX contract_documents_contract_id_idx ON contract_documents (contract_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON contract_registry, contract_documents TO zonaryos_app;

ALTER TABLE contract_registry ENABLE ROW LEVEL SECURITY;
ALTER TABLE contract_documents ENABLE ROW LEVEL SECURITY;

CREATE POLICY contract_registry_tenant_isolation ON contract_registry
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY contract_documents_tenant_isolation ON contract_documents
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

-- document_templates.type's CHECK constraint isn't additive - it has to
-- be dropped and recreated to extend its vocabulary with 'contract'
-- (internal/documents.RenderContract, this batch).
ALTER TABLE document_templates DROP CONSTRAINT document_templates_type_check;
ALTER TABLE document_templates ADD CONSTRAINT document_templates_type_check
    CHECK (type IN ('invoice', 'delivery_note', 'report', 'contract'));
