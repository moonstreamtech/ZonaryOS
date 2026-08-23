// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import EmptyState from "./EmptyState";

// This codebase has no React Testing Library component-test convention
// yet (see docs/DEVELOPMENT.md) - renderToStaticMarkup (react-dom, no new
// dependency) is enough to assert on the rendered HTML for this simple,
// side-effect-free presentational component.
describe("EmptyState", () => {
  it("renders the message and the illustration without an action", () => {
    const html = renderToStaticMarkup(<EmptyState message="No products yet." />);
    expect(html).toContain("No products yet.");
    expect(html).toContain("<svg");
  });

  it("renders the action when one is provided", () => {
    const html = renderToStaticMarkup(
      <EmptyState message="No products yet." action={<button type="button">Add your first product</button>} />,
    );
    expect(html).toContain("No products yet.");
    expect(html).toContain("Add your first product");
    expect(html).toContain("<button");
  });
});
