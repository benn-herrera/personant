### Terms

- **Excerpt**: one turn-excerpt file `threads/thr_<n>/turns/<name>` with `<name>` matching the
  `<digits>.md` pattern the excerpt listing accepts. Its turn number is the parsed `<digits>`.
- **Folio**: the per-thread cold tier, `threads/thr_<n>/folio.jsonl`. The name is distinct from
  "digest" (Layer A2 project summaries), from git "pack"/"repack" (the gc path in `autogit` and
  `Consolidate`) and from "archive" (SPEC §3.8 deep-cold archival). Use "folio" in code, docs and
  events; do not call it a pack.
- **Bind**: move loose excerpts into the folio. **Unbind**: move folio entries back to loose files.
  **Settle**: one pass that binds and unbinds a thread until it matches the placement rule.
- **Hot tail H**: `store.HotExcerptTail = store.ThreadTurnWindow + memops.EmbeddingDebtCap` (512 +
  16 = 528).

