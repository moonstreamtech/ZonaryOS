import { describe, expect, it, beforeEach } from "vitest";
import { scanInteractiveElements } from "./useAuditBadges";

// Vision §3: "Buttons without a permission tag are flagged with a
// separate warning badge ... a self-audit mechanism for catching
// permission checks forgotten during development." These tests prove
// the classification rules that badge actually depends on, rather than
// relying on a manual visual check of the running app.

beforeEach(() => {
  document.body.innerHTML = "";
});

describe("scanInteractiveElements", () => {
  it("classifies an untagged button as untagged - the forgotten-tag case", () => {
    document.body.innerHTML = `<button id="mystery">Do a thing</button>`;

    const targets = scanInteractiveElements();

    expect(targets).toHaveLength(1);
    expect(targets[0].kind).toBe("untagged");
    expect(targets[0].permissionKey).toBeUndefined();
  });

  it("classifies a data-permission-key button as tagged, carrying the key", () => {
    document.body.innerHTML = `<button data-permission-key="workflow.stock_to_sale.record_sale">Sell</button>`;

    const targets = scanInteractiveElements();

    expect(targets).toHaveLength(1);
    expect(targets[0].kind).toBe("tagged");
    expect(targets[0].permissionKey).toBe(
      "workflow.stock_to_sale.record_sale",
    );
  });

  it("excludes a data-permission-public element entirely - no badge either way", () => {
    document.body.innerHTML = `<a href="/stock" data-permission-public="true">Go to stock</a>`;

    const targets = scanInteractiveElements();

    expect(targets).toHaveLength(0);
  });

  it("excludes elements inside the Audit Mode overlay's own root, even if untagged", () => {
    document.body.innerHTML = `
      <div data-audit-overlay-root>
        <button>Close</button>
      </div>
    `;

    const targets = scanInteractiveElements();

    expect(targets).toHaveLength(0);
  });

  it("does not scan a plain <a> with no href (not actually a link)", () => {
    document.body.innerHTML = `<a>Not a real link</a>`;

    const targets = scanInteractiveElements();

    expect(targets).toHaveLength(0);
  });

  it("classifies a mix of elements independently, in document order", () => {
    document.body.innerHTML = `
      <button data-permission-key="a.b.c">Tagged</button>
      <button>Forgotten</button>
      <a href="/x" data-permission-public="true">Public link</a>
      <a href="/y">Also forgotten</a>
    `;

    const targets = scanInteractiveElements();

    expect(targets.map((t) => t.kind)).toEqual([
      "tagged",
      "untagged",
      "untagged",
    ]);
    expect(targets[0].permissionKey).toBe("a.b.c");
  });
});
