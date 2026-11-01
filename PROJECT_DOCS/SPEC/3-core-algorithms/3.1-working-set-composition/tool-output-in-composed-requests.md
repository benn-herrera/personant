#### Tool output in composed requests

Tool output is bounded so it cannot exhaust the 15% live-turn
budget:

- A hard byte cap on tool-result inclusion in the composed request (default 8 KB; configurable).
  Above the cap: head + tail with an ellipsis marker. A truncation always carries its marker.
- The cap is a **task-class** bound: it applies to `tool.result` and to `user.shell-capture`. It
  does **not** apply to the `fs.read`/`fs.write`/`fs.commit` deltas, whose content is the file body
  written to the per-thread tracked-file store ([§3.9](../3.9-working-set-content-dedup/index.md)).
- The cap is applied to the **delta**: the bounded content is what the composed request carries and
  what the working window holds. The full body is not persisted ([§3.10.6](../3.10-transient-data-lifecycle/3.10.6-what-stays-transient.md)).
- The agent can re-fetch with a narrower query or a byte range if it needs more.

**LRU update rule** (driven by the [§3.0.4](../3.0-context-modification-events-the-architectural-primitive/3.0.4-per-turn-coalescing-for-engagement.md) turn-close path). After per-turn engagements commit, for
each engaged thread id:
- Remove it from both the Layer B active set and the dormant set if present.
- Place it at the front of the Layer B active set. **Layer B order** is that sequence, most recently
  engaged first.
- If the active set exceeds `layer.b-top-k`, demote the tail to the front of the dormant set.

Layer B order and the dormant set persist across a relaunch (`working-set.json`, [§2.1](../../2-data-model/2.1-storage-layout/index.md)); Layer B
order is the recency signal [§3.3](../3.3-symbol-extraction-three-passes/index.md) owner-default uses. The dormant set is **not count-capped**: Layer
C's byte budget truncates it at composition time, and its truncation marker is the overflow
indicator.

