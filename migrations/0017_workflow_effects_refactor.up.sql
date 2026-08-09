-- The Effects refactor: workflow_transitions carried five separate,
-- independently-added jsonb columns for its optional side-effect
-- "bridge" templates - journal_template (0009_financial_management_core),
-- stock_adjustment (0012_inventory_workflow_bridge), delivery_template/
-- customer_template (0014_logistics_crm_workflow_bridge), and
-- invoice_template (0016_invoicing_workflow_bridge). The fifth of those
-- (invoice_template) crossed a threshold internal/workflow/spec.go's own
-- TransitionSpec doc comment had documented in advance: "a fifth or
-- sixth such bridge... is the signal to switch to the generic Effects
-- shape." This migration is that switch, on the storage side: the five
-- columns collapse into one, `effects jsonb NOT NULL DEFAULT '[]'::jsonb`
-- - an array of `{"kind": "journal"|"stock"|"delivery"|"customer"|
-- "invoice", "payload": <the exact same template object the old column
-- held>}` entries. See internal/workflow/spec.go's TransitionSpec doc
-- comment for the corresponding Go-side shape
-- (TransitionEffect/EffectKind), and this batch's own report for the
-- full backward-compatibility proof (the HTTP API's request/response
-- JSON shape for each bridge kind is unchanged by this refactor - only
-- this internal storage shape and TransitionSpec's own Go shape changed).
--
-- Existing rows are transformed in place, not dropped and recreated: any
-- workflow_transitions row that already had one or more of the five old
-- columns set gets exactly the same effects, now living in one jsonb
-- array instead of five columns - no side-effect data is lost. A row
-- with none of the five old columns set (the common case - most
-- transitions carry no bridge at all) gets effects = '[]'::jsonb, the
-- same "no effects" meaning a transition with all five old columns NULL
-- already had.

ALTER TABLE workflow_transitions ADD COLUMN effects jsonb NOT NULL DEFAULT '[]'::jsonb;

UPDATE workflow_transitions wt SET effects = COALESCE(agg.effects, '[]'::jsonb)
FROM (
    SELECT id, jsonb_agg(effect) AS effects
    FROM (
        SELECT id, jsonb_build_object('kind', 'journal', 'payload', journal_template) AS effect
            FROM workflow_transitions WHERE journal_template IS NOT NULL
        UNION ALL
        SELECT id, jsonb_build_object('kind', 'stock', 'payload', stock_adjustment)
            FROM workflow_transitions WHERE stock_adjustment IS NOT NULL
        UNION ALL
        SELECT id, jsonb_build_object('kind', 'delivery', 'payload', delivery_template)
            FROM workflow_transitions WHERE delivery_template IS NOT NULL
        UNION ALL
        SELECT id, jsonb_build_object('kind', 'customer', 'payload', customer_template)
            FROM workflow_transitions WHERE customer_template IS NOT NULL
        UNION ALL
        SELECT id, jsonb_build_object('kind', 'invoice', 'payload', invoice_template)
            FROM workflow_transitions WHERE invoice_template IS NOT NULL
    ) raw_effects
    GROUP BY id
) agg
WHERE wt.id = agg.id;

-- migration-safety:acknowledge: this system has no decided deployment
-- target/rolling-deploy infrastructure yet (docs/OPEN_POINTS.md item 34 -
-- Canary/Rollback Trigger is explicitly "Not Set Up"), so there is no
-- live primary/replica window today where an old binary could run
-- against this new schema mid-rollout. The application code reading/
-- writing these five columns is removed in this same batch (see
-- internal/workflow/engine.go's marshalEffects/unmarshalEffects and
-- spec.go's TransitionEffect), so once this migration and its
-- corresponding code both ship together - the only way this repository
-- is deployed today - there is no code path left that expects these
-- columns to exist. When a real rolling-deploy target is decided, this
-- kind of same-batch column drop will need to become a deprecate-then-
-- drop migration pair instead.
ALTER TABLE workflow_transitions DROP COLUMN journal_template;
ALTER TABLE workflow_transitions DROP COLUMN stock_adjustment;
ALTER TABLE workflow_transitions DROP COLUMN delivery_template;
ALTER TABLE workflow_transitions DROP COLUMN customer_template;
ALTER TABLE workflow_transitions DROP COLUMN invoice_template;
