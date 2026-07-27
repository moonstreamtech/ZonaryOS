# ZONARYOS — WORKING RULES (PROCESS RULES)

> This is not a product/system rules file. It defines how the Developer and Claude discuss this project, how records are kept, and where each piece of information belongs.

---

1. **Four-document system:**
   - `docs/VISION.md` → only decisions the Developer has explicitly finalized.
   - `docs/OPEN_POINTS.md` → everything not yet finalized, not yet detailed, or still to be discussed.
   - `docs/RULES.md` (this file) → process/workflow rules.
   - Development-process notes (tooling, CI discipline) live separately from product vision and are not part of this repo's rule set.

2. **Nothing is written to the vision doc until the Developer finalizes it.** Finalization means the Developer has explicitly approved/decided.

3. **Every undetailed note goes into Open Points.** If the Developer drops a short idea without detail, it is automatically logged as an Open Points item — not written into Vision.

4. **Every clarifying question that comes to mind also goes into Open Points.** When a note is logged, any clarifying questions are recorded as sub-items under that entry. Until answered, the related item does not move to Vision.

5. **No detail is skipped, and each point is explained thoroughly — never abbreviated.** Purpose: avoid having to re-think or re-discuss the same topic during development. An item should carry enough context (why, how, open questions) that it never needs to be revisited from scratch. Brevity belongs in the item title, not the content.

6. **When an item is finalized:** (1) it is added to Vision with the Developer's approval, (2) it is removed from Open Points.

7. **Contradictions are flagged, never silently resolved.** If a new note contradicts an existing Open Point or Vision item, the contradiction is explicitly called out and put to the Developer — Claude does not unilaterally decide which one holds.

8. **When an external source (another AI chat, a document, a link) is shared:** every idea in it is scanned individually; anything not already in Vision goes into Open Points; anything already present is not re-added.

9. **No internal/reference project names appear in `docs/VISION.md` or `docs/OPEN_POINTS.md`.** The Developer may describe a mechanism inspired by prior experience during conversation, but when written into these docs, it is expressed as an original, independent ZonaryOS design — no "this was done in project X" attribution.

10. **Identity privacy:** none of the four documents contain the Developer's real name — always "Developer" instead.

11. **No third-party competitor product names.** Commercial ERP/WMS/accounting products mentioned as competitors/references are not named specifically — use "third-party software" generically instead. Purpose: keep the documents from targeting anyone or creating adversaries. (This does not apply to integration partners — e.g. email providers — mentioned as integration targets, since those aren't competitors.)

---

*This file can also grow — if the Developer wants a new working rule, it gets added here.*
