export type StateInfo = {
  key: string;
  name: string;
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

export type WorkflowDefinition = {
  definitionId: string;
  key: string;
  name: string;
};

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
 * Calls the Go backend's `POST /api/firms/{firmId}/workflow-instances/{instanceId}/transitions/{actionKey}`
 * (e.g. "record a sale"). Unlike the read helpers above, failures are
 * surfaced to the caller rather than swallowed - the stock UI needs to
 * show the caller why a sale didn't go through (e.g. 403 permission
 * denied), not just that it failed.
 */
export async function executeTransition(
  token: string,
  firmId: string,
  instanceId: string,
  actionKey: string,
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
        body: JSON.stringify({ payload: {} }),
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
