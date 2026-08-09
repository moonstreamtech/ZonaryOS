-- Performance indexes migration (Part 2 of the multi-tenant isolation
-- hardening + performance batch): additive only, no data changes -
-- CREATE INDEX IF NOT EXISTS throughout, not CONCURRENTLY, since there is
-- still no multi-instance deployment target (docs/OPEN_POINTS.md item 34)
-- and this codebase's own dev/test scale doesn't need an online index
-- build. See docs/DEVELOPMENT.md's "Performance: N+1 query audit and
-- indexes" section for the full audit this migration is based on -
-- several of the tables this batch's design brief named already had a
-- covering index from an earlier migration (journal_lines(entry_id),
-- invoice_lines(invoice_id), notifications' own partial unread index,
-- scheduled_rule_runs' own partial pending-due index); those are NOT
-- repeated here.

-- workflow_instances: ListInstances (internal/workflow) filters by
-- workflow_definition_id alone today (workflow_definition_id already
-- implies firm, since a definition belongs to exactly one firm) and
-- relies on RLS for firm_id - workflow_instances_workflow_definition_id_idx
-- (migrations/0003) already serves that query well on its own. This
-- composite is added anyway per the design brief's own explicit call
-- ("list instances by workflow is the most common read pattern"): it
-- lets the planner satisfy both the RLS policy's firm_id check and the
-- definition filter from a single index instead of an index scan plus a
-- row-level recheck, and costs nothing extra to maintain given the
-- table's own write pattern (append-mostly, no high-churn column here).
CREATE INDEX IF NOT EXISTS workflow_instances_firm_definition_idx
    ON workflow_instances (firm_id, workflow_definition_id);

-- invoices.source_workflow_instance had NO index at all before this
-- migration (unlike customers.source_workflow_instance, which has had
-- one since migrations/0013) - a real gap, not a duplicate: Part 3a's
-- new GET /workflow-instances/{id}/invoices endpoint (and the invoice
-- detail page's own new "Source" link, the reverse direction) both filter
-- invoices by this column directly.
CREATE INDEX IF NOT EXISTS invoices_source_workflow_instance_idx
    ON invoices (source_workflow_instance);

-- edge_events: internal/edgeagent.ListEvents filters `WHERE agent_id = $1
-- AND firm_id = $2 ORDER BY received_at DESC` - edge_events_agent_id_idx
-- (migrations/0018, already (agent_id, received_at)) already serves this
-- reasonably well since agent_id alone is highly selective, but a
-- firm_id-first composite matches this time-series table's actual
-- multi-tenant access pattern (every read is firm-scoped by RLS first)
-- more directly, per the design brief's own explicit call.
CREATE INDEX IF NOT EXISTS edge_events_firm_agent_received_idx
    ON edge_events (firm_id, agent_id, received_at);

-- stock_movements: internal/inventory.ListStockMovements filters
-- `WHERE ($1::uuid IS NULL OR product_id = $1) ORDER BY created_at DESC`
-- (firm_id via RLS) - no existing index covers firm_id + product_id +
-- created_at together (only three separate single/two-column indexes
-- from migrations/0011: firm_id, product_id, and (source_type,
-- source_id)), so both the firm-wide movement history view and the
-- per-product movement history view were doing more work than necessary.
CREATE INDEX IF NOT EXISTS stock_movements_firm_product_created_idx
    ON stock_movements (firm_id, product_id, created_at);
