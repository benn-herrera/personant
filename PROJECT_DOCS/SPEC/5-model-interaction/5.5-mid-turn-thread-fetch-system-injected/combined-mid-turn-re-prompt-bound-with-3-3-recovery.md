#### Combined mid-turn re-prompt bound (with [§3.3](../../3-core-algorithms/3.3-symbol-extraction-three-passes/index.md) recovery)

Re-prompts are capped at **1 per
cause**, and there are exactly three causes — missing-thread (this section), missing-tag ([§3.3](../../3-core-algorithms/3.3-symbol-extraction-three-passes/index.md)), and
empty-response ([§3.3](../../3-core-algorithms/3.3-symbol-extraction-three-passes/index.md)) — so a turn issues at most 3 re-prompts (≤ 4 model streams). A second miss for
the same cause falls through to that cause's deterministic close-time fallback (LRU pickup here;
owner-default for missing-tag; accept-the-empty for empty-response). The empty-response cause
subsumes missing-tag for a persistent-empty response ([§3.3](../../3-core-algorithms/3.3-symbol-extraction-three-passes/index.md)): the two reminders never both fire
against emptiness, with one exception — an all-whitespace response longer than the leading scan
window ([§5.1.2](../5.1-topic-tag-emission-format/5.1.2-parser.md)) counts as tag-less rather than empty, so it fires the missing-tag reminder instead
of the empty-response one. A fetch costs at most one aborted stream
plus one full stream; turns that trigger no cause pay nothing. Speculative pre-fetch is a non-goal
(ARCHITECTURE "No speculative prefetch").

---

