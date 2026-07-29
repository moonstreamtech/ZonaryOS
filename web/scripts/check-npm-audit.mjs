// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// CI Checklist item 10, "Dependency Vulnerability Scan" (frontend half -
// see scripts/check_govulncheck.py for the backend/Go half). Runs `npm
// audit --json` and fails on any high/critical-severity finding that
// isn't explicitly listed in audit-allowlist.json - a documented
// exception, not a silent ignore. Each allowlist entry must carry a
// `reason` and a `reviewBy` date so a suppressed finding stays visible in
// code review rather than disappearing forever.
//
// Only high/critical findings are enforced: moderate/low/info findings are
// reported but don't fail the build, matching the task's own instruction
// to reserve the documented-exception mechanism for what can't be fixed
// immediately, not for everything below high.
import { execSync } from "child_process";
import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ALLOWLIST_PATH = path.join(__dirname, "..", "audit-allowlist.json");
const ENFORCED_SEVERITIES = new Set(["high", "critical"]);

function loadAllowlist() {
  const raw = JSON.parse(fs.readFileSync(ALLOWLIST_PATH, "utf8"));
  const now = new Date();
  const byUrl = new Map();
  for (const entry of raw) {
    if (!entry.url || !entry.reason || !entry.reviewBy) {
      throw new Error(`audit-allowlist.json entry missing url/reason/reviewBy: ${JSON.stringify(entry)}`);
    }
    if (new Date(entry.reviewBy) < now) {
      console.error(`audit-allowlist.json entry for ${entry.url} has an expired reviewBy (${entry.reviewBy}) - re-review it, don't just push the date out silently.`);
      process.exitCode = 1;
    }
    byUrl.set(entry.url, entry);
  }
  return byUrl;
}

function runNpmAudit() {
  try {
    const out = execSync("npm audit --json", { cwd: path.join(__dirname, ".."), encoding: "utf8" });
    return JSON.parse(out);
  } catch (err) {
    // npm audit exits non-zero when vulnerabilities are found - that's the
    // normal "findings exist" case, not a tool failure. stdout still has
    // the JSON report in that case.
    if (err.stdout) {
      return JSON.parse(err.stdout);
    }
    throw err;
  }
}

function collectFindings(report) {
  const findings = new Map(); // url -> { package, severity, title }
  for (const [pkgName, vuln] of Object.entries(report.vulnerabilities || {})) {
    for (const via of vuln.via || []) {
      if (typeof via !== "object") continue; // a string `via` is just a dependency-chain reference, not its own advisory
      if (!via.url) continue;
      findings.set(via.url, { package: pkgName, severity: via.severity, title: via.title });
    }
  }
  return findings;
}

function main() {
  const allowlist = loadAllowlist();
  const report = runNpmAudit();
  const findings = collectFindings(report);

  const unallowlisted = [];
  const allowlisted = [];
  for (const [url, finding] of findings) {
    if (!ENFORCED_SEVERITIES.has(finding.severity)) continue;
    if (allowlist.has(url)) {
      allowlisted.push({ url, ...finding });
    } else {
      unallowlisted.push({ url, ...finding });
    }
  }

  if (allowlisted.length > 0) {
    console.log(`${allowlisted.length} high/critical finding(s) suppressed via audit-allowlist.json:`);
    for (const f of allowlisted) {
      console.log(`  - [${f.severity}] ${f.package}: ${f.title} (${f.url})`);
    }
  }

  if (unallowlisted.length > 0) {
    console.error(`\n${unallowlisted.length} high/critical finding(s) NOT in audit-allowlist.json:`);
    for (const f of unallowlisted) {
      console.error(`  - [${f.severity}] ${f.package}: ${f.title} (${f.url})`);
    }
    console.error(
      "\nFix these, or if there is genuinely no non-breaking fix available yet, add a documented " +
        "entry (url, reason, reviewBy) to web/audit-allowlist.json - do not silently ignore them.",
    );
    process.exitCode = 1;
    return;
  }

  console.log("\nnpm audit check passed (no unallowlisted high/critical findings)");
}

main();
