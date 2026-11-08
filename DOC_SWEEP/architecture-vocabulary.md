# ARCHITECTURE vocabulary inventory

Line numbers are `DOC_SWEEP/ARCHITECTURE.md` at `3fb2084` unless marked SPEC. The inventory is by
adjagent:tech-writer; what is settled and what is open is under "Rulings" at the end. The settled
vocabulary is THESIS plus `DOC_SWEEP/spec-vocabulary.md` (Rulings and Apply pass); code
identifiers in backticks keep their names.

## 1. Divergences from the settled vocabulary

Each item gives a settled term, the variants ARCHITECTURE prose uses instead, and their lines.

### memory store, store directory, format revision, authoritative, store working tree

- **memory store or store directory**
  - "home" for the store, its directory or its repository: 294; 404 ("home repository"); 408, 409, 420 ("home tree"); 421; 536; 543 (`$PERSONANT_HOME`); 578; 693; 956; 1006 and 1135 (`<home>/…`); 1275; 1310–1312 ("legacy home", "current-format home"); 1583; 1595; 1646. Heading "Home layout" (540).
  - `~/.personant/` used as the store's name, where "store directory" should be used and the default stated once: 332, 370, 398, 404, 408, 416, 421, 470, 492.
  - "substrate" for the store: 330, 332, 333, 399, 470, 478–479, 483, 494–529, 610, 1234, 1356–1365, 1428, 1478, 1539, 1635, 1686, 1706.
  - Other senses of "substrate": at 1595 it means the memory service's semver; at 340 and 474 "substrate-free" means pure and store-independent.
- **format revision**: "home format" at 561, 1275, 1310–1316. `home-format=` at 1583 and 1595 is code.
- **authoritative**: "canonical" in the source-of-truth sense at 361, 370, 380, 491, 496–504, 544, 553, 561, 569, 577 (table row), 578, 632, 695, 756, 766, 1073, 1243, 1249, 1390, 1498, 1683, 1694, 1716, 1740.
- **store working tree**: "worktree" or "working tree" at 1176–1177, 1243–1244, 1247, 1255, 1693.

### memory service, host

- **memory service**
  - "the runtime" at 237, 1004, 1172, 1501, 1512–1516, 1569, 1598, 1691.
  - "memory" as the actor at 408.
- **host**: "Harness (opencode)" at 372. Elsewhere, "harness" is the test harness.
- **the system as a whole**: "context-management … system" at 11, against THESIS's "memory system for a coding agent".

### model, summarizer, clusterer

- **model**: "LLM" at 44, 361, 365–366, 371, 380, 394, 396, 743, 856, 869, 1066, 1190, 1241, 1355, 1379, 1383, 1451, 1683, 1687, 1690, 1740, 1742. Heading at 394.
- **summarizer**: "curator" at 383, 744, 853, 1025, 1066, 1068, 1158, 1341, 1351. Code names stay.
- **clusterer**: "dissector" at 371, 1186, 1190, 1341. Code names stay, and so does the heading "Fallback dissection".

### thread, thread registry, owner turn, unknown thread id, provenance

- **thread registry**: "spine" at 384, 400, 440, 475, 517, 577, 581 (heading), 695, 890, 1008, 1019, 1022, 1043, 1045, 1053, 1057, 1102, 1162, 1172, 1191, 1201, 1323. Code names stay.
- **display line**: "recognition entry" and "recognition surface" at 219–220, 999, 1051, 1057.
- **thread**: "topic" on the user surface at 118, 440, 1102–1103. "Topic tag", `/topic*` and `*new-topic*` stay.
- **owner turn**: "primary-engagement turn" at 634.
- **unknown thread id**: "phantom" at 1007.
- **provenance / derived-from**: "lineage" at 167, 747.
- **Thread stages** (1037–1046) should restate in SPEC §2.2.1 terms:
  - created;
  - active, in the active set;
  - dormant, in the dormant set;
  - closed;
  - archived, in cold storage.
  
  "Retired" and "retirement" follow SPEC, where they are unruled.

### Slots

