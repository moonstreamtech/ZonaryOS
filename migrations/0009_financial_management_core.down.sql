ALTER TABLE workflow_transitions DROP COLUMN journal_template;
DROP POLICY journal_lines_tenant_isolation ON journal_lines;
DROP POLICY journal_entries_tenant_isolation ON journal_entries;
DROP POLICY accounts_tenant_isolation ON accounts;
DROP TABLE journal_lines;
DROP TABLE journal_entries;
DROP TABLE accounts;
