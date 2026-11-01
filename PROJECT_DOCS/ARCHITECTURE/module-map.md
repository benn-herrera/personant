## Module map

| Path | Responsibility |
|---|---|
| `cmd/` | cobra subcommands: `init`, `index`, `verify`, `archive`, `version`; persistent flags `--home`, `--allow-newer-home`. Every verb that opens the substrate goes through the one `openHome` helper (format gate, then Reconcile); `version` is ungated and never fails on a broken home. `chat`, `ping`, `models`: see the superseded-code note at the top. |
| `internal/memops/` | the `MemoryOps` port: interface plus the domain types it trades in (`SpineRecord`, `Thread`, `ProjectMeta`, `Config`, the symbol enums, `Normalize`, `DominantSource`). The application layer depends only on this; it imports no internal package. |
| `internal/memops/fileadapter/` | the substrate-backed adapter: JSONL + markdown + YAML + TOML + go-git on `~/.personant/` |
| `internal/store/` | file substrate: paths, JSONL helpers, init, spine and project ops, thread I/O, bootstrap. Depends on `memops` for the domain types. |
| `internal/autogit/` | go-git-backed autonomic git wrapper with bitflag systemic-validation policy |
| `internal/recovery/` | startup reconcile orchestrator over the on-disk observables |
| `internal/crashpoint/` | named kill-point hooks, test-armed and a no-op in production |
| `internal/eventlog/` | daily-rotated plain-text event log |
| `internal/dedup/` | reverse-delta content-versioning engine |
| `internal/turn/` | the §3.0 chain, the turn loop, per-turn coalescing, the staging buffer, the recall surface, the decay-triggered closure flow, and the phase entry points (`probe_phases.go`) |
| `internal/recall/scoring/` | pure Jaccard and cosine recall primitives, substrate-free; chunk tier, beam descent, summary-tree build |
| `internal/recall/measure/` | application-side recall stack: `Service`, `Recaller`, the single async indexer and its snapshot, `.recall-cache` sidecars, sleep-cycle tree builder |
| `internal/recall/exact/` | the exact lexical recall tier |
| `internal/curator/` | closure-summary drafting (SPEC §3.5, §5.2) |
| `internal/workset/` | layered context composition (E/A1/A2/B/C); a pure renderer |
| `internal/prompt/` | system-prompt template, topic-tag parser, stream filter |
| `internal/model/` | LLM client surface: OpenAI-compatible `HTTPClient`, scripted and generated `MockClient`, `MockServer` (the mock served as an OpenAI-compatible endpoint), SSE streaming |
| `internal/opencodehook/` | probe memory service: the loopback handlers the plugin calls, each driving one `internal/turn` phase |
| `opencode-plugin/` | probe plugin: forwards hooks and events, applies answers, per-RPC timeout and fallback count |
| `internal/scenarios/` | scenario harness, invariants, metrics; `sim/` the acceptance simulation; `opencode/` the opencode driver and executor |
| `internal/{metrics,clock,index,verify,version,log,testsupport}/` | supporting subsystems: metrics emission, the single clock, derived-file rebuild, structural validation, version identity, logging, live-endpoint test helpers |
| `justfile` | build, test and dependency-vetting recipes; the target inventory lives there |

**Open:** SPEC §4.1 keeps the `search`, `deps` and `log` verbs; `cmd/` has none of them.

---

