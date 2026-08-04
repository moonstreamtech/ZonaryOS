// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// The well-known workflow key seeded for every firm by the firm-creation
// wizard (internal/wizard.CreateDefaultFirm -> workflow.SeedStockToSaleWorkflowTx)
// - see internal/workflow/stock_to_sale.go's StockToSaleKey constant.
// Shared here (rather than redeclared per page) so the dashboard's item
// count and the stock page's listing can't drift apart.
export const STOCK_TO_SALE_KEY = "stock_to_sale";

export type StateInfo = {
  key: string;
  name: string;
};

// FieldType is spec.go's FieldType wire value. Open Points item 35
// resolved the original four scalar types; item 38 extends it with
// "enum"/"reference"/"array" - mirrored 1:1 on the frontend, same
// convention as the original four.
export type FieldType = "string" | "number" | "boolean" | "date" | "enum" | "reference" | "array";

// ScalarFieldType is the subset of FieldType a FieldSpecInput's
// arrayItemType may be (see FieldSpecInput.arrayItemType below) - the
// original four types, deliberately excluding "enum"/"reference"/"array"
// itself (no nested arrays, no arrays of enums/references - matching
// internal/workflow/spec.go's validateFieldShape).
export type ScalarFieldType = "string" | "number" | "boolean" | "date";

// FieldSpecInput is internal/workflow.FieldSpec's wire shape (see
// handlers.go's fieldSpecResponse/defineFieldRequest) - one declared
// payload field on a workflow definition's OPTIONAL payload schema.
// Shared between DefinitionSpecInput (the builder's request body) and
// WorkflowDefinition (a definition read back from the backend), the same
// way PermissionSpecInput already is.
export type FieldSpecInput = {
  name: string;
  type: FieldType;
  required: boolean;
  // options is only meaningful when type is "enum" - the fixed set of
  // string values this field accepts (Open Points item 38).
  options?: string[];
  // referenceDefinitionKey is only meaningful when type is "reference" -
  // the workflow_definitions.key (e.g. "stock_to_sale") this field's
  // value must identify an instance of, in the same firm (item 38).
  referenceDefinitionKey?: string;
  // arrayItemType is only meaningful when type is "array" - which scalar
  // type each array element must satisfy (item 38).
  arrayItemType?: ScalarFieldType;
};

export type AvailableAction = {
  actionKey: string;
  name: string;
  toState: StateInfo;
  permissionKey: string;
};

export type InstanceState = {
  instanceId: string;
  workflowDefinitionId: string;
  state: StateInfo;
  payload: Record<string, unknown>;
  availableActions: AvailableAction[];
};

export type ListInstancesPage = {
  instances: InstanceState[];
  total: number;
};

export type WorkflowDefinition = {
  definitionId: string;
  key: string;
  name: string;
  createPermissionKey: string;
  // Fields is OPTIONAL (Open Points item 35) - undefined/empty for a
  // schema-less definition (stock_to_sale/customer_pipeline today, and
  // every definition that existed before this field was added), matching
  // the backend's own `fields,omitempty` on definitionInfoResponse.
  // CreateInstanceForm branches on this: present -> typed fields, absent
  // -> today's free-form key/value editor, unchanged.
  fields?: FieldSpecInput[];
};

// The request body shape for POST .../workflow-definitions - a 1:1 wire
// mirror of the Go backend's DefinitionSpec (internal/workflow/spec.go),
// itself mirrored again by defineWorkflowRequest in handlers.go. See
// docs/api/openapi.yaml's DefinitionSpec schema.
export type PermissionSpecInput = {
  key: string;
  description: string;
};

export type StateSpecInput = {
  key: string;
  name: string;
  isInitial: boolean;
  isTerminal: boolean;
};

export type TransitionSpecInput = {
  fromStateKey: string;
  toStateKey: string;
  actionKey: string;
  name: string;
  permission: PermissionSpecInput;
};

export type DefinitionSpecInput = {
  key: string;
  name: string;
  createPermission: PermissionSpecInput;
  states: StateSpecInput[];
  transitions: TransitionSpecInput[];
  // Fields is OPTIONAL, mirroring internal/workflow.DefinitionSpec.Fields
  // (Open Points item 35): omitted or empty means "no schema", exactly
  // today's freeform-payload workflow. DefinitionBuilder.tsx only sets
  // this when the owner actually added at least one payload field.
  fields?: FieldSpecInput[];
};

