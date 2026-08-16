// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Projects module, frontend side (internal/project) - mirrors
// lib/manufacturing.ts's own "types + fetch helpers, one file per
// backend package, postJSON generic helper" convention.

export type ProjectStatus = "active" | "on_hold" | "completed" | "cancelled";

export type Project = {
  id: string;
  name: string;
  description?: string;
  status: ProjectStatus;
  startDate?: string;
  endDate?: string;
  budget?: string;
  currency: string;
  managerPersonId?: string;
  createdAt: string;
};

export type ProjectTaskStateCount = {
  stateKey: string;
  count: number;
};

export type ProjectSummary = {
  tasksByState: ProjectTaskStateCount[];
  totalTasks: number;
  totalHoursLogged: string;
  budget?: string;
  estimatedCost?: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/projects` (optional `status` filter). */
export async function fetchProjects(
  token: string,
  firmId: string,
  opts?: { status?: ProjectStatus },
): Promise<Project[] | null> {
  try {
    const query = opts?.status ? `?status=${encodeURIComponent(opts.status)}` : "";
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/projects${query}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Project[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/projects/{projectId}`. */
export async function fetchProject(token: string, firmId: string, projectId: string): Promise<Project | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/projects/${encodeURIComponent(projectId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Project;
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/projects/{projectId}/summary`. */
export async function fetchProjectSummary(
  token: string,
  firmId: string,
  projectId: string,
): Promise<ProjectSummary | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/projects/${encodeURIComponent(projectId)}/summary`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as ProjectSummary;
  } catch {
    return null;
  }
}

export type CreateProjectInput = {
  name: string;
  description?: string;
  startDate?: string;
  endDate?: string;
  budget?: string;
  currency?: string;
  managerPersonId?: string;
};

export type UpdateProjectInput = Partial<{
  name: string;
  description: string;
  status: ProjectStatus;
  startDate: string;
  endDate: string;
  budget: string;
  managerPersonId: string;
}>;

type ApiResult<T extends string, V> = { ok: true } & Record<T, V> | { ok: false; error: string; status: number };

async function postJSON<T extends string, V>(
  url: string,
  method: "POST" | "PATCH" | "DELETE",
  token: string,
  key: T,
  body?: unknown,
): Promise<ApiResult<T, V>> {
  try {
    const res = await fetch(url, {
      method,
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    const value = res.status === 204 ? (undefined as V) : ((await res.json()) as V);
    return { ok: true, [key]: value } as ApiResult<T, V>;
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `POST /api/firms/{firmId}/projects` (owner-only). */
export async function createProject(
  token: string,
  firmId: string,
  input: CreateProjectInput,
): Promise<ApiResult<"project", Project>> {
  return postJSON(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/projects`, "POST", token, "project", input);
}

/** Calls the Go backend's `PATCH /api/firms/{firmId}/projects/{projectId}` (owner-only). */
export async function updateProject(
  token: string,
  firmId: string,
  projectId: string,
  patch: UpdateProjectInput,
): Promise<ApiResult<"project", Project>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/projects/${encodeURIComponent(projectId)}`,
    "PATCH",
    token,
    "project",
    patch,
  );
}
