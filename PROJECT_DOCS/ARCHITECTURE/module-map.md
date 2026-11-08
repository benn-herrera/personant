## Module map

| Path | Responsibility |
|---|---|
| `cmd/` | cobra subcommands: `init`, `index`, `verify`, `archive`, `version`; persistent flags `--home`, `--allow-newer-home`. Every verb that opens the memory store goes through the one `openHome` helper (format check, then Reconcile); `version` is exempt from the format check. |
| `internal/memops/` | the `MemoryOps` port: interface plus the domain types it trades in (`SpineRecord`, `Thread`, `ProjectMeta`, `Config`, the symbol enums, `Normalize`, `DominantSource`). The application layer depends only on this; it imports no internal package. |
| `internal/memops/fileadapter/` | the memory-store adapter: JSONL + markdown + YAML + TOML + go-git on the store directory |
| `internal/store/` | file storage for the memory store: paths, JSONL helpers, init, thread registry and project ops, thread I/O, excerpt bundles, project resolution. Depends on `memops` for the domain types. |
| `internal/autogit/` | go-git-backed automatic git wrapper with bitflag systemic-validation policy |
| `internal/recovery/` | startup reconcile orchestrator over the on-disk observables |
| `internal/crashpoint/` | named kill-point hooks, test-armed and a no-op in production |
| `internal/eventlog/` | plain-text event log, one file per day |
| `internal/dedup/` | content identifiers, write-time diffs, excerpt identifier lines and the composition plan; pure; depends only on go-git `plumbing` |
| `internal/turn/` | the per-delta pipeline, the turn loop, per-turn coalescing (the turn buffer), the staging buffer, recall candidates and the decay-triggered flow for closing a thread |
| `internal/recall/scoring/` | pure Jaccard and cosine recall primitives, store-independent; fine level (per-excerpt chunk vectors), beam descent, and the summary-tree build (a per-thread index for long threads, searched by beam descent) |
| `internal/recall/measure/` | application-side recall stack: `Service`, `Recaller`, the single async indexer and its index snapshot, `.recall-cache` sidecars, the consolidation tree builder |
| `internal/recall/exact/` | the exact lexical (flat-scan) recall level |
| `internal/curator/` | the summarizer's closing-summary drafting (SPEC §3.5, §5.2) |
| `internal/workset/` | context composition into slots (directives, registry, other-projects, active-threads, dormant-threads); a pure renderer |
| `internal/prompt/` | system-prompt template, topic-tag parser, stream filter |
| `internal/model/` | model client surface: OpenAI-compatible `HTTPClient`, scripted and generated `MockClient`, `MockServer` (the mock served as an OpenAI-compatible endpoint), SSE streaming |
| `internal/opencodehook/` | Not in this tree (on `probe-path`): prototype memory service: the loopback handlers the plugin calls, each driving one `internal/turn` phase |
| `opencode-plugin/` | Not in this tree (on `probe-path`): prototype plugin: forwards hooks and events, applies answers, per-RPC timeout and fail-open count |
| `internal/scenarios/` | scenario harness, invariants, metrics; `sim/` the acceptance simulation (the prototype's `opencode/` driver and executor is on `probe-path`) |
| `internal/{metrics,clock,index,verify,version,log,testsupport}/` | supporting subsystems: metrics emission, the single clock, derived-file rebuild, structural validation, version identity, logging, live-endpoint test helpers |
| `justfile` | build, test and dependency-vetting recipes; the target inventory lives there |

**Open:** SPEC §4.1 keeps the `search`, `deps` and `log` verbs; `cmd/` has none of them.

---