- **Named slots** (directives, registry, other-projects, active-threads, dormant-threads)
  - "Layer E/A1/A2/B/C" at 164, 344, 385, 441, 554, 567, 703, 809, 821, 858, 909, 973, 975–976, 979, 984, 999, 1004, 1009–1011, 1041–1043, 1113, 1163, 1201–1202, 1371, 1501.
  - `LayerB` at 680.
  - "layer" meaning a slot at 331, 963, 970, 972, 1612.
- **unused-share rule and unused-share reallocation**: "underusage" at 964 and 1028; "the lending rule" at 964.
- **truncation notice**: "overflow marker" at 973.
- **stand-in line**: "Evicted content (the thunk)" at 217.

### delta, per-delta pipeline, turn buffer, expiry

- **delta**: "context-modification event" at 898, 1026.
- **per-delta pipeline**: "hook chain", "the §3.0 chain" or "chain" at 137, 339, 896, 900, 912, 926, 1026, 1742.
- **turn buffer**: "coalesce buffer" at 943, 946, 1092.
- **expiry**: "Window-close GC", "staging GC" or "evict" at 243, 247, 949.

### Closure, ack, drain, fallback, restore, cold storage, recall

- **ack question**
  - "ack channel" at 130.
  - "prompt" meaning a question at 130–131, 1111, 1112, 1417, 1421.
- **drain point**: "boundary drain" or "exception drain boundary" at 1166, 1168, 1599.
- **fallback**
  - "recovery reminders" at 990, 1090.
  - "missing-tag recovery", "tag recovery" or "empty-response recovery" at 1096, 1608.
- **resumed turn**: "recovery turn" at 261, 263, 284, 286.
- **restore**: "Recovery (RecoverThread …)", "archived-recoverable" or "recoverable by explicit fetch" at 1045, 1179, 1540.
- **cold storage**: "deep cold" or "Deep cold archival" at 569, 1045, 1170.
- **loaded (into the active set)**: "promoted into Layer B by recall" at 858.
- **recall candidates**: "recall surface" at 339; "recall-surfaced working set" at 858.
- **level / signal**: "tier" at 340, 342, 1107 (meaning signal), 1116, 1121, 1531.
- **Recall-level terms SPEC already fixed**
  - **flat-scan level**: "flat scan" at 1119–1121.
  - **completeness floor**: "lexical floor" at 1130.
  - **unbacklogged-embedding limit**: "embedding-debt cap" or "embedding-debt window" at 698, 1124, 1131.

### Day cycle and durability

- **turn repository / history repository**
  - "daily DB", "primary DB", "git DBs", bare "daily" or "primary" at 402, 416, 566, 571–572, 579, 720–722, 1177, 1233, 1244, 1247, 1252–1258, 1274–1275, 1291, 1300, 1306–1308, 1602, 1693–1694.
  - "dual-repo" at 399, 688.
- **day rollover / a named rollover step**: "barrier" or "day barrier" at 243, 688, 708, 710, 714, 717, 721, 1254, 1268, 1276, 1279, 1293, 1560, 1563, 1565.
- **Rollover step names, replacing B1, B1b, B1c, B2, B5 and B6**: these codes appear at 688, 717, 1276, 1279–1280, 1290, 1293, 1298–1299, 1601–1602.
- **case**: "cell" at 1253, 1269, 1601, 1693.
- **ignore-block rule**: "INV-6" at 579.
- **turn-repository init**: "morning-init" at 1561, 1601.
- **deleted and recreated each day**: "reborn daily" at 572, 1602.
- **format revision**: "stamp" at 1313, 1315.
- **consolidation**: "sleep cycle" at 341, 1119, 1318 (heading), 1550, 1673.

### Feedback directive tuning, windows, session commands, project resolution

