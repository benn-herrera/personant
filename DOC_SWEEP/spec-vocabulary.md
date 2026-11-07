# SPEC vocabulary inventory

Input to the owner's ruling. Line numbers are `DOC_SWEEP/SPEC.md` at commit `022282b`. Proposed
terms are the reviewer's (adjagent:tech-writer); none is ruled. Rulings are recorded under
"Rulings" at the end; SPEC changes only after them.

## 1. Inventory

### Actors and the store

| Concept | Proposed term | Meaning | Variants now (lines) | THESIS | S/C |
|---|---|---|---|---|---|
| The running process that owns memory | memory service | The single-writer local process that composes requests and writes memory. | "Personant" as actor (13); "memory" as actor (75, 1884); "memory-service process" (26); "the runtime" (613, 634, 1564, 2141); "the service" (2087); "the substrate" as actor (1658) | The memory service | stock |
| Personant's piece inside opencode | plugin | The stateless adapter that forwards to the memory service. | "the plugin" (21, 817, 1346); "Plugin route" (19); opencode's own plugins (18) | The opencode plugin | stock |
| The on-disk memory and its git history | memory store | All files under the store directory, with their git history. | "home", "home directory", `$PERSONANT_HOME` (151, 1327, 2286); "home tree" (156); "home git" (1030); `<Home>` (1537, 1559); "canonical memory" (152); "versioned store" (102, 113); "the substrate" (1273, 1564, 2139) | The memory store | stock |
| One process lifetime / one conversation container | service run / opencode session (keep) | As defined in §1.1. | "relaunch" (596, 700); "restart" (79); "startup" (1568); "service-run load" (1568); "process open" (2279); "session-scoped" (849, ambiguous) | none | coinage, defined 26 |

### Threads and their states

| Concept | Proposed term | Meaning | Variants now | THESIS | S/C |
|---|---|---|---|---|---|
| Unit of work memory is organized by | thread | The durable record of one problem being worked on. No sentence defines it. | "topic" on the user surface (1353, 1359–1366, "idle topic" 919); "indexed body" (778); "problem" does not occur | Thread lifecycle; "problem" | stock |
| Thread in a terminal state | closed thread | A thread in `wip`, `resolved`, `decided` or `abandoned`. | "retired thread" (232, 969); "closed topic" (1366); "RECENTLY CLOSED" (1361); "retirement" (947, 2001); "closure" (205, 877) | none | stock |
| Ending a thread | closure | Decay or `/done` → curator-drafted summary → chosen resolution. | "retire", "retirement", "retire.complete", "retirement summary" (2001), "closure summary" (680), "batch retirement" (947) | none | stock |
| Thread state set | the §2.2.1 list, plus one defined word per group | The seven stored states. | "engaged" (191, 641), "dormant" (560, 594, 1361), "paused", "closed", "live thread" (239), "warm" (233), "cold" (969) | "state machine" | mixed |
| Thread touched by a turn | engaged | The thread's symbols or tag were referenced in the turn. | "engagement", "engaged set" (656); "primary-engagement turn" (224, means owner turn); "recently active" (191) | none | coinage-lite, defined 641 |
| Thread a turn belongs to | owner | The one thread that receives the turn's excerpt. | "owner turn" (177), "owning thread" (1073), "primary" (224) | none | coinage, defined 633 |
| Thread bookkeeping file | thread registry (replaces "spine") | One record per thread, in one file. | "spine" (175 on), "spine record", "spine entry" (1443), "off-spine" (957), "spine cardinality" (957), "spine match" (2184, means recall match), "recognition entry/surface" (63, 64) | Indexes (derived, so not this) | coinage with stock alternative |
| What the model sees of a record | display line | One-line rendering of id, anchors, summary, state. | "display form" (209), "display name" (216), "NAME" (215), "working name" (1359), "gist" (835, 888, 1361, 2005), "`thr_N: display — gist`" (218) | none | coinage; "gist" undefined |
| Thread's recall handles | anchors | 0..`anchor.projection-max` normalized symbols re-derived each owner turn. | "anchor-list" in the topic tag (1914, 1934; not anchors); "anchor-rich" (895); "anchored to the decision delta" (1212); "archival anchors" (1289, 1694); sim clock anchor (2387) | Symbol extraction | coinage, defined 180 |
| Model's per-turn symbol contribution | tag symbols | Symbols the model attaches in its topic tag. | "anchor-list" (1914), "model-emitted anchors" (679) | none | stock |
| Recall token | symbol | A normalized term in one of three categories. | "identifiers", "entities", "tags" (380); "code symbols" (380); `history_symbols` (217); "symbol history" (243) | Symbol extraction | stock, defined 381 |
| Symbol's lifecycle | active / superseded | Still projected, or dropped out and kept as a recall handle. | "central" / "once central" (243); "protected set" (244); "high-specificity" (243); none defined | none | coinage |
| Symbol origin vs provenance | origin (emitter); derived-from (recall provenance) | Who emitted a symbol; the thread it was carried from. | "source" (391; also event source 442, `source=user.prompt` 424, `modification_source` 295); "provenance" (98 vetting; 395 `derived_from`) | none | stock |
| Per-turn record of a thread | turn excerpt | The terse excerpt written for an owner turn. | "excerpt" (224), "warm excerpts" (2033), "per-thread file"/`folio.jsonl` (233, 968, 2034), "thread body" (450, 2033), "turns/" (968) | none | stock |
| Non-owner thread update | engaged-not-owner | Recency and symbol merge, no excerpt. | "engaged-but-not-owner" (641) | none | stock |

