#### Tool output in composed requests

Tool output is bounded so it cannot exhaust the 15% live-turn
budget:

- A hard byte cap on tool-result inclusion in the composed request: 15% of the live-turn reserve (`live_turn`, [§2.6.1](../../2-data-model/2.6-directive-file-format-directives-md/2.6.1-recognized-parameters.md)).
  Above the cap: head + tail with an ellipsis notice. A truncation always carries its notice.
- The cap is a **task-class** bound: it applies to `tool.result` and to `user.shell-capture`. It
  does **not** apply to the `fs.read`/`fs.write`/`fs.commit` deltas, whose content is the file body
  written to the per-thread tracked-file store ([§3.9](../3.9-working-set-content-dedup/index.md)).
- The cap is applied to the **delta**: the bounded content is what the composed request carries and
  what the working window holds. The full body is not persisted ([§3.10.6](../3.10-transient-data-lifecycle/3.10.6-what-stays-transient.md)).
- The assistant can re-fetch with a narrower query or a byte range if it needs more.

**LRU update rule** (driven by the [§3.0.4](../3.0-all-content-enters-through-one-pipeline-deltas/3.0.4-turn-boundaries-and-per-turn-engagement-coalescing.md) turn-close path). After a turn's engagements commit, each
thread it engaged moves to the front of the active set, most recently engaged first
(**active-set order**). A thread pushed past `layer.b-top-k` moves to the front of the dormant set.

Active-set order and the dormant set persist across a relaunch (`working-set.json`, [§2.1](../../2-data-model/2.1-storage-layout/index.md));
active-set order is the recency signal [§3.3](../3.3-symbol-extraction-two-passes/index.md) owner-default uses. The dormant set is **not
count-capped**: the dormant-threads slot's byte budget truncates it at composition time, and its
truncation notice is the overflow indicator.