- **feedback directive tuning**: "accrual", "accrued" or "directive accrual" at 557, 1137–1138, 1199, 1206 (heading), 1208, 1211, 1220–1221, 1718.
- **recent-excerpt window**: "assembly window" or "FIFO assembly window" at 678, 680, 698, 738, 1117, 1445.
- **provisional value**: "calibration window" at 1725.
- **session commands**: "Memory-native commands" at 120, 125.
- **project resolution**: "Bootstrap waterfall" at 117 and 1407; "bootstrap" at 333.
- **remote identity**: "External canonical identity" or "canonical external identity" at 795, 1021, 1404, 1421.
- **identity levels**: "Three-layer identity" and the "Layer" column at 1021, 1401.
- **span / run configuration**: "rung" at 502, 1459, 1483, 1525, 1532–1533, 1543, 1565, 1666.

### Check, criterion, automatic, service run, transcript tail

- **check / acceptance criterion / metric requirement**: "gate" at 330, 402, 1290, 1299, 1595, 1434, 1461, 1666, 152, 1501, 1655.
- **automatic**: "autonomic" at 127, 334, 370, 394 (heading), 399, 492, 1364, 1687.
- **service run**: "session" in that sense at 970 and 1008 (1393 already notes it). "Relaunch" or "restart" at 296, 298, 1006, 1010 is low priority, since SPEC still uses "relaunch".
- **transcript tail**: "bounded history tail" or "history" at 87, 247, 296, 299.

### Figurative names and codes

- **Figurative names**: "thunk" (217), "silent lie" (1488), "canary" and "tripwire" (1495), "the service owns its house" (492), "recall-madlibs" (1524), "dead zone" (1123, 1533), "pollution" and "noise" for transient symbols (937, 953, 955). "A session is a window into the one memory" (117) should read "a session reads and writes the one memory". "Forensic" is defined at 1579 and stays.
- **Codes in prose**:
  - open-item ids U3–U10;
  - research-report shorthand IO, PP, PR, RP, CI, SS (22–25 onward) and the finding codes inside them (A9, A17, M9, M19, T1–T4);
  - roadmap ids A2, A3, A5;
  - issue references #94, #109, #111, #123, #127;
  - finding codes W1, W8.

## 2. ARCHITECTURE-only concepts

| Concept | Proposed term | Meaning | Variants (lines) | Narrows | Stock or coinage |
|---|---|---|---|---|---|
| Where a thread's turn excerpts sit | excerpt storage tiers: loose tier, bundled tier | Individual files under `turns/` vs the thread's `folio.jsonl` | "warm tier" / "cold tier" (634, 637), "Excerpt tiers" (686), "bound" / "unbound" (699–714, 1602), "hot tail" (698, 708), "loose" (697–702, 1283) | turn excerpt; distinct from THESIS cold storage | "loose" stock; "cold tier" collides with cold storage |
| The file holding the bundled tier | folio, or excerpt bundle | `folio.jsonl`, one line per bundled excerpt | 333, 549, 636–637, 686, 690, 699–711, 1283–1291, 1301, 1313, 1602 | turn excerpt | "folio" is a coinage; "excerpt bundle" stock |
| Moving excerpts between tiers | settle (in the excerpt-storage step) | Convergent tier move during the rollover | 688, 1280–1288 | rollover steps | mild coinage; code `store.SettleExcerptTiers` |
| The background embedding indexer | indexer, index snapshot | One goroutine building vectors and swapping an `atomic.Pointer` | 341, 1127–1135 | SPEC §3.4 | stock |
| Indexer progress guard | embed sequence guard | Stops a stale in-flight embed overwriting a fresher vector | "per-thread turn-count watermark" (1128) | — | collides with SPEC's watermark |
| Pending flushes | backlog, flush job | Enqueued, unpublished index flushes | 1131–1133 | SPEC unembedded backlog limit | stock |
| Per-thread summary hierarchy | summary tree | Beam-descent index for long threads | 340, 341, 1118–1121, 1134–1135 | SPEC §3.4 | stock; undefined in both volumes |
| The four ways a thread comes forward | recall paths | Model recognition, opportunistic surfacing, cross-project, intra-thread | "Recall mechanisms (four, layered by cost)" (1100), 1144, 1203 | SPEC signals (another axis) | stock |
| How the model recognizes threads | display line recognition | The registry slot's display lines are enough for the model to recognize a thread | 219–220, 999, 1051 | SPEC display line, registry slot | stock |
| opencode's checkpoint compaction | checkpoint compaction | Fixed-placeholder compaction scheduled to bound opencode's history read | 94–102, 208, 297, 307 | SPEC §1.1, §1.7 | stock-ish |
| Hook-driven entry points | turn phases | BeginTurn … AbortTurn | 234, 250 | per-delta pipeline | stock |
| The throwaway implementation | the prototype | Code on branch `probe-path` | "the probe" (23–35, 72, 98, 154, 157–158, 176, 188, 250, 262, 1625–1661) | — | "probe" collides with measurement probes |
| Tool-call ordering | call order | Order a response's tool calls are applied | 173–179 | SPEC §1.5 | stock |
| Harness machinery | turn executor, step, step source, scenario | Test-side types | 1473–1475, 1503–1515, 1638–1642 | SPEC §9.2 | stock |
| Test model of active-set membership | active-set oracle | The generator's model, cross-checked each step | "Layer-B shadow", "shadow oracle", "runtime-mirror" (1445, 1501) | SPEC §9 | "shadow" is a coinage |
| Metric watching a ratio | indicator metric | — | "canary" (1495, 1599) | SPEC §9.4 | stock fit for a figure |
| Recovery constructs | recovery scope, scoped restore, quarantine | Scoped revert of paths; byte-exact preservation | 1270, 1595 | SPEC §4.5.8 | "recovery scope" defined only by use |
| Format migration | format migration, migration table | Steps from older format revisions | 1309–1316 | format revision | stock |
| Evolving anchor model | drop the label | Anchors are a projection of active symbol history | 1071, 1546 | SPEC §2.7.4 | coinage; SPEC says "projection" |
| A gate that passes vacuously | seam failure | Renamed metric key, drifted log line, second clock path | "silent lie" (1488), 1433 | — | coinage, defined by example |