// A firm's second default-seeded workflow (see
// internal/workflow/customer_pipeline.go's CustomerPipelineKey) -
// exported the same way STOCK_TO_SALE_KEY is, for any page that wants to
// address it directly by its well-known key rather than searching
// fetchDefinitions() results for it.
export const CUSTOMER_PIPELINE_KEY = "customer_pipeline";

export type StateCount = {
  stateKey: string;
  stateName: string;
  count: number;
};

export type DefinitionInstanceCounts = {
  definitionId: string;
  key: string;
  name: string;
  counts: StateCount[];
};

// --- Rule Engine (Open Points item 7, the builder UI batch) -------------
//
// ExpressionNode/Action are internal/workflow/rules.go's condition-tree/
// action wire shapes, mirrored 1:1 (see handlers.go's
// expressionNodeResponse/actionResponse and their request-side
// counterparts) - the same "flat type, all fields optional" shape
// FieldSpecInput above already uses.

export type LogicOp = "and" | "or" | "not";
export type ConditionType = "field" | "state" | "field_compare";
export type CompareOp = "eq" | "neq" | "lt" | "gt" | "lte" | "gte" | "contains";
export type ActionType = "transition" | "notify" | "set_field";
export type NotifyChannel = "audit_log";
export type RuleTrigger = "on_create" | "on_transition";

// ExpressionNode is either a logic node (op is "and"/"or"/"not",
// children set, type empty) or a condition leaf (type set, selecting
// which of the leaf-specific fields below apply) - see
// internal/workflow/rules.go's own ExpressionNode doc comment. TypeScript
// can't cheaply express "exactly one of these shapes" as a discriminated
// union here without the tree-builder component doing its own narrowing
// anyway, so this mirrors the Go side's own "one flat struct" choice.
export type ExpressionNode = {
  op?: LogicOp | CompareOp | "";
  children?: ExpressionNode[];
  type?: ConditionType | "";
  field?: string;
  value?: unknown;
  definitionKey?: string;
  instanceIdField?: string;
  state?: string;
  fieldA?: string;
  fieldB?: string;
};

export type RuleAction = {
  type: ActionType;
  actionKey?: string;
  channel?: NotifyChannel;
  messageTemplate?: string;
  field?: string;
  value?: unknown;
};

export type Rule = {
  id: string;
  definitionKey: string;
  name: string;
  trigger: RuleTrigger;
  conditionTree: ExpressionNode;
  actions: RuleAction[];
  autonomous: boolean;
  enabled: boolean;
};

// The create-rule request body's shape (no id/definitionKey - those are
// server-assigned/path-derived, see internal/workflow/handlers.go's
// createRuleRequest).
export type RuleInput = {
  name: string;
  trigger: RuleTrigger;
  conditionTree: ExpressionNode;
  actions: RuleAction[];
  autonomous: boolean;
  enabled: boolean;
};