### Composed request and its slots

| Concept | Proposed term | Meaning | Variants now | THESIS | S/C |
|---|---|---|---|---|---|
| What the model is sent | composed request | The request assembled from memory instead of a replayed transcript. | "composed request" (50), "the context"/"composed context" (1059), "working set" (518; "working-set.json" 596), "working window" (436, 1105), "working context" (212), "system prompt" (518) | Context composition; "composed, not replayed" | stock |
| One part of a composed request | slot | A named, budgeted component. | "layer" for E/A1/A2/B/C (518, 534); "slot" for the other three (544); "every slot — the five layers and the three above" (554); "tier" (523) | Context composition ("budgeted slots") | stock, THESIS term |
| The five memory slots | descriptive names, not E/A1/A2/B/C | Directives and conventions; this project's thread registry; other projects' digests; active threads; dormant threads. | "Layer E/A1/A2/B/C" (263 first use, 534 table); "high tier" (523) | none | letter codes are coinage |
| Threads loaded in full | active set | Up to `layer.b-top-k` threads, most recently engaged first. | "Layer B active set" (537, 560, 614); "Layer B order" (594); "active threads" (351) | none | coinage |
| Threads shown as display lines | dormant set | Threads shown only as display lines; not count-capped. | "dormant set" (560, 594); "DORMANT" (1361) | none | coinage, defined 594 |
| Slot answers | slot policy: budget, priority, overflow form, unused-share rule | The four things each slot defines. | "Slot answers" (552), "underusage" (558), "lend/borrow" (559), "fixed/dynamic slot" (561) | "budgeted slots in priority order" | coinage; "underusage" has a stock alternative |
| Size limits | token ceiling (whole request); slot byte budget (derived); live-turn reserve | Three distinct quantities. | `context.token-budget` (356), "token ceiling" (519), "byte budget" (603), "layer share" (522), `layer.budget.percentages` (352), "live_turn" (352, 522), "15% live-turn budget" (577), "resume budget" (1878), "budget pressure" (947), "Heap bounded" (2344) | "budgeted" | stock |
| Content shown by identifier only | evicted from the request | Dropped from this request for budget, still named at its version. | "evicted" (59), "content evicted for budget" (1094, 1130), "overflow form" (62) | none | stock |
| Full text vs content id | full content vs content-id line | A file's bytes rendered in place, vs one line naming operation, content id and path. | "literal" (1056, 1117), "literal diffs" (1117), "identifier line" (1073), "identifier-only" (1147), "content-id" (1236) | Content dedup | "literal" is a coinage |
| Stored file bytes | file-content store | Per-thread store of current content and recent diffs. | "tracked-file store" (227, 1078, 1091), "tracked files" (232), "tracked file" meaning the user's git (450), `files.json` (1729) | Content dedup | coinage |

