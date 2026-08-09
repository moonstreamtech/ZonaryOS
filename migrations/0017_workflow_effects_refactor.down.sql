ALTER TABLE workflow_transitions ADD COLUMN journal_template jsonb;
ALTER TABLE workflow_transitions ADD COLUMN stock_adjustment jsonb;
ALTER TABLE workflow_transitions ADD COLUMN delivery_template jsonb;
ALTER TABLE workflow_transitions ADD COLUMN customer_template jsonb;
ALTER TABLE workflow_transitions ADD COLUMN invoice_template jsonb;

UPDATE workflow_transitions SET
    journal_template = (
        SELECT effect -> 'payload' FROM jsonb_array_elements(effects) effect WHERE effect ->> 'kind' = 'journal' LIMIT 1
    ),
    stock_adjustment = (
        SELECT effect -> 'payload' FROM jsonb_array_elements(effects) effect WHERE effect ->> 'kind' = 'stock' LIMIT 1
    ),
    delivery_template = (
        SELECT effect -> 'payload' FROM jsonb_array_elements(effects) effect WHERE effect ->> 'kind' = 'delivery' LIMIT 1
    ),
    customer_template = (
        SELECT effect -> 'payload' FROM jsonb_array_elements(effects) effect WHERE effect ->> 'kind' = 'customer' LIMIT 1
    ),
    invoice_template = (
        SELECT effect -> 'payload' FROM jsonb_array_elements(effects) effect WHERE effect ->> 'kind' = 'invoice' LIMIT 1
    )
WHERE jsonb_array_length(effects) > 0;

ALTER TABLE workflow_transitions DROP COLUMN effects;
