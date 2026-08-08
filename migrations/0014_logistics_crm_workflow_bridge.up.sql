-- The workflow-to-logistics and workflow-to-CRM bridges
-- (internal/workflow/spec.go's TransitionSpec.Delivery/Customer): two more
-- OPTIONAL declarative templates a transition can carry, resolved against
-- one instance's payload at ExecuteTransition time
-- (internal/workflow/engine.go) and applied through
-- internal/logistics.CreateDeliveryTx / internal/crm.CreateCustomerTx -
-- the exact same shape 0009_financial_management_core.up.sql's
-- journal_template and 0012_inventory_workflow_bridge.up.sql's
-- stock_adjustment columns already established, not a new mechanism. NULL
-- (every transition defined before these columns existed) means "create
-- nothing", unchanged behavior - see TransitionSpec.Delivery/Customer's
-- own doc comments.
ALTER TABLE workflow_transitions ADD COLUMN delivery_template jsonb;
ALTER TABLE workflow_transitions ADD COLUMN customer_template jsonb;
