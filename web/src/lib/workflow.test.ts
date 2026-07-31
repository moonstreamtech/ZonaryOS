// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { afterEach, describe, expect, it, vi } from "vitest";
import { searchAcrossDefinitions, type WorkflowDefinition } from "./workflow";

// Item 3's aggregation layer: proves searchAcrossDefinitions is a thin
// loop over fetchInstancesPage's own per-definition search (one fetch
// per definition, same query params fetchInstancesPage already sends -
// see WorkflowInstanceList's own search box, the UI half of that
// existing per-definition search) rather than a parallel/duplicate
// search implementation, and that it groups results by definition and
// drops definitions with zero matches.

const stockDefinition: WorkflowDefinition = {
  definitionId: "stock-def-id",
  key: "stock_to_sale",
  name: "Stock In -> Sale",
  createPermissionKey: "workflow.stock_to_sale.add_stock",
};

const crmDefinition: WorkflowDefinition = {
  definitionId: "crm-def-id",
  key: "customer_pipeline",
  name: "Customer Pipeline",
  createPermissionKey: "workflow.customer_pipeline.create_lead",
};

function jsonResponse(body: unknown, totalHeader: string) {
  return {
    ok: true,
    headers: new Headers({ "X-Total-Count": totalHeader }),
    json: async () => body,
  } as unknown as Response;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("searchAcrossDefinitions", () => {
  it("aggregates matches across multiple definitions, grouped by definition", async () => {
    const fetchMock = vi.fn(async (url: string) => {
      if (url.includes("stock-def-id")) {
        return jsonResponse(
          [{ instanceId: "i1", workflowDefinitionId: "stock-def-id", state: { key: "in_stock", name: "In Stock" }, payload: { item: "Widget" }, availableActions: [] }],
          "1",
        );
      }
      if (url.includes("crm-def-id")) {
        return jsonResponse(
          [{ instanceId: "i2", workflowDefinitionId: "crm-def-id", state: { key: "lead", name: "Lead" }, payload: { name: "Acme" }, availableActions: [] }],
          "1",
        );
      }
      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock);

    const groups = await searchAcrossDefinitions(
      "token",
      "firm-id",
      [stockDefinition, crmDefinition],
      "widget",
    );

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(groups).toHaveLength(2);
    expect(groups.map((g) => g.definition.key).sort()).toEqual([
      "customer_pipeline",
      "stock_to_sale",
    ]);
    const stockGroup = groups.find((g) => g.definition.key === "stock_to_sale");
    expect(stockGroup?.instances).toHaveLength(1);
    expect(stockGroup?.total).toBe(1);
  });

  it("drops definitions with zero matches from the result", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.includes("stock-def-id")) {
          return jsonResponse([], "0");
        }
        return jsonResponse(
          [{ instanceId: "i2", workflowDefinitionId: "crm-def-id", state: { key: "lead", name: "Lead" }, payload: {}, availableActions: [] }],
          "1",
        );
      }),
    );

    const groups = await searchAcrossDefinitions(
      "token",
      "firm-id",
      [stockDefinition, crmDefinition],
      "acme",
    );

    expect(groups).toHaveLength(1);
    expect(groups[0].definition.key).toBe("customer_pipeline");
  });

  it("returns an empty array (not an error) when every definition's search fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({ ok: false }) as unknown as Response),
    );

    const groups = await searchAcrossDefinitions(
      "token",
      "firm-id",
      [stockDefinition],
      "anything",
    );

    expect(groups).toEqual([]);
  });

  it("passes the query and per-definition limit through to each fetch", async () => {
    const fetchMock = vi.fn(async () => jsonResponse([], "0"));
    vi.stubGlobal("fetch", fetchMock);

    await searchAcrossDefinitions("token", "firm-id", [stockDefinition], "widget", 3);

    const calledUrl = fetchMock.mock.calls[0][0] as string;
    expect(calledUrl).toContain("q=widget");
    expect(calledUrl).toContain("limit=3");
  });
});
