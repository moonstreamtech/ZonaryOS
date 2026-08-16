DROP POLICY projects_tenant_isolation ON projects;
DROP POLICY crm_opportunities_tenant_isolation ON crm_opportunities;
DROP POLICY crm_interactions_tenant_isolation ON crm_interactions;

DROP TABLE projects;
ALTER TABLE crm_interactions DROP CONSTRAINT crm_interactions_opportunity_id_fkey;
DROP TABLE crm_opportunities;
DROP TABLE crm_interactions;