### Events and the hook chain

| Concept | Proposed term | Meaning | Variants now | THESIS | S/C |
|---|---|---|---|---|---|
| Unit of incoming content | delta | One content addition to the working window, with a source. | "context-modification event" (434), "event" (442; "file event" 1107), "context delta" (450, 616), "delta" (456, 469); `context.modified` (424) | none | coinage with stock alternative |
| Per-delta processing sequence | per-delta pipeline | Extraction, engagement, dedup, budget check, logging. | "hook chain", "hook", "hook firing" (459–493), "per-event hook chain" (438); opencode hooks and git pre-commit hook (2152) | none | "hook" collides |
| Delta classes | task / decision retention class | Provisional-transient vs provisional-persistent. | "task-class", "decision-class" (228), "retention class" (1167), "provisional-transient/persistent" (1172) | none | coinage |
| Buffers | turn buffer; staging buffer | Per-turn accumulation of decision symbols; cross-turn holding area for task symbols. | "coalesce buffer" (469, 1189, 1211), "staging buffer" (472, 1184) | none | "coalesce buffer" is a coinage |
| Staging span | citation window (K = 3) | Turns after staging in which a decision delta may cite a symbol. | "K" (1104, 1198), "k" for top-k threads (537), `staging.window-turns` (1204) | none | coinage, defined 1198 |
| Moving a staged symbol | promotion (staging) | See collision 5. | 684, 1208–1217 | none | |

### Recall and closure

| Concept | Proposed term | Meaning | Variants now | THESIS | S/C |
|---|---|---|---|---|---|
| Surfacing older threads | recall | Matching current content against stored threads. Also the IR metric (2342). | "recall surface" (810), "surfaced" (813), "surface threshold" (837; no parameter by that name), "opportunistic recall" (336) | Recall | stock |
| The scoring methods | signals | Symbolic Jaccard, embedding cosine, model judgment. | "layers" (757, 823; "layer 2" 770), "tier" (822, 835), "intra-thread" as a fourth (778, 825) | Recall | stock |
| Embedding index levels | coarse / fine level | One vector per thread; per-excerpt chunk vectors. | "coarse tier", "fine tier" (762), "hierarchical" (761), "summary tree" (807, undefined), "flat-scan tier" (738) | Recall | "tier" collides |
| Offer outcomes | auto / ask / below surface | Three bands by score against the signal's own thresholds. | AUTO, ASK (825, 831), `banded` (338), "band" (820), "surfaced cap" (814), "auto threshold" (346, 348) | Recall ("thresholds") | all-caps names |
| Asking the user | question / confirmation | A question put to the user, and the answer. | "ack", "ack prompt" (161), "ack-mode" (two meanings, 295/338), "offer" (831, 919), "prompt" (205), "closure prompt", "ask" (831), "confirm" (204) | none | "ack" is an undefined abbreviation |
| Closure triage | routine / exception | Auto-accepted vs queued for review. | ROUTINE, EXCEPTION (895, 898); "pending-review queue" (1363); "boundary drain" (893) | none | all-caps names |
| Model roles | curator, dissector, embedder, judge | Drafts closure summaries; clusters old content; vectorizes; confirms candidates. | "curator" (179; also a symbol origin 394; "curator detects" 204); "dissector LLM" (2009); "model judgment" (764); "Personant's own models" (2087) | Model judgments / Model access | curator, dissector are coinages |
| Archive of retired threads | cold storage | Local tiered storage and retrieval for retired threads; restore by explicit fetch. | "deep cold archival" (955), "archive", "archival", "deep-cold" (456, 1025), "archived thread", "off-spine" | Cold storage | THESIS term; "archived thread" may stay as the narrower term |

