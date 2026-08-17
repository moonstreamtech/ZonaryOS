DROP POLICY cost_centers_tenant_isolation ON cost_centers;
DROP POLICY budget_lines_tenant_isolation ON budget_lines;
DROP POLICY budgets_tenant_isolation ON budgets;

ALTER TABLE journal_lines DROP COLUMN cost_center_id;

DROP TABLE cost_centers;
DROP TABLE budget_lines;
DROP TABLE budgets;
