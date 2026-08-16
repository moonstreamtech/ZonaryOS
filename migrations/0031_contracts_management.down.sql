ALTER TABLE document_templates DROP CONSTRAINT document_templates_type_check;
ALTER TABLE document_templates ADD CONSTRAINT document_templates_type_check
    CHECK (type IN ('invoice', 'delivery_note', 'report'));

DROP POLICY contract_documents_tenant_isolation ON contract_documents;
DROP POLICY contract_registry_tenant_isolation ON contract_registry;

DROP TABLE contract_documents;
DROP TABLE contract_registry;