### Durability and the day cycle

| Concept | Proposed term | Meaning | Variants now | THESIS | S/C |
|---|---|---|---|---|---|
| Crash safety | crash recovery | Startup reconciliation after unclean shutdown. | "Startup recovery" (1562); "recovery" for five other things (collision 4) | Durability and crash recovery | stock |
| Per-turn vs per-day git repositories | turn repository; history repository | Disposable fine-grain repo; permanent day-grain repo. | "daily DB"/"daily" (1273; commits per turn), "primary DB"/"primary" (964, 1273, 1088), `.git-daily`, "git DB" | "commit points to recover to" | "daily/primary" mislead |
| Daily rollover | day rollover (steps B0–B6) | Archival, day-commit, rebuild, reset of the turn repository. | "day barrier" (236), "barrier" (960, 1289), "barrier-exclusive", "barrier-homed" (960), "morning-init" (1800), "boundary" (893, 911, 1295), "nuked and reborn" (1293) | none | "barrier" is a coinage |
| Derived staleness proof | watermark | Turn-repository HEAD the derived files were built from. | `derived-watermark` (1595) | "Indexes … rebuildable" | stock |
| In-flight turn record | turn journal | Append-only record of prompt and response before canonical writes. | "journal" (1587), "turn transaction" (1607), "turn scope" (1402, 1617) | "write-ahead journaling" | stock |
| Recovery row | recovery case | One of 12 classified startup situations. | "cell" (153, 1649), "cell 3/4/9/12", "ARCH entry" (1657), "WT" (1651), "HEADTURN/HEADDAY" (1599), "DAILY" (1602), `INV-1…6` (1698) | none | coinage, used before defined (153) |
| Maintenance cycle | consolidation | Offline cycle that reviews and packs memory. | "consolidation cycle" (115), "sleep cycle" (1255, 1300, 2409), "keep/toss" (1250, 1255), "consolidation marker step" (2409) | none | "sleep" is a coinage |

### Configuration and surface

| Concept | Proposed term | Meaning | Variants now | THESIS | S/C |
|---|---|---|---|---|---|
| Instruction files | directives | Personant's layered files under `directives/`. | "directive" (283), "instructions" (opencode's, 548), "convention files"/"project conventions" (264, 561), "defaults" (file vs values), "directive" for system-prompt text (527, 1990) | Directives | stock |
| Tunable values | parameter | Named value overridable in a directive file, or fixed. | "parameter" (291), "key" (322), "setting" (2107), "knob" (1301, 1804), "directive key" (1204), "calibration window" (180) | none | stock |
| Interface | CLI subcommand; session command | `personant …` vs slash commands inside opencode. | "binary subcommands" (1321), "memory-native commands" (1340), "Persistent flags" | The CLI | "memory-native" is a coinage |
| Project and identity | project; handle / remote identity / display name | A user workspace Personant tracks. | "project root" (4.5), `current_root_path`, "session directory" (1426), "workspace" (120, 450, 2058), "external canonical identity" (259) | none | stock |
| Memory kinds, vetting | behavior / process / career memory; review, write path, accrual | As in THESIS and §1.8. | "proposal", "policy" (99), "accrual" (103), "attractive nuisance" (121), "standing … memory" (116) | Memory vetting; Career memory | "accrual" is a coinage |
| Event log | event log | Append-only daily text log. | "log" (418), "forensic line" (704, 727) | The event log | stock |
| Acceptance machinery | acceptance run, span, oracle, mock | Simulated multi-month run against a mock model. | "rung" (1859, 2191, 2213), "smoke/real ladder", "realism element" (2223), "known unknown" (2229), "gate" (2169) | none | "rung" is a coinage |

## 2. Collisions

Each item gives the meanings with their lines, then a proposed split.