## 3. Collisions specific to ARCHITECTURE

1. **snapshot.**
   - Meanings: pre-turn rollback (243, 248); the index snapshot (341, 1127); the pre-crash baseline (1497).
   - Split: pre-turn snapshot; index snapshot; pre-crash baseline.
2. **watermark.**
   - Meanings: the turn-repository HEAD (566, 1595), which is SPEC's sense; the indexer's guard (1128).
   - Split: keep watermark for the HEAD; embed sequence guard for the indexer.
3. **checkpoint.**
   - Meanings: opencode checkpoint compaction (94–105, 208, 297, 307); a turn-repository commit (1308); "30-minute checkpoint budget" (1564).
   - Split: checkpoint compaction; checkpoint commit; check interval.
4. **fallback.**
   - Meanings: the plugin's fail-open path, counted as "fallback count" (72–73, 348, 1656); the Go server port as an alternative route (58, 115); owner fallback (1007); fallback dissection (1184); the re-prompt fallback, which is SPEC's sense.
   - Split: fail-open count; alternative route; owner default; keep "fallback dissection" (SPEC title); "fallback" for the re-prompt.
5. **probe.**
   - Meanings: the prototype (23–35); measurement probes (1473, 1500, 1515, 1533); `probe-*` recipes (1658).
   - Split: prototype; measurement probe.
6. **oracle.**
   - Meanings: the model (442, 1104); the test oracle (1444, 1497, 1501, 1562).
   - Split: "the model recognizes"; oracle for tests only.
7. **accrual.**
   - Meanings: directive accrual, which is ruled; "lifelong accrual" (1199).
   - Split: "career" and "accumulates" at 1199.
8. **tier.**
   - Meanings: excerpt storage; the "Tier / Role" table (368); recall levels and signals (340, 342, 1107, 1116, 1121, 1531); "storage tier" (1075); "ranking tier" (882); ROADMAP tiers (1672).
   - Split: "tier" for excerpt storage only; "Component / Role"; level or signal per SPEC; "horizons" for ROADMAP.
9. **history.**
   - Meanings: symbol history (655, 768, 1023); transcript tail (87, 296, 299); opencode history rows (205, 208); the `oc_*` message count (1656); the history repository.
   - Split: symbol history; transcript tail; opencode message rows; history repository.
