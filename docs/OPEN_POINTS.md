ZONARYOS — OPEN POINTS (TOPICS TO DISCUSS)
Rule 1: No item in this file moves to the main document (`docs/VISION.md`) until it's finalized. When a topic is discussed and decided: (1) it's added to the main document with the Developer's approval, (2) the corresponding entry here is deleted. Rule 2: No detail from any shared text/chat transcript is skipped — even a minor-looking detail is logged here so the same topic doesn't need to be re-thought during development. Item titles stay short; content doesn't get padded unnecessarily. Source: a transcript of a chat the Developer previously had with another AI.
17. Email Integration — Technical Implementation Detail (deliberately deferred)

* Finalized principle (see Vision §5): ZonaryOS will provide an email service for firms without their own domain; firms with their own mail server can connect it, no extra app needed.
* Developer's note: "Email technology will be discussed and decided" — i.e., deliberately deferred for now, not urgent.
* Claude's questions (to be discussed later): Will this be a fully self-hosted mail server, or integration with existing providers (Gmail/Outlook/own SMTP-IMAP server)? Standard protocols (IMAP/SMTP) only, or also API-based integration for major providers (Gmail API, Microsoft Graph)? Will this service only "display" email, or integrate with CRM/accounting modules (e.g. sending an invoice directly from that firm's email)?

7. Workflow Engine — Technical Detail

* The idea of a "graph-based process engine / state machine" and a `workflows` table has been discussed.
* Autonomous Decision Layer finalized (see Vision §3): unlimited-depth parametric "condition → action" rules, per-rule autonomous/human-approval choice, deterministic (no-ML) rule engine.
* Remaining open question: What will the rule engine's concrete data model/schema look like (how are rules stored in the database, how are conditions expressed — e.g. a simple DSL or a visual rule builder)? This should be handled together with item 12 (wizard question tree design), since rules will also be defined through the wizard.

8. Development Roadmap / Phasing — deliberately deferred

* Finalized constraint: i18n (multi-language) and audit infrastructure must be in the core layer from the start — not a feature to add later, since adding it later would require architectural changes.
* Developer's decision: The concrete roadmap will not be finalized now — it will be handled as a step right before development/production actually begins, deliberately deferred, not forced now.
* Reference (from a prior conversation, not yet approved as a draft): (1) core accounting+inventory (MVP), (2) parametric sector engine, (3) Edge/IoT layer — this is only an example, not binding.

12. Wizard Question Tree — Concrete Content (mechanism finalized, content open)

* Finalized mechanism (see Vision §3): The wizard is a decision tree — each feature appears as a question on the appropriate branch, each answer opens follow-up sub-questions (recursive), and a customer-specific structure activates at the end. Example root questions: "do you manufacture?", "do you track inventory?" — but these are examples, not the full list.
* Remaining open question: What will all the root/main branches of the tree be — the full list hasn't been designed yet. Together with item 8 (roadmap), this also determines which features get modeled into the tree first — this requires substantial design work and should be handled separately, later.

20. License Agreement Content (partially finalized)

* Finalized principle (see Vision §5): Code is open, but commercial use/copying is prohibited — usable only through ZonaryOS's own central service. It was assessed that no authorization from an official body/institution is required — a corporate license agreement is sufficient.
* Remaining open question: The concrete content/text of the agreement hasn't been written yet. Will this adapt an existing open-source license (e.g. AGPLv3/SSPL/BUSL), or will a custom license be written from scratch? Important: For this agreement to actually create legal liability and hold up in court, it should be drafted/reviewed by a legal professional (a lawyer) — Claude can propose a draft but the final text requires legal counsel.
* See item 32 — an encryption/integrity idea came up that would also technically support this principle.

24. Localization / Fiscal-Legal Compliance

* UI text multi-language support (i18n) is finalized (see Vision §4) — this item now only concerns fiscal/legal compliance: each country's tax system and e-invoice/e-ledger standards differ. How will a single global core handle this — will there be country-specific "compliance plug-ins"?
* No country restriction in scope was finalized (see Vision §1) — this means localization needs to be built into the architecture from the start (as a plug-in/module), but the concrete approach hasn't been determined yet.

25. Backup Frequency / SLA Target (partially finalized)

