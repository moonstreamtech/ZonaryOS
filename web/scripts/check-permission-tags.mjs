// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// CI Checklist item (new this batch), "Permission-Tag Coverage Check"
// (CLAUDE.md Never-Violate Rule 7: "Every permission-gated UI element
// must carry a permission tag. Untagged interactive elements should be
// flagged, not silently shipped"). Until this script, Rule 7 was only
// enforced by Permission Audit Mode's runtime overlay
// (components/AuditMode/useAuditBadges.ts's scanInteractiveElements) -
// a live-DOM check a developer has to remember to actually open and look
// at. This is the static, CI-enforced counterpart: same AST-based
// philosophy as check-i18n-strings.mjs (TypeScript compiler API,
// `typescript` already a devDependency - not a fresh regex heuristic or
// a new AST-parsing dependency), scanning every .ts/.tsx file under
// src/.
//
// What counts as "interactive" here deliberately mirrors
// useAuditBadges.ts's own INTERACTIVE_SELECTOR ("button, a[href]") as
// closely as static analysis allows, so the static check and the
// runtime overlay agree on what needs a tag instead of silently
// disagreeing:
//
//   1. Any <button> element.
//   2. Any element carrying a literal `onClick` attribute (covers
//      non-<button> clickable elements, e.g. custom controls).
//   3. Any <form> element carrying a literal `onSubmit` attribute.
//   4. Any <a> or <Link> (next/link, this repo's `@/i18n/navigation`
//      re-export included - both render to a real <a href> in the DOM,
//      same element the runtime overlay scans) element carrying a
//      literal `href` attribute - this repo's own established
//      convention already tags *every* such link, including plain
//      navigation ones, with `data-permission-public="true"` (see e.g.
//      Nav/NavShell.tsx) rather than treating "just navigates" as
//      exempt, so this check follows that convention rather than trying
//      to (unreliably, statically) distinguish "mutating" links from
//      "navigational" ones.
//
// A qualifying element must carry one of this repo's two established
// markers as a literal JSX attribute:
//
//   - `data-permission-key="<catalog-key>"` - gated by a real
//     permission-catalog key (permission.Has-checked on the backend).
//   - `data-permission-public="true"` - deliberately, structurally not
//     permission-gated (e.g. Accept Invite, plain navigation links, an
//     is_owner-gated element - is_owner is a structural flag, not a
//     permission-catalog key, so there is no key to tag it with; see
//     docs/DEVELOPMENT.md's Permission Audit Mode section and
//     DefinitionBuilder.tsx's own doc comment on this exact point).
//
// Limitation (matching how check-i18n-strings.mjs documents its own
// scope in its header): this only verifies a LITERAL string attribute
// is present on the element. It cannot follow a spread prop
// ({...props}), a marker applied conditionally/dynamically, or a
// marker applied by a wrapping component the checked element merely
// renders inside of (whether the permission key passed as a prop
// actually maps to a real catalog key, or whether a marker forwarded
// through a shared wrapper component actually reaches the DOM element
// it claims to, is not checkable by a single-file AST walk without full
// type-flow analysis - out of scope here, same as check-i18n-strings.mjs
// not resolving what a `{t(...)}` call's key actually contains).
//
// A line can opt out with a trailing `// permission-audit-ignore: <reason>`
// comment - mirroring i18n-ignore's marker style but, per this repo's
// cmd/ciaudit convention (`ciaudit:ignore-firmid-check: <reason>` -
// see e.g. internal/workflow/customer_pipeline.go), requiring an actual
// explanatory reason after the colon, not a bare suppression tag.
//
// ESM (not .cjs): matching check-i18n-strings.mjs's own reasoning - this
// repo's ESLint config flags require()-style imports repo-wide.
import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";
import ts from "typescript";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const SRC_ROOT = path.join(__dirname, "..", "src");
const IGNORE_MARKER = "permission-audit-ignore";
// Requires a colon followed by real text, e.g. "permission-audit-ignore:
// pagination control, structurally not permission-gated" - a bare
// "// permission-audit-ignore" with nothing after the colon does not
// count (see header comment: this mirrors cmd/ciaudit's
// ciaudit:ignore-<reason> convention, not a blanket bare tag).
const IGNORE_PATTERN = new RegExp(`${IGNORE_MARKER}:\\s*\\S.{4,}`);

const LINK_TAGS = new Set(["a", "Link"]);
const PERMISSION_KEY_ATTR = "data-permission-key";
const PERMISSION_PUBLIC_ATTR = "data-permission-public";

