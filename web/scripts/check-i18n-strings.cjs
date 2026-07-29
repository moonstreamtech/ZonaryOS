// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// CI Checklist item 5, "i18n Hardcoded-String Check" (CLAUDE.md
// Never-Violate Rule 4: "All user-facing text goes through the i18n
// layer from the start"). AST-based (via the TypeScript compiler API,
// already a devDependency here - not a fresh regex heuristic), scanning
// every .ts/.tsx file under src/ for:
//
//   1. JSX text nodes containing letters, rendered directly (not inside a
//      {t("key")} expression container - those are JsxExpression nodes,
//      not JsxText, so they're invisible to this walk entirely).
//   2. String-literal JSX attribute values for a fixed set of text-
//      bearing attributes (placeholder, aria-label, title, alt) that
//      contain letters.
//
// A line can opt out with a trailing `// i18n-ignore` comment - for the
// rare case of genuinely non-UI text (e.g. this codebase's workflow
// state/action names, which come from the backend as data, not UI copy -
// see web/src/app/[locale]/stock/StockList.tsx's own doc comment on why
// those specifically are out of scope for the i18n layer).
const fs = require("fs");
const path = require("path");
const ts = require("typescript");

const SRC_ROOT = path.join(__dirname, "..", "src");
const TEXT_ATTRIBUTES = new Set(["placeholder", "aria-label", "title", "alt"]);
const HAS_LETTERS = /[A-Za-z]{2,}/;
const IGNORE_MARKER = "i18n-ignore";

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

function lineIsIgnored(sourceFile, lineNumber) {
  const lineText = sourceFile.getFullText().split("\n")[lineNumber] || "";
  return lineText.includes(IGNORE_MARKER);
}

function checkFile(filePath) {
  const text = fs.readFileSync(filePath, "utf8");
  const sourceFile = ts.createSourceFile(filePath, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const violations = [];

  function visit(node) {
    if (ts.isJsxText(node)) {
      const raw = node.getText(sourceFile);
      if (HAS_LETTERS.test(raw)) {
        const ln = lineOf(sourceFile, node.getStart(sourceFile));
        if (!lineIsIgnored(sourceFile, ln)) {
          violations.push({ line: ln + 1, snippet: raw.trim().slice(0, 60) });
        }
      }
    }

    if (ts.isJsxAttribute(node) && node.initializer && ts.isStringLiteral(node.initializer)) {
      const attrName = node.name.getText(sourceFile);
      if (TEXT_ATTRIBUTES.has(attrName) && HAS_LETTERS.test(node.initializer.text)) {
        const ln = lineOf(sourceFile, node.getStart(sourceFile));
        if (!lineIsIgnored(sourceFile, ln)) {
          violations.push({ line: ln + 1, snippet: `${attrName}="${node.initializer.text}"` });
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
      console.error(`${rel}:${v.line}: possible hardcoded UI string: ${JSON.stringify(v.snippet)}`);
    }
  }

  if (violationCount > 0) {
    console.error(
      `\n${violationCount} possible hardcoded UI string(s) found - route them through next-intl's ` +
        `useTranslations/getTranslations (see web/messages/{locale}.json) instead, or mark a ` +
        `deliberate exception with a trailing "// ${IGNORE_MARKER}" comment on that line.`,
    );
    process.exit(1);
  }

  console.log("i18n hardcoded-string check passed");
}

main();