* Finalized principle (see Vision §4): At least 3 different locations, active-passive model (single global primary + read-only replicas + automatic failover). The real risk was identified as not being on the server side, but the customer's internet connectivity (see item 29).
* Remaining open questions: Is there a concrete uptime/SLA target (e.g. a number like "99.9%"), or will this be decided later by the technical team? Backup frequency and how geographically distant the 3 locations should be from each other (same country/continent vs. cross-continent) haven't been determined yet.

29. Offline / Connectivity-Loss Scenario (partially finalized)

* Finalized principle (see Vision §4): NATS JetStream was chosen as the message queue because it can run embedded/as a leaf node on the Edge Agent — when the internet drops, the Edge Agent buffers data locally and auto-syncs once the connection returns. This provides an architecture-level solution.
* Remaining open question: The concrete behavior of this buffer/queue mechanism — how long/how much data it can buffer (is there a disk capacity limit), and how conflicts are resolved during resync (e.g. if the same inventory record changed both locally and in the cloud during the sync window) — hasn't been designed yet. Whether a browser-side (IndexedDB) layer is also needed hasn't been determined.

30. Pricing — Concrete Numbers (partially finalized)

* Finalized principle (see Vision §6): a 3-tier model — module base fee (E) + usage band (B+D combined) + per-device add-on fee (C). Seat/user count (A) was deliberately left out.
* Remaining open question: Concrete numbers such as band thresholds, unit prices, and the module-to-base-fee mapping haven't been determined yet — this will be addressed separately as the product's scope (especially the roadmap in item 8) becomes clearer.

31. Practical Development Sequence / First Concrete Test Scenario

* Finalized in the vision: no country/sector restriction in the architecture, built from the start to be ready for any scenario (see Vision §1).
* Remaining open question: This doesn't mean "nothing gets prioritized" — development has to start somewhere. Which workflow will be used as the first concrete test/demo scenario (e.g. a simple retail sale, or a more complex manufacturing flow)? This should be discussed together with item 8 (roadmap/phasing).

32. License Verification Mechanism — Remaining Technical Details