10. **display name.**
    - Meanings: a thread's name (625–628, 744); a project's name (795, 826, 1405).
    - Split: working name for a thread, as SPEC 221 has it; display name for a project.
11. **recognition surface / surface / entry.**
    - Meanings: registry display lines (219–220, 999, 1051–1057); user interface (1440); eval and tuning scope (1351, 1362, 1385); recall candidates (339).
    - Split: display line; interface or scope.
12. **layer.**
    - Meanings: slot; "application layer" (331, 466), which is stock; "canonical layer" (496); "directive layer" (970); "layered context composition" (344); test layers (1448); "precision layer" (685, 1324).
    - Split: slot; application code; authoritative files; directive precedence; levels; stage.
13. **sweep.**
    - Meanings: opencode's boot sweep (283); full staging pass (1177, 1309); tmp residue (1289); parameter sweeps (1455, 1716).
    - Split as SPEC: full staging pass; debris cleanup; parameter sweep.
14. **drain.**
    - Meanings: the exception drain at a drain point (1162, 1166, 1599); the archival batch, "B1's drain" (1280, 1282).
    - Split: drain point; archival batch.
15. **marker.**
    - Meanings: operation marker (565, 1252, 1268, 1290, 1305, 1693–1694); overflow marker (973); log marker (1434, 1516); the consolidation "marker step" (1550).
    - Split: operation marker; truncation notice; log line name; consolidation step.
16. **primary.**
    - Meanings: the history repository; opencode's "primary step" (83); `remote_urls[0]` (804).
    - Split: after the rename, the last two are standard senses.
17. **eviction.**
    - Meanings: dropped from the request (217, 975–976); a thread leaving the active or dormant set (859–861, 1163); the symbol history cap (762, 769–774, 874, 882–891, 1023, 1079–1080); staging (949); bump-eviction (920); merge → project → evict (890).
    - Split: evicted from the request; demoted; trimmed; expired; budget demotion; trim.
18. **bind / bound.**
    - Meanings: the folio bind; "bound" meaning a limit (87, 100, 1093, 1123, 1161).
    - Split: bundle; limit or capped.
19. **registry.**
    - Meanings: the thread registry; `homeFormatMigrations` (1312); crash-scenario and metric-key registries (1479, 1497).
    - Split: "table" for the code-side registries.
20. **U4.**
    - Meanings: used for several questions (125, 130, 1111, 1166, 1416).
    - Split: if the ids are kept, one id per question.

## 4. Headings with a retired or variant term

| Line | Current | Proposed | Cited by title |
|---|---|---|---|
| 234 | Runtime inversion | Hook-driven turn phases | none; 269 "Invariant for the production inversion" → "… phase split" |
| 357 | Applying the thesis (load-bearing) | Applying the thesis | none |
| 368 (table header) | Tier / Role | Component / Role | none |
| 394 | Autonomic vs LLM-controlled | Automatic vs model-controlled | ARCH 43–44 |
| 412 | Single spelunking location | One inspection location | ARCH 113, 295 |
| 466 | Port-and-adapter at the substrate boundary | … at the memory store boundary | none |
| 483 | Substrate non-negotiables | Storage non-negotiables | ARCH 1706 |
| 494 | Substrate evolution: if/when scale forces it | Storage evolution: … | none |
| 540 | Home layout | Store directory layout | none |
| 581 | Spine record (`spine.jsonl`) | Thread registry record (`spine.jsonl`) | ARCH 1019 (table) |
| 686 (label) | Excerpt tiers | Excerpt storage tiers | ARCH 637 |
| 678 (label) | Assembly window vs retention | Recent-excerpt window vs retention | spec-vocabulary.md only |
| 896 | The §3.0 hook chain (load-bearing primitive) | The per-delta pipeline (§3.0) | ARCH 137 |
| 961 | The layered working set | The composed request and its slots | ARCH 1234 |
| 1002 | Names and recency state | Active-set and dormant-set order | none |
| 1051 | Spine entries are the recognition surface | Display lines are how the model recognizes threads | SPEC 68; ARCH 219 |
| 1071 | Anchors vs. history (the evolving-anchor model) | Anchors and symbol history | none |
| 1100 | Recall mechanisms (four, layered by cost) | Recall paths (four, ordered by cost) | ARCH 1203 |
| 1199 | Multi-project as lifelong accrual | Many projects, one career | none |
| 1206 | Personalization through directive accrual | Personalization through feedback directive tuning | ARCH 1138, 1718 |
| 1238 | Crash stability and the dual-repo substrate | Crash recovery and the turn and history repositories | ARCH 399, 1234 |
| 1318 | Memory consolidation — the "sleep" cycle | Memory consolidation | SPEC 1296; ARCH 1324 |
| 1327 | Serving deployment | Model access and inference telemetry | ARCH 1611 |
| 1347 | Model-family as platform | (owner call) | ARCH 1380 (anchor link) |
| 1360 | Architectural mechanism | Isolating the model-family surface | none |
| 1367 | Two context figures | Token ceiling and model context window | none |
| 1374 | Minimize infrastructural prompts | Minimize memory prompts | none |
| 1399 | Project identity (dual-layered) | Project identity | ARCH 117 |
| 1425 | Testing as the lab bench | Testing as the measurement instrument | SPEC 2387 |

