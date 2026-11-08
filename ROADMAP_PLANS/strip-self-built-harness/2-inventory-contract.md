## 2. Inventory: contract

Every change lands in the package that motivates it. "Unused" follows CONVENTIONS "Document
conventions" (`PROJECT_DOCS/CONVENTIONS/document-conventions.md`): the heading keeps its number,
its text says what replaced it, and the number joins that leaf's list of unused numbers. Rows for
WP8 and WP10 give exact text in the package; the rest state the outcome the coder writes.

| Leaf (under `PROJECT_DOCS/` unless noted) | Change | WP |
|---|---|---|
| SPEC §4.1 | delete the U5 `ping`/`models` Open; in the `version` bullet, delete the U6 Open | 1, 2 |
| CONVENTIONS "Versioning and acceptance" | delete the U6 Open (it names `test-fe`) | 1 |
| SPEC §9.1 "Version lines" | delete the U6 Open; two lines remain: the substrate semver and the home format | 2 |
| ARCHITECTURE "Event log format" | 2: drop `frontend=` from the `system.bootstrap` row and the example. 3: drop rows emitted only by `toolloop.go`. 6: `retire` row keeps `prompt` (`inactivity=`), `complete`, `curator-error`, `load-error`, `apply-error`, `error`; drops `ack`, `pending`, `defer`, `resolver-error`, `trigger=manual\|queued`; example `retire.ack` line → `retire.complete`. 7: `recall` row drops `offer`, `decline`, `ack=` | 2, 3, 6, 7 |
| ARCHITECTURE "Module map" | `cmd/` row: drop the `chat`/`ping`/`models` sentence. `memops` row: drop `Config`. `model` row: "LLM client surface for the in-process runtime and the live tests: `Client`, `MockClient`, the OpenAI-compatible `HTTPClient`; the binary does not link it" | 1, 5 |
| CONVENTIONS "Dependencies" | 1: delete the sentence from "Do not reintroduce `github.com/peterh/liner`" to "(origin: commit c1ddea2)." 3: delete the rest of "Pending removal". 5: replace the U5 bullet by adding `langchaingo/llms` to "Permanently out" with "Personant has no inference path of its own (SPEC §8.2.1)" | 1, 3, 5 |
| SPEC §8.1 "opencode settings" | delete the U5 Open | 5 |
| SPEC §8.2.1 | rewrite: the memory service reaches models only through opencode: text generation (curator, dissector, recall judgment, vetting reviewer) through the plugin's `experimental.generate`, embeddings through opencode's embedding route (`Open:` the route, ROADMAP A12a). Personant holds no endpoint, provider pool or API key. Delete "Key material" | 5 |
| SPEC §8.2.2 | delete the U5 Open. Add: the reference names one of opencode's providers and a model it serves; `Open:` the bootstrap check that it exists (A12a) | 5 |
| SPEC §2.1 "Outside `$PERSONANT_HOME`", "Secret-bearing content" | delete the U5 Open. Secret-bearing: Personant reads no API key and keeps no secret-bearing file | 5 |
| SPEC §5.2, §5.3, §1.8 | replace each U5 Open with "through opencode (§8.2.1)"; §1.8 keeps `Open:` for which model reviews | 5 |
| ARCHITECTURE "Serving deployment", "Single spelunking location", "Substrate non-negotiables" table | delete the U5 Opens and the `paths.go still places…` sentence; the config row reads `config.toml` under `~/.config/personant/` | 5 |
| SPEC §3.5 index | rewrite to the autonomic flow (decay → draft → retire as `resolved` → B/C eviction; failed or empty draft retried). Delete the U4 Open, the exception queue, the announcement and `/back-to`. Revision path: a returning engagement resurrects the thread (§3.2) and its next close re-drafts. Delete the leaves "The offer names the topic", "Escape hatch", "Ack-quality instrumentation" | 6 |
| SPEC §2.2.1 | transitions: thread creation → `active`; `active` → `resolved` on engagement decay (§3.5); archive recovery → `wip` (§3.8.3); `wip`/`resolved`/`decided`/`abandoned`/`paused` → `active` on re-engagement. `paused`, `blocked`, `decided`, `abandoned` stay readable for stored threads and are no longer entered | 6 |
| SPEC §3.6 | "→ batch retirement through the §3.5 closure path" (drop "ack flow") | 6 |
| SPEC §2.6 index example; §2.6.1 | 6: drop `closure.ack-mode` lines. 7: drop `recall.ack-mode` lines and the accepted-offers example line; auto thresholds read "fetched autonomically". 8: header (exact text in WP8) | 6, 7, 8 |
| ARCHITECTURE "Closure and retirement", "Fallback dissection", "The model drafts, deterministic code writes", "Thread lifecycle" (stage 1 explicit act) | match the SPEC §3.5 rewrite | 6 |
| SPEC §3.4 "Recall surface", "Banding", "Escape hatch" | merge into "Recall surface": the AUTO band is fetched autonomically through the fetch path, logged `recall.accept`, `recall_fires` incremented; below the auto bar, nothing beyond the match-fire record; a resident non-intra candidate is skipped. Thresholds keep their §2.6.1 keys. Delete "Banding" and "Escape hatch" | 7 |
| SPEC §4.3 (index and "Who supplies the reason") | Unused: no recall candidate is declined; add §4.3 to the unused numbers | 7 |
| ARCHITECTURE "Recall mechanisms" index, "Personalization through directive accrual", "Acceptance-run mechanics", "opencode-in-loop harness" | drop the U4 Open, decline categorization as an accrual input, the pinned `recall.ack-mode`, "and acks" | 7 |
| SPEC §4 index, §4.2 and "Vocabulary bridge", §2.2.2, §3.0.1, §3.10.1; ARCHITECTURE "Integration shape", "The layered working set"; CONVENTIONS "Document conventions" (§4.2) | exact text in WP8 | 8 |
| SPEC §4.5.1, §4.5.2, §4.5.3, §4.5.4, §4.5.5, §4.5.6, §4.5.7 | 4.5.1: the active project is resolved from opencode's project directory; drop "unless the user selects one explicitly". 4.5.2: a project is one directory and a bundle of threads, with two layers: the internal handle and the display name (the directory's base name at creation). A directory Personant has not seen is a new project, moved or not: Personant cannot tell a move from a copy, or from several copies, and people routinely copy old projects as templates for new ones. Drop the external canonical identity row and "survives file moves"; no Open replaces them. 4.5.3: resolve by `current_root_path`, else create; Personant reads nothing in the directory, its git included; drop steps 1–3, the prompt and the U4 Open. 4.5.4: Unused (Personant reads no remote). 4.5.5: Unused (a moved directory is a new project, per §4.5.2). 4.5.6: Unused. 4.5.7: path match, then create, then `prj_default` with no directory; drop explicit selection, the git-remote step, step 3 and the `last-active` section. Add §4.5.4–§4.5.6 to the unused numbers | 9 |
| SPEC §2.5.1 | schema: drop `historical_root_paths`, `remote_urls`, `historical_remote_urls`, "Remote URL normalization", the external-identity sentence and the `/project rename` mention | 9 |
| ARCHITECTURE "Project metadata", "Key abstractions" (Project row), "Project identity (dual-layered)", "Autonomic vs LLM-controlled", "Integration shape" (Sessions and projects bullet) | drop the three fields and remote normalization; Project: handle plus display name, rooted at one directory; rename "Project identity (dual-layered)" to "Project identity" (both `Contents:` lists) and state the directory-only resolution; Autonomic: the workspace-repository bullet becomes "The service never reads the user's workspace repository; its only git is the home's two repositories"; Integration shape: "through the bootstrap waterfall" → "from its directory alone" | 9 |
| SPEC §4.5.8 | (3): journal bytes are preserved and reported in the event log (`recovery.journal-recovered`), not surfaced to the user; drop the U4 Open. Opening: drop `last-active` from derived state | 9 |
| SPEC §9.4 index; ARCHITECTURE "Home layout", "File layout — `internal/scenarios/`" | drop `last-active` and its invariant | 9 |
| README | 1: Quick Start drops "plus the self-built chat REPL … still starts the REPL."; delete "## Chat REPL"; Build Targets drop the `test-fe` line; acknowledgements drop the `x/sys` row. 3: drop the readability, html-to-markdown and `x/net` rows. 5: Sim Knobs: "Both live toggles read `test/rundata/test.{providers,config}.toml` (user-provided, gitignored) and need" → "Both live toggles use the `reaper` endpoint from `internal/testsupport` (override with `PERSONANT_REAPER_URL`) and need"; the `toml` row's use → "`version.toml` parsing". 10: exact text in WP10 | 1, 3, 5, 10 |
| `ROADMAP_PLANS/inference-in-loop.md` | its "Live mode reads `test/rundata/test.providers.toml`…" bullets → the testsupport endpoint | 5 |
| SPEC §4.3.3, §4.5.8 (10); ARCHITECTURE "Invariant for the production inversion", "Start order and resume", "Event log format" (U8); CONVENTIONS "Versioning and acceptance" (PATCH rule); ROADMAP.md | exact text in WP10 | 10 |

### Citations to re-map at resumption

The doc sweep rediced `PROJECT_DOCS/` and renamed headings to the settled vocabulary (closure →
closing, spine → thread registry, home → store directory, and so on), so this plan's heading
citations and the rows above are re-mapped against the current trees before any package resumes.
Leaf paths that no longer resolve:

- `4-work-packages/wp2-retire-the-front-end-version-line-mechanical.md` (done; historical):
  `PROJECT_DOCS/SPEC/9-measurement-and-validation-regime/9.1-the-four-month-simulation-v0-1-acceptance-gate/version-lines.md`
  (now `…/9.1-the-four-month-simulation-the-v0-5-0-acceptance-criterion/version-lines.md`) and
  `PROJECT_DOCS/ARCHITECTURE/testing-as-the-lab-bench/event-log-format.md` (now
  `PROJECT_DOCS/ARCHITECTURE/testing-as-the-measurement-instrument/event-log-format.md`).