* Finalized logic (see Vision §5): The code is open to everyone, and no one can be physically prevented from running it on their own server — but the software does not function without connecting to ZonaryOS's central server and obtaining a valid license verification. License verification also determines which modules are active on that installation.
* Developer's raw idea (mechanism proposal): "Something like a complex hexadecimal process, GitHub Actions secrets and variables, combined with each file's MD5 and version-specific values, encrypting the files."
* Remaining open questions (mechanism detail):
   1. Exactly when/how will verification trigger — on every app launch, periodically, or on every request to the central server?
   2. Will this verification halt the system when internet connectivity is lost (offline), or will there be a grace period? (See item 29 — it's finalized that the Edge Agent can buffer offline via NATS JetStream, but how the core system's/license verification's behavior in this scenario is a separate question.)
   3. What exactly does the MD5 + version-specific "encryption" protect — the integrity of the executable (verifying it hasn't been tampered with), or the license key/module activation data itself?
   4. Since this mechanism also lives inside the open-source code (public repo), the verification logic itself will be visible. In systems like this, the actual secret is usually kept server-side, with the client only implementing a protocol — will this approach be adopted?

33. Audit Data Retention Period and Data-Protection Question (partially finalized)

* Finalized principle (see Vision §3): Audit at the most detailed level (data changes + view/read records), access held by the firm administrator + opened to an external auditor via the "Auditor Role" if needed, data will not be deleted (at least 10 years).
* Remaining open questions: (1) Will the exact retention period be set as ZonaryOS's own parameter, or will it vary by compliance standard? (2) Whether this level of detailed monitoring (particularly retaining view/read records) creates an issue under Turkey's data protection law (KVKK) — an open question requiring legal counsel; Claude cannot resolve this.

34. Deployment Target / Infrastructure (not yet decided)

* Found while building out the CI pipeline's checklist items (see CLAUDE.md's "How to Verify a Change"): a "Canary/Rollback Trigger" CI check is listed as part of the eventual acceptance bar, but there is no decided deployment target or infrastructure for ZonaryOS to actually deploy to yet (checked: not in Vision, not elsewhere in this file) — Vision §4 only fixes the *database* topology (single global primary + read-only replicas, active-passive, automatic failover), not where/how the application binary itself gets deployed, released, or rolled back.
* Remaining open question: what is the actual deployment target (cloud provider, container orchestration platform, bare-metal, PaaS, ...) and release mechanism (blue/green, canary, rolling)? A rollback trigger needs a real deploy pipeline to hook into — inventing one just to attach a CI check to would be designing production infrastructure that hasn't been decided, not implementing an agreed decision. Left as "Not Set Up" in the CI Checklist until this is resolved.

38. Commit-Signing Infrastructure and Policy (investigated, Developer decision needed)

* Background: GPG/SSH commit-signing infrastructure was unavailable when PR #9 (Permission Audit Mode UI) was ready to commit. Claude Code deferred rather than deciding unilaterally (per Working Rule 7) and committed with `--no-gpg-sign` on the Developer's explicit authorization, noted in that commit. This item tracks the follow-up: is signing a hard requirement going forward, and if so, what's the actual fix?
* Investigation findings:
  1. **The signing mechanism is not owned by this repository at all.** There is no repo-level `.gitconfig`, signing key, or CI secret related to commit signing anywhere in ZonaryOS (checked `.git/config`, `migrations/`, `.github/workflows/ci.yml`, and the whole tree — nothing). Signing is entirely provided by the Claude Code execution environment itself: `git config` shows `commit.gpgsign=true`, `gpg.format=ssh`, and `gpg.ssh.program` pointing at the harness's own signing helper, keyed to a per-session file at `/home/claude/.ssh/commit_signing_key.pub`.
  2. **Root cause of the PR #9 failure**: in whatever session produced that commit, the harness's signing-key file was apparently not yet provisioned/populated for that session — an environment-provisioning gap outside this repository's (or the Developer's) control to fix directly.
  3. **Signing works in the current session**: a throwaway test commit (pushed to a disposable branch, then deleted; the branch `test/gpg-signing-verification` and PR #13 in GitHub may still need manual cleanup) produced a real, well-formed embedded SSH signature (`gpgsig -----BEGIN SSH SIGNATURE-----...`), and its author/committer resolved to a real, distinct GitHub identity (`claude`, account ID 81847) rather than an arbitrary unverified email. Whether GitHub actually renders the green "Verified" badge for it depends on whether that account's registered SSH signing keys include the one the harness signed with — this is Anthropic's account/infrastructure configuration, not something visible or controllable from within this repo or session (no direct GitHub API access was available to check the `verification` field programmatically; the available GitHub MCP tool doesn't surface it).
  4. **No branch protection currently requires signed commits** on `main` (PR #9 itself merged unsigned without being blocked), and no Vision/Rules document ever established signing as a deliberate ZonaryOS policy — it was ambient default behavior from the environment, not a decision anyone made.
* Claude's recommendation (not adopted as policy without Developer sign-off, per this file's own Rule 1): given signing infrastructure is environment-managed rather than repo-managed, already works in-session when properly provisioned, isn't enforced by branch protection, and GitHub's own account-based attribution (commits already resolve to a real, distinct account per session/identity) provides comparable practical assurance for a small, single-team, private-repo project — **don't make signed commits a hard requirement enforced by this repository** (e.g. no "require signed commits" branch protection rule). Keep `--no-gpg-sign` as an allowed, explicitly-logged fallback for the rare case a session's signing key isn't provisioned, same as PR #9, rather than blocking work on infrastructure this project doesn't own.
* Remaining question needing the Developer's actual decision: does the Developer agree with dropping this as a hard requirement, or is there a reason (compliance, a specific external audit expectation, something about the Anthropic account behind these commits) to instead insist on it and escalate the environment-provisioning gap as a harness-side bug report? Claude cannot make this call — it's a policy preference question, not a technical one.

REFERENCE NOTES (no decision required, context only)

* Developer's industry background: Familiar with various third-party ERP/WMS software; has previously written their own WMS/order-management application. (Credibility/experience background for this project.)
* Reference software (mentioned): Various third-party ERP/WMS software — can be used as a reference for scope/price comparison, but the project is not positioned "against a competitor" (see main document §1).
* Project names tried and rejected: Kosmo/KosmoERP (taken), Axon/AxonOS, KernelOS, OmniCore, Modulex/Modulexa, Zetta/ZettaForge, Kozanix, Ontor/OntorCore, Axiomix — ZonaryOS was chosen. Whether the naming-check rule (must not be previously used) will become a general rule for future brand/module naming hasn't been settled — low priority, will come up if/when needed.

This file will shrink as things get discussed. When a new source (another chat transcript, etc.) comes in, it gets processed the same way and added here.