function walk(dir, files) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      walk(full, files);
    } else if (/\.(tsx?|jsx?)$/.test(entry.name) && !entry.name.endsWith(".test.ts") && !entry.name.endsWith(".test.tsx")) {
      files.push(full);
    }
  }
  return files;
}

function lineOf(sourceFile, pos) {
  return sourceFile.getLineAndCharacterOfPosition(pos).line; // 0-based
}

function rangeIsIgnored(sourceFile, startLine, endLine) {
  const lines = sourceFile.getFullText().split("\n");
  for (let ln = startLine; ln <= endLine && ln < lines.length; ln++) {
    if (IGNORE_PATTERN.test(lines[ln])) {
      return true;
    }
  }
  return false;
}

function getTagName(node) {
  const tag = node.tagName;
  return ts.isIdentifier(tag) ? tag.text : tag.getText();
}

function getAttribute(node, name) {
  for (const prop of node.attributes.properties) {
    if (ts.isJsxAttribute(prop) && prop.name.getText() === name) {
      return prop;
    }
  }
  return undefined;
}

function hasSpreadAttribute(node) {
  return node.attributes.properties.some((p) => ts.isJsxSpreadAttribute(p));
}

function isMarkedPermissionSafe(node) {
  const keyAttr = getAttribute(node, PERMISSION_KEY_ATTR);
  if (keyAttr) return true;
  const publicAttr = getAttribute(node, PERMISSION_PUBLIC_ATTR);
  if (publicAttr) return true;
  return false;
}

// Returns a reason string ("button" / "onClick handler" / "onSubmit
// form" / "link") if `node` (a JsxOpeningElement or
// JsxSelfClosingElement) is an interactive element per this check's
// scope, or null if it's out of scope entirely.
function interactiveReason(node) {
  const tagName = getTagName(node);
  if (tagName === "button") return "button element";

  const onClick = getAttribute(node, "onClick");
  if (onClick) return "element with an onClick handler";

  if (tagName === "form" && getAttribute(node, "onSubmit")) {
    return "form element with an onSubmit handler";
  }

  if (LINK_TAGS.has(tagName) && getAttribute(node, "href")) {
    return `${tagName} element with an href`;
  }

  return null;
}

function checkFile(filePath) {
  const text = fs.readFileSync(filePath, "utf8");
  const sourceFile = ts.createSourceFile(filePath, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const violations = [];

  function visit(node) {
    if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) {
      const reason = interactiveReason(node);
      if (reason) {
        if (hasSpreadAttribute(node)) {
          // Not statically checkable - a spread prop could carry either
          // marker. See header comment's "Limitation" note. Skipped, not
          // flagged, to avoid a false positive on something this walk
          // genuinely cannot verify.
        } else if (!isMarkedPermissionSafe(node)) {
          const startLine = lineOf(sourceFile, node.getStart(sourceFile));
          const endLine = lineOf(sourceFile, node.getEnd());
          if (!rangeIsIgnored(sourceFile, startLine, endLine)) {
            violations.push({
              line: startLine + 1,
              snippet: `<${getTagName(node)}> (${reason})`,
            });
          }
        }
      }
    }

    ts.forEachChild(node, visit);
  }

  visit(sourceFile);
  return violations;
}

function main() {
  const files = walk(SRC_ROOT, []);
  let violationCount = 0;

  for (const file of files) {
    const violations = checkFile(file);
    if (violations.length === 0) continue;
    violationCount += violations.length;
    const rel = path.relative(path.join(__dirname, ".."), file);
    for (const v of violations) {
      console.error(`${rel}:${v.line}: untagged interactive element: ${v.snippet}`);
    }
  }

  if (violationCount > 0) {
    console.error(
      `\n${violationCount} untagged interactive element(s) found - CLAUDE.md Never-Violate Rule 7 ` +
        `requires every permission-gated UI element to carry a permission tag. Add ` +
        `${PERMISSION_KEY_ATTR}="<catalog-key>" if this is gated by a real permission-catalog key, ` +
        `${PERMISSION_PUBLIC_ATTR}="true" if it's deliberately, structurally not permission-gated ` +
        `(e.g. plain navigation, an is_owner-only structural check), or mark a deliberate, explained ` +
        `exception with a trailing "// ${IGNORE_MARKER}: <reason>" comment on that line.`,
    );
    process.exit(1);
  }

  console.log("permission-tag coverage check passed");
}

main();
