// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import type { ElementType, ReactNode } from "react";

type Props = {
  as?: ElementType;
  interactive?: boolean;
  className?: string;
  children: ReactNode;
  [key: string]: unknown;
};

// The one canonical card/panel surface (item 1 of this batch) -
// components/styles/tokens.css's `.panel` class, wrapped as a small
// shared primitive so React components can compose it the same way they
// already compose any other shared component, rather than having to
// remember to hand-append the class name themselves. Either usage is
// fine (this repo already had zero components/ui/ primitives before
// this batch); `.panel` remains available directly for callers that just
// want the class on an element they otherwise control (e.g. a <Link>
// that also needs `data-permission-*`, which this wrapper would have to
// forward anyway).
export default function Panel({ as, interactive, className, children, ...rest }: Props) {
  const Tag = as ?? "div";
  const classes = ["panel", interactive ? "panel--interactive" : "", className ?? ""]
    .filter(Boolean)
    .join(" ");
  return (
    <Tag className={classes} {...rest}>
      {children}
    </Tag>
  );
}
