#### Anchors and symbol history

`history_symbols` is the authoritative, accreting, weighted per-thread symbol history (capped at 40
by default) and the recall match set. `anchors` is the
deterministic top-`AnchorProjectionMax` (computed by `ProjectAnchors`) **projection** of the
thread's *retained* history symbols, re-derived each owner turn and stored in the thread registry record and frontmatter. `memops.AnchorProjectionMax` is a
compiled constant (8), not a config key: the SPEC §2.6.1 `anchor.projection-max` directive is not
read. A symbol that falls out of the projection flips to **superseded** but is **retained** — an abandoned premise stays a findable recall handle, protected from capacity trimming
by its `ever_central` latch while the protected set fits the cap. 0 anchors is legal (a vague-start thread that hasn't accreted a
headline yet). (Spec §3.4; "Symbol lifecycle and anchor projection".)