// RuleUpdateInput mirrors internal/workflow.RuleUpdate's partial-update
// contract: an omitted field leaves that column unchanged.
export type RuleUpdateInput = Partial<RuleInput>;

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/workflow-definitions?key=...`,
 * mirroring lib/me.ts's fetchMe / lib/wizard.ts's fetchWizardNode. Returns
 * null on any failure (missing/expired token, backend unreachable, no
 * such definition for this firm - e.g. the firm predates this workflow
 * being seeded) rather than throwing.
 */
export async function fetchDefinitionByKey(
  token: string,
  firmId: string,
  key: string,
): Promise<WorkflowDefinition | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/workflow-definitions?key=${encodeURIComponent(key)}`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) return null;
    return (await res.json()) as WorkflowDefinition;
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/workflow-definitions`
 * with no `?key=` - list mode (see internal/workflow.ListDefinitions and
 * handlers.go's handleWorkflowDefinitions). The data source for a
 * firm-level "Workflows" view: every definition the firm has, not one
 * hardcoded by key. Returns null on failure, same convention as
 * fetchDefinitionByKey.
 */
export async function fetchDefinitions(
  token: string,
  firmId: string,
): Promise<WorkflowDefinition[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/workflow-definitions`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) return null;
    return (await res.json()) as WorkflowDefinition[];
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/workflow-instance-counts`
 * (item 2: the dashboard's overview data source) - every workflow
 * definition the firm has, each with its instance count broken down by
 * state, computed as one grouped query server-side rather than derived
 * from fetchInstances/fetchDefinitions client-side. Returns null on
 * failure, same convention as fetchDefinitions/fetchInstances.
 */
export async function fetchInstanceCounts(
  token: string,
  firmId: string,
): Promise<DefinitionInstanceCounts[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/workflow-instance-counts`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) return null;
    return (await res.json()) as DefinitionInstanceCounts[];
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/workflow-definitions`
 * (item 1: expose internal/workflow.DefineWorkflow over HTTP, owner-only)
 * - the builder's submit action. Same failure-surfacing convention as
 * createInstance/executeTransition below (not the swallow-to-null read
 * helpers above): the builder needs to show the caller why a definition
 * didn't get created (e.g. 403 not-owner, 409 duplicate key, 400 an
 * invalid spec the backend's own DefinitionSpec.Validate rejected even
 * though the client-side check passed).
 */
export async function defineWorkflow(
  token: string,
  firmId: string,
  spec: DefinitionSpecInput,
): Promise<{ ok: true; definition: WorkflowDefinition } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/workflow-definitions`,
      {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify(spec),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return {
        ok: false,
        error: text || `Request failed (${res.status})`,
        status: res.status,
      };
    }
    return { ok: true, definition: (await res.json()) as WorkflowDefinition };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/workflow-definitions/{definitionId}/instances`
 * (e.g. the stock list). Returns null on failure, same convention as
 * fetchDefinitionByKey.
 */
export async function fetchInstances(
  token: string,
  firmId: string,
  definitionId: string,
): Promise<InstanceState[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/workflow-definitions/${encodeURIComponent(definitionId)}/instances`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) return null;
    return (await res.json()) as InstanceState[];
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `GET .../instances` with `?limit=`/`?offset=`/`?q=`
 * - the paged, filterable read path a listing screen with real volume
 * needs, as opposed to fetchInstances above (still unpaginated, used by
 * WorkflowHistory's own correlation lookup, which wants every instance's
 * current payload as a fallback, not just the current page's). The
 * backend reports the filtered-but-unpaged total via the `X-Total-Count`
 * response header rather than changing the array body shape (see
 * handleListInstances's doc comment) - read here to compute page count.
 * Returns null on failure, same convention as fetchInstances.
 */
export async function fetchInstancesPage(
  token: string,
  firmId: string,
  definitionId: string,
  opts: { limit: number; offset: number; q?: string },
): Promise<ListInstancesPage | null> {
  try {
    const params = new URLSearchParams({
      limit: String(opts.limit),
      offset: String(opts.offset),
    });
    if (opts.q) params.set("q", opts.q);
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/workflow-definitions/${encodeURIComponent(definitionId)}/instances?${params.toString()}`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) return null;
    const instances = (await res.json()) as InstanceState[];
    const total = Number(res.headers.get("X-Total-Count") ?? instances.length);
    return { instances, total: Number.isFinite(total) ? total : instances.length };
  } catch {
    return null;
  }
}

export type WorkflowSearchGroup = {
  definition: WorkflowDefinition;
  instances: InstanceState[];
  total: number;
};

/**
 * Item 3, firm-wide search: aggregates fetchInstancesPage's own
 * per-definition search across every definition the firm has, in
 * parallel. Deliberately a thin loop over the existing, already-tested
 * per-definition search (same query param, same backend endpoint) rather
 * than a new backend "search everything" query - see
 * WorkflowDefinitionView/WorkflowInstanceList for the per-definition
 * search this reuses. Definitions with zero matches are dropped from the
 * result rather than returned as empty groups, so the global search
 * page's "grouped by workflow" list only shows workflows that actually
 * matched.
 */
export async function searchAcrossDefinitions(
  token: string,
  firmId: string,
  definitions: WorkflowDefinition[],
  q: string,
  limitPerDefinition = 5,
): Promise<WorkflowSearchGroup[]> {
  const groups = await Promise.all(
    definitions.map(async (definition) => {
      const page = await fetchInstancesPage(token, firmId, definition.definitionId, {
        limit: limitPerDefinition,
        offset: 0,
        q,
      });
      return {
        definition,
        instances: page?.instances ?? [],
        total: page?.total ?? 0,
      };
    }),
  );
  return groups.filter((g) => g.total > 0);
}

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/workflow-definitions/{definitionId}/instances`
 * ("add stock"). Same failure-surfacing convention as executeTransition
 * below (not fetchDefinitionByKey/fetchInstances's swallow-to-null one):
 * a form submitting this needs to show the caller why it didn't go
 * through (e.g. 403 permission denied), not just that it failed.
 */
export async function createInstance(
  token: string,
  firmId: string,
  definitionId: string,
  payload: Record<string, unknown>,
): Promise<{ ok: true; instance: InstanceState } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/workflow-definitions/${encodeURIComponent(definitionId)}/instances`,
      {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ payload }),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return {
        ok: false,
        error: text || `Request failed (${res.status})`,
        status: res.status,
      };
    }
    return { ok: true, instance: (await res.json()) as InstanceState };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/workflow-instances/{instanceId}/transitions/{actionKey}`
 * - any transition on any workflow definition, not just "record a sale"
 * (actionKey and payload are exactly what the backend's own generic
 * ExecuteTransition takes; nothing stock-specific here). Unlike the read
 * helpers above, failures are surfaced to the caller rather than
 * swallowed - a UI executing this needs to show the caller why it didn't
 * go through (e.g. 403 permission denied), not just that it failed.
 */
export async function executeTransition(
  token: string,
  firmId: string,
  instanceId: string,
  actionKey: string,
  payload: Record<string, unknown> = {},
): Promise<{ ok: true; instance: InstanceState } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/workflow-instances/${encodeURIComponent(instanceId)}/transitions/${encodeURIComponent(actionKey)}`,
      {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ payload }),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return {
        ok: false,
        error: text || `Request failed (${res.status})`,
        status: res.status,
      };
    }
    return { ok: true, instance: (await res.json()) as InstanceState };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/**
 * Calls the Go backend's `GET .../workflow-definitions/{definitionKey}/rules`
 * - the read-only rule listing endpoint. Returns null on failure, same
 * convention as fetchDefinitionByKey/fetchInstances above.
 */
export async function fetchRules(
  token: string,
  firmId: string,
  definitionKey: string,
): Promise<Rule[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/workflow-definitions/${encodeURIComponent(definitionKey)}/rules`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) return null;
    return (await res.json()) as Rule[];
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `POST .../workflow-definitions/{definitionKey}/rules`
 * (owner-only). Same failure-surfacing convention as defineWorkflow/
 * createInstance above (not the swallow-to-null read helpers) - the
 * builder needs to show the caller why a rule didn't get created (e.g.
 * 403 not-owner, 400 an invalid tree the backend's own
 * ValidateExpressionTree/ValidateActions rejected).
 */
export async function createRule(
  token: string,
  firmId: string,
  definitionKey: string,
  rule: RuleInput,
): Promise<{ ok: true; rule: Rule } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/workflow-definitions/${encodeURIComponent(definitionKey)}/rules`,
      {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify(rule),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, rule: (await res.json()) as Rule };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/**
 * Calls the Go backend's `PATCH .../workflow-definitions/{definitionKey}/rules/{ruleId}`
 * (owner-only) - a partial update (RuleUpdateInput's omitted fields leave
 * that column unchanged, see internal/workflow.RuleUpdate).
 */
export async function updateRule(
  token: string,
  firmId: string,
  definitionKey: string,
  ruleId: string,
  patch: RuleUpdateInput,
): Promise<{ ok: true; rule: Rule } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/workflow-definitions/${encodeURIComponent(definitionKey)}/rules/${encodeURIComponent(ruleId)}`,
      {
        method: "PATCH",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify(patch),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, rule: (await res.json()) as Rule };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/**
 * Calls the Go backend's `DELETE .../workflow-definitions/{definitionKey}/rules/{ruleId}`
 * (owner-only).
 */
export async function deleteRule(
  token: string,
  firmId: string,
  definitionKey: string,
  ruleId: string,
): Promise<{ ok: true } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/workflow-definitions/${encodeURIComponent(definitionKey)}/rules/${encodeURIComponent(ruleId)}`,
      {
        method: "DELETE",
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}