1. **layer**
   - Meanings: working-set slots (518); recall signals (757, 823); project identity layers (259, 1432); test layers (2312, 2331); measurement stack (2326); "precision layer" (1255); "autonomic git layer" (957).
   - Split: slots, recall signals, identity levels, test levels. Retire "layer" as a proper noun.
2. **tier**
   - Meanings: "high tier" (523); coarse/fine embedding tier (762); exact lexical / flat-scan tier (738); signal label (822, 835); excerpt tiering (1724, 1727).
   - Split: "level" for embeddings, "signal" for recall methods, "tier" only for excerpt storage form.
3. **window**
   - Meanings: engagement decay (191); assembly (229, 1082); warm (233); live FIFO (779); citation (1198); diff count (1081); leading scan (1956); day-off idle (1301); closed-window cutoff (1361); working window (436); "calibration window", which means a provisional value (180, 363, 413, 787, 861).
   - Owner to confirm: assembly, warm and FIFO likely name one thing.
   - Split: one "recent-excerpt window"; "calibration window" becomes "provisional value".
4. **recovery / recover**
   - Meanings: crash recovery (1562); restoring an archived thread (1010; `archive recover` 1331); missing-tag and empty-response fallback (686, 709); "recovery turn" (499); "recovery reminders" (527); "recovery artifact" (1639); "deep-cold recovery" (456); recovering evicted file content from the user's version control (1097, 1147).
   - Split: crash recovery; restore; fallback; resumed turn.
5. **promote / promotion**
   - Meanings: staged symbol into the turn buffer (1211); thread into the active set (614, 828); dedup back to literal (457); local→remote project (1472).
   - Split: promote; load; restore full content; adopt remote.
6. **evict / eviction**
   - Meanings: dropped from the request for budget (59, 1130); Layer B/C at closure (877, 901); bump-eviction (477); symbol-history cap (243, 411); staging window-close (1220).
   - Split: evicted from the request; demoted; trimmed; expired.
7. **tag**
   - Meanings: `#`-tags as a symbol category (381); the topic tag (1892); git lifecycle tags (1748); "tag-less" (663).
   - Split: hashtag; topic tag; git tag.
8. **identifier / id**
   - Meanings: symbol category (380); content id and identifier line (1058, 1070–1073); thread id (1912); `claim IDs` (464, undefined).
   - Split: identifier symbol; content id; thread id.
9. **anchor**
   - Meanings: projected thread symbols (180); the model's `<anchor-list>` (1914); "archival anchors" (1289, 1694); the sim clock anchor (2387, 2413); "anchored to" (1212).
   - Split: anchors (projection only); tag symbols; archive commits. The clock sense stays.
10. **history**
    - Meanings: `history_symbols` (217, 243); replayed transcript tail (525); in-run history (79); career/primary history (1288, 1694); opencode history rows (1054); `historical_*` paths and URLs.
    - Split: symbol history; transcript; git history.
11. **canonical**
    - Meanings: source-of-truth vs derived (152, 997); normalized form (270); external canonical identity (259, 1439); "label is canonical" (866); "canonical-namespace" (1654); "canonical-by-construction" (2346).
    - Split: authoritative; normalized; remote identity.
12. **home / substrate / store**
    - Meanings: "home" for the store directory, its git repo and the layout revision (2302); opencode's data home (168); "substrate" for the store (1273, 1564, 2139), for the runtime and memory model (2277), and for the git-operations component (1658).
    - Split: memory store; store directory; memory service; version.
13. **worktree / working tree / workspace**
    - Meanings: the store's git working tree (1088, 1579, 1693; "WT" 1651); "workspace" for the user's repository (120, 450, 2058); "tracked file" for both the user's git (450) and the store's git (232).
    - Split: store working tree; user workspace; never "tracked file" for the store.
14. **session**
    - Meanings: opencode session (26); "session-scoped" (849, probably meaning the service run); sim work session of ~6h (2398); "session directory" (1426).
    - Split: service-run scope at 849; "work block" for the sim.
