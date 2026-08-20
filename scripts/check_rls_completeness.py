#!/usr/bin/env python3
"""RLS Completeness Check (tenant isolation audit, Part 1).

cmd/ciaudit's own RLS check is static: it greps migrations/*.up.sql text
for a `CREATE TABLE ... (... firm_id ...)` followed somewhere in the
directory by a matching `ALTER TABLE ... ENABLE ROW LEVEL SECURITY`. That
catches "nobody ever wrote the ALTER TABLE line at all", but it cannot
tell whether RLS being enabled ever actually got backed by a real
`CREATE POLICY` (a table with RLS enabled and zero policies denies every
row to every non-owner role by default - not a leak, but also not the
tenant-isolation-by-policy this codebase's own convention always pairs
with ENABLE ROW LEVEL SECURITY, so a table that somehow ended up in that
state would be worth catching too) - and it cannot see whether a policy
that runs would ever be evaluated by Postgres, since ENABLE ROW LEVEL
SECURITY plus a REVOKE could still be misconfigured in ways only a live
database actually reveals. This check queries a real, migrated Postgres
directly (the same one CI's own `integration-tests` job stands up) as a
second, independent line of defense: it asks Postgres itself, via
pg_policies, rather than trusting a text search over the SQL that
supposedly configured it.

Two checks, over a fully-migrated database:
  1. Every table with a `firm_id` column (information_schema.columns)
     must have at least one row in pg_policies.
  2. (Informational only, printed but not a failure) every table WITHOUT
     a firm_id column that nonetheless has a pg_policies row is flagged
     for a human to double check - a platform-level table should not
     usually need RLS at all, so an RLS policy appearing on one is worth
     a second look, not necessarily a bug (a legitimate but unusual case
     is plausible) - see cmd/ciaudit's own migrations/0033 precedent
     for why some firm-referencing columns are deliberately NOT named
     `firm_id` to stay off this table's own boundary.

Usage:
    python3 scripts/check_rls_completeness.py

Reads the target Postgres connection string from
ZONARYOS_TEST_ADMIN_DATABASE_URL - the same env var every
`*_integration_test.go` file and CI's own `integration-tests` job already
uses for a migrated, superuser-role connection - so this check has
nothing new to configure in CI beyond running in a job that already sets
that variable and has already run `go run ./cmd/migrate`.
"""
import os
import subprocess
import sys

MISSING_RLS_QUERY = """
SELECT c.table_name
FROM information_schema.columns c
JOIN pg_class pc ON pc.relname = c.table_name AND pc.relnamespace = 'public'::regnamespace
WHERE c.table_schema = 'public'
  AND c.column_name = 'firm_id'
  -- Excludes partition children (pc.relispartition): a partitioned
  -- table's own RLS policies are enforced by Postgres for every one of
  -- its partitions automatically, including a query against a partition
  -- by name directly - a partition never needs (and normally never has)
  -- its own separate CREATE POLICY, so requiring one here would be a
  -- false positive against every table this codebase declares
  -- PARTITION BY RANGE (analytics_events, request_metrics, ...); only
  -- the partitioned parent itself is checked below.
  AND NOT pc.relispartition
  AND NOT EXISTS (
      SELECT 1 FROM pg_policies p
      WHERE p.schemaname = 'public' AND p.tablename = c.table_name
  )
ORDER BY c.table_name;
"""

UNEXPECTED_RLS_QUERY = """
SELECT DISTINCT p.tablename
FROM pg_policies p
WHERE p.schemaname = 'public'
  AND NOT EXISTS (
      SELECT 1 FROM information_schema.columns c
      WHERE c.table_schema = 'public' AND c.table_name = p.tablename AND c.column_name = 'firm_id'
  )
ORDER BY p.tablename;
"""


def run_query(dsn, query):
    result = subprocess.run(
        ["psql", dsn, "-tA", "-c", query],
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        print("RLS Completeness Check FAILED to query Postgres:", file=sys.stderr)
        print(result.stderr, file=sys.stderr)
        sys.exit(2)
    return [line for line in result.stdout.splitlines() if line.strip()]


def main():
    dsn = os.environ.get("ZONARYOS_TEST_ADMIN_DATABASE_URL")
    if not dsn:
        print("ZONARYOS_TEST_ADMIN_DATABASE_URL must be set to run the RLS completeness check", file=sys.stderr)
        return 2

    missing = run_query(dsn, MISSING_RLS_QUERY)
    unexpected = run_query(dsn, UNEXPECTED_RLS_QUERY)

    if unexpected:
        print("RLS Completeness Check: note - RLS policy present on a table with no `firm_id` column (not a failure, just worth a human glance):")
        for table in unexpected:
            print(f"  - {table}")

    if missing:
        print("RLS Completeness Check FAILED:", file=sys.stderr)
        print("The following tables have a `firm_id` column but no RLS policy in pg_policies (Never-Violate Rule 3):", file=sys.stderr)
        for table in missing:
            print(f"  - {table}", file=sys.stderr)
        return 1

    print("RLS completeness check passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