Unchanged, cited by SPEC by title: "Plugin route", "Storage and record formats", "Symbol
normalization", "Symbol source", "Symbol lifecycle and anchor projection", "Event log format",
"No speculative prefetch".

## 5. Seen, other kinds (held for later passes)

1. 11–16: the opening's "compaction and summarization" and "sub-mind split and re-merge" are undefined, and they are not mechanism.
2. 1341–1345: the U5 text about reaching "its own models" conflicts with THESIS's "the memory service never calls a model".
3. 1733: "anchor selection quality at retirement" contradicts 1068.
4. 1399 says "dual-layered", while 794, 1021 and 1401 describe three levels.
5. 1102–1114, 1137: user-facing recall behavior that belongs in SPEC §3.4 and §4.3.
6. 1583–1621: event-table meanings overlap SPEC §2.8.
7. 353: SPEC §4.1's `search`, `deps` and `log` are missing from `cmd/`.
8. 1706: the document map is stale for the monolith working copy.
9. 1352: an environment-specific tuning target (Gemma via `reaper`).

## Rulings

### Settled by the SPEC rulings: apply without a further call

- Section 1 in full: each divergence takes the settled term. Code identifiers keep their names.
- Section 3 splits 1–19, by principle 4.
- Section 4 headings, by principle 3. Each rename updates every title citation listed for it,
  in SPEC and in ARCHITECTURE.
- Section 2 terms marked stock.
- By principle 3: "folio" → excerpt bundle, "bound" / "unbound" → bundled / loose tier.
- By principle 4: "the probe" → the prototype (the branch's code), "measurement probe" for the rest.
- "gate" stays where it is the dev-process term (edit gate, checkpoint gate); elsewhere it becomes
  check, acceptance criterion or metric requirement.

### Owner calls

| Current | Ruled | Applies to |
|---|---|---|
| retire, retirement, closure (ending a thread) | close / closing / closed; "close-out" only where a noun is unavoidable; never "closure", which reads as the programming term | SPEC and ARCHITECTURE. SPEC §3.5 heading "Closure flow" → "Closing a thread", with its title citations. The summarizer "drafts a thread's closing summary". Backticked names (`closure.ack-mode`, `retire.*` events, `RetireThread`) stay. |
| U3–U10 open-item ids | kept | ROADMAP "Pending decisions" is keyed on them. |
| Research-report shorthand IO, PP, PR, RP, CI, SS and the finding codes inside them | the report named in prose (e.g. "the integration-options report"), with its path once | ARCHITECTURE "Plugin route" and everywhere the shorthand appears. |
| Issue references (#94, #109, #111, #123, #127) and finding codes (W1, W8) | dropped | History lives in git. |