15. **marker / mark**
    - Meanings: truncation marker (581); `op-in-progress.json` (1591); `lifetime:*` (1260); user marks reference material (1179); in-window mark (849); recall-surfaced marks (1409).
    - Split: truncation notice; operation marker; lifetime tag; reference flag.
16. **gate**
    - Meanings: acceptance gate (2169); format gate (2290); B2 spine gate (1790); integrity gating (1290); "ungated" (1329).
    - Split: acceptance criterion; format check; integrity check.
17. **rung**
    - Meanings: a span of simulated days (2191); a run configuration (2180, 2213, 2265, 1859).
    - Split: span; run configuration.
18. **version / v0.1**
    - Meanings: v0.1 as the acceptance release (231, 477, 1039, 2169); substrate semver, with the gate at v0.5.0 (2283); home `format = N` (2286); version identity / build stamp / format stamp (1328); a file's earlier version (62, 1094).
    - Split: release label; semver; format revision; file version.
19. **prompt**
    - Meanings: user input (444); prompt template (2001, 1987); asking the user (205, 1477); MCP prompt (45); re-prompt (1039).
    - Split: user message; prompt template; question; MCP prompt; re-prompt.
20. **resolve / resolver / resolution**
    - Meanings: the `resolved` state; the chosen resolution at closure (205, 900); "interactive resolver" (919); "name resolver" (1360); phantom resolution (650); directory → project (1445).
    - Split: outcome; closure dialog; name lookup; match.
21. **fire**
    - Meanings: trigger; recall candidate logged (`spine.match-fire` 812); `recall_fires` counted on accept (829, 176).
    - Split: trigger vs recall surfacing count (rename the field).
22. **sweep**
    - Meanings: full-worktree staging (1621, 1294); debris (1654); percentage simulation sweep (540); calibration sweep (2371).
    - Split: full staging pass; debris cleanup; parameter sweep.
23. **stamp**
    - Meanings: format stamp / build stamp (1328); stamp commit and stamp-repair (984, 1659); ref `<stamp>` timestamp (1756); watermark stamping (1741).
    - Split: format number; build id; back-fill; timestamp.
24. **auto**
    - Meanings: `closure.ack-mode: auto` (329); the AUTO band (825); `ack=auto`; "autonomic" git (957, 2141).
    - Split: automatic; auto-fetch band.
25. **agent / model / LLM**
    - Meanings: the coding assistant (449, 1182, 1259); opencode agents and agent mode (16, 548); "data model" (142); "memory model" (2277); "flat model" (241).
    - Split: the assistant or the model; opencode agent; data model.
26. **route**
    - Meanings: way or mechanism (47, 1136, 1181, 1942); "turns route to threads" (30); "this route", meaning the plugin approach (2072); "thread routing" (1991).
    - Split: mechanism; assign; "plugin route" as a pointer only.
27. **entry**
    - Meanings: spine entry; recognition entry (63); overflow-form entry (561); archive index entry (973); staging entry (1195); excerpt; `ARCH entry` (1657).
    - Split: registry record; archive entry; staging entry; stand-in line.
28. **surface**
    - Meanings: user surface (1314); recall surface (810); live recall surface (1037); forensic surface (1038); filter surface (871); "surface form" (388); "surface the journal" (1639).
    - Split: user interface; recall candidates; show/report.
29. **boundary / barrier**
    - Meanings: closure drain boundary (893, 911, undefined); per-turn boundary contract (422); semantic boundaries (484); process boundary (2290); durability boundary (1280); day barrier.
    - Split: one name for the log line; one for the drain moment.
30. **escape hatch**
    - Meanings: the always/banded mode parameter (851, 927); `prj_default` (1554).
    - Split: mode parameter; no-project default.
31. **lifecycle**
    - Meanings: thread (2.2); symbol (405); transient-data (1159); frontmatter `lifecycle` field (867); git lifecycle tags (1748).
    - Split: qualified uses are fine; rename the field and the tags.
32. **ack-mode**
    - Meanings: `closure.ack-mode: auto | always` (329) and `recall.ack-mode: banded | always` (338) take values that mean different things.
    - Split: separate names, or one shared definition.
