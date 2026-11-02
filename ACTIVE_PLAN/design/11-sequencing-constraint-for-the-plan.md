## 11. Sequencing constraint for the plan

There is no dual-write stage. The new path is proven by its own acceptance criteria and the
[§13](13-measurement-the-a11a-method.md) measurement. These steps run in order:

1. **Build the new store.** `dedup` primitives, the store format and its verify check land beside
   the old mechanism, which keeps running.
2. **Switch to it.** Turn close writes identifier lines and calls `RecordFileEvents`; composition
   renders in-place expansions and no longer renders the tracked-files section.
   - Evidence for this step: [§12](12-acceptance-criteria.md) criteria 1 and 10, observed in a
     compose test, plus a short mock sim run whose expansion report ([§6](6-composition.md) step 5)
     counts at least one rendered literal.
3. **Remove the old mechanism.** Precondition: step 2's evidence. Land B1c and the format 2 → 3
   migration, and remove `files.json`, `Chain` / `LiveWindow`, the tracked-files section's types,
   reachability-gated aging, the recovery op and the workspace-git helpers.
4. **Measure** ([§13](13-measurement-the-a11a-method.md)).

---
