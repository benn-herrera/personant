#### Anchors vs. history (the evolving-anchor model)

`history_symbols` is the canonical, accreting,
weighted per-thread set (capped at 40 by default) and the recall match surface. `anchors` is **not**
a separate storage tier — it is the deterministic top-`AnchorProjectionMax` (computed by
`ProjectAnchors`) **projection** of the thread's *active* history symbols, re-derived each owner
turn. `memops.AnchorProjectionMax` is a compiled constant (8), not a config key: the SPEC §2.6.1
`anchor.projection-max` directive is not read. A symbol that falls out of the projection flips to
**superseded** but is **retained, not evicted** — an abandoned premise stays a findable recall
handle, protected from capacity eviction by its `ever_central` latch. 0 anchors is legal (a
vague-start thread that hasn't accreted a headline yet). (Spec §3.4; "Symbol lifecycle and anchor
projection".)