33. **bootstrap / init**
    - Meanings: project bootstrap at startup (4.5.7); `system.bootstrap` (2279); §8 "Bootstrap and lifecycle"; bootstrap carve-out (1845); `personant init` (1323); "init" in startup order (1568); "morning-init" (1800).
    - Split: scaffold; project resolution; turn-repository init.

Not collisions (standard words pinned by context): "budget" as cost (2175), directive "scope", "frontmatter", "recall" as the IR metric (2342, 2358), "anchor" as the clock verb (2387).

## 3. Coinages with a stock alternative

| Coinage | Stock alternative | Fit |
|---|---|---|
| spine | thread registry (or thread table) | Fits. THESIS "indexes" are derived, and the spine is canonical (997). |
| Layer E / A1 / A2 / B / C, "high tier" | named slots | Fits. |
| context-modification event / hook chain | delta / per-delta pipeline | Fits; "hook" already means opencode and git hooks (2152). |
| coalesce buffer | turn buffer | Fits. |
| underusage; lend / borrow | unused-share reallocation | Fits. |
| overflow form | stand-in | Partial; undefined at first use (62). |
| calibration window | provisional value | Fits. |
| barrier, barrier-homed, barrier-exclusive, morning-init | day rollover; runs only at rollover; turn-repository init | Fits. |
| daily DB / primary DB | turn repository / history repository | Fits; "daily" commits per turn, and "DB" implies a database. |
| nuked and reborn, microscope, forensic breadcrumb, canary, attractive nuisance, dead zone, heap | deleted and recreated; fine-grained recovery points; trace; indicator metric; tempting wrong approach; gap in findability; size | Fits; these are figures of speech. |
| sleep cycle | consolidation | Fits; already "consolidation cycle" at 115. |
| phantom | dangling reference / unknown thread id | Fits. |
| literal | full content / verbatim | Fits. |
| embedding-debt cap | unembedded backlog limit | Fits. |
| rung | span; run configuration | Fits. |
| cell (recovery) | case | Fits. |
| memory-native commands | session commands | Fits. |
| accrual | lesson filing, or "learning" | Partial; owner to rule. |
| curator, dissector | summarizer, clusterer | Keep as role names, but define each once (neither is defined: 179, 2009). |
| deep cold archival | cold storage | Settled (process-notes). "archived thread" may stay as the narrower term. |
| topic (user surface) | thread | Owner to rule: the bridge at 1353 keeps a two-term pair. |
| INV-n, B0–B6, EXCEPTION/ROUTINE, AUTO/ASK, HEADTURN/HEADDAY, WT, ARCH, DAILY | lowercase names, defined once | All-caps names read as variables. |
| `$PERSONANT_HOME` as a name for the store | store directory (env var only in §8.2.3) | Fits. |
| "project root" | the project's directory | Its meaning shifts with the case (1426). |

## 4. Undefined at first use

- **sub-mind split and re-merge** (14), **Code Mode** (18), **compaction / summarization** (14), **MCP prompt** (45).
- **thread** (30; no defining sentence), **topic** (30), **turn** (30; closing never stated), **recall** (14; defined 757), **composition** (13; defined 518).
- **slot**, **overflow form** (62; defined 544, 558). **spine**, **recognition entry** (64; spine at 175; recognition entry only via ARCHITECTURE). **evicted** (59; never defined).
- **checkpoint** (83; Personant sense 1294, 1360). **kept session**, **scrollback** (84).
- **symbol** (180; defined 381). **curator** (179). **owner turn** (177; defined 633). **recall-fire count** (176; explained 829). **warm window**, **per-thread file** (233; `folio.jsonl` only at 968, 2034). **tracked-file store** (227; defined 1078). **day barrier**, **B1b** (236; defined 1724). **once central**, **high-specificity identifier**, **protected set** (243–244; never defined).
- **engagement decay window** (191; only through parameters at 328). **Layer E / A2 / B** (263, 279, 451; defined 530–538). **dormant set** (538; defined 594). **ack** (161, 295, 338; never defined). **ack-mode** (two meanings). **banded** (338; defined 820). **Jaccard** (336; query-side set never defined).
- **claim id / claim IDs** (380, 464; never defined). **consolidation cycle** (115; ARCHITECTURE only). **keep/toss** (1250). **summary tree** (742, 807; never). **completeness floor** (741; defined 801). **orientation preamble** (526). **provider pool** (170). **live thread** (239). **`prj_default`** (1551; defined 274).
- **Day**: day barrier, day index, day-commit (236–1289). The production day boundary and timezone are never stated; UTC is stated only for the sim (2387).
- **cell** (153; defined 1649). **primary / daily** (964, 1088; defined 1273, 1691). **INV-n**, **B0–B6**, **(5)** and **(10)** as labels (236, 1850). **ARCH entry** (1657), **WT** (1651).
- **drain** (893, 962; two senses). **boundary** (893, 911). **U3–U9**, **ROADMAP B6a** (1135): pointers to documents a standalone reader does not have.

