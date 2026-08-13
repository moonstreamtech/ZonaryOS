-- Reverse dependency order (absences/time_entries/payroll_entries all
-- reference payroll_periods/people/workflow_instances, but none of THOSE
-- reference tables are dropped here, so plain drops in child-first order
-- are sufficient - DROP TABLE cascades each table's own policy, no
-- separate DROP POLICY needed).
DROP TABLE IF EXISTS absences;
DROP TABLE IF EXISTS time_entries;
DROP TABLE IF EXISTS payroll_entries;
DROP TABLE IF EXISTS payroll_periods;
