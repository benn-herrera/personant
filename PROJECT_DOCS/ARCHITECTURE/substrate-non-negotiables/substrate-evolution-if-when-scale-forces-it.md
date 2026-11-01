### Substrate evolution: if/when scale forces it

The substrate non-negotiables above are constraints on the *canonical* layer — the source of truth
that humans inspect, git tracks, and `cat`/`grep` operate on. They do **not** preclude a fast
read-optimized **derived index** alongside the canonical, accessed through the same `MemoryOps` port
(an additional adapter or an internal optimization within the file adapter).

If the markdown/JSONL canonical substrate ever hits a critical speed ceiling — most plausibly under
long-rung simulation acceleration (especially inference-/embedding-in-loop runs), not interactive
use — the option to reach for is **[bbolt](https://github.com/etcd-io/bbolt)** as a derived KV
index, with the markdown-graph remaining canonical. Choice: bbolt (read-heavy B+ tree, pure Go,
etcd-proven), rebuildable via `MemoryOps.RegenerateDerivedState` and drift-checked like any derived
file. Rejected: Pebble (write-optimized), LMDB (CGo); SQLite only if queries become SQL-shaped.

This is **not** a planned upgrade. The asymmetric-cost discipline says: wait for simulation data
that shows substrate I/O dominates the sim's wall-clock per simulated month. The `MemoryOps` port
makes the eventual decision local to one adapter.