## 5. Seen, other kinds (held for later passes)

1. Mechanism in SPEC: §4.5.8 recovery cells, trailers, B0–B6, INV-1…6, the §3.8.1 `archive/index.jsonl` JSON, the regex at 1950, field and file names.
2. "Unused → ARCHITECTURE «…»" stubs (175, 212, 222, 249, 258, 279, 380, 384, 394, 407, 417) and placeholders (1386, 1418, 1884, 2057) send the reader elsewhere for requirements.
3. 522's "12/18/15/55" against the E/A1/A2/B/C order disagrees with 352 (E 12, A1 10, B 55, C 15).
4. Display-name fallback disagrees: 216 vs 920 vs 835.
5. Recall layer numbering disagrees: 764 vs 823.
6. Acceptance gate labeled "v0.1" (2169 and elsewhere) but landing at v0.5.0 (2283).
7. "chars/token" (522) vs "B/tok" and "TEXT-ONLY" (356).
8. §9 opens by restating the thesis (2161).
9. SPEC cites THESIS for a requirement (119).
10. Numbering gaps and irregular nesting (no §7; §4.3.3 beside §4.3; "§3.1 §5.5 mid-turn fetch").

## Rulings

### Principles

1. THESIS terms carry into SPEC: memory service, memory store, plugin, slot, cold storage.
2. Letter codes and all-caps names become descriptive lowercase names, each defined once (Layer
   E/A1/A2/B/C, AUTO/ASK, ROUTINE/EXCEPTION, INV-n, B0–B6, WT, HEADTURN/HEADDAY, DAILY, ARCH).
3. A coinage with a stock alternative that fits (section 3, "Fits") takes the stock term.
4. Collisions split as section 2 proposes; a word keeps a meaning only where context pins it.

### Individual calls

| Current | Ruled term | Basis |
|---|---|---|
| spine | thread registry | Principle 3. Not "index": THESIS uses that for derived files, and the registry is canonical. |
| daily DB / primary DB | turn repository / history repository | Principle 3. |
| curator / dissector | summarizer / clusterer | Principle 3. |
| topic (user surface) | thread, everywhere | One term per meaning. |
| accrual, directive accrual | feedback directive tuning; "feedback tuning" where directives are already the subject | Owner. "Tuning" alone reads as model tuning; "directive" covers behavior memory, process memory and parameters, and avoids giving "process" a second sense. |
| assembly window, FIFO window | recent-excerpt window | Same thing: a thread's newest excerpts, up to `ThreadTurnWindow`, read into the composed request (ARCHITECTURE "Assembly window vs retention"). |
| warm window, warm / cold excerpts | out of SPEC | A storage tier (loose files vs `folio.jsonl`; `HotExcerptTail` in `internal/store/folio.go`) is mechanism. SPEC keeps the outcome: excerpts are returned byte-exact however stored. ARCHITECTURE names the tiers as excerpt storage tiers, distinct from THESIS's cold storage. |
