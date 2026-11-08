**Source note:** all answers come from the SPEC text alone. The Q2 analogies are my general engineering knowledge applied to what the SPEC describes. Where a Q3 item could be reconciled by a reading the text supports, I say so.

## 1. What it does and doesn't do

Personant is a plugin for opencode, an AI coding-agent terminal tool, backed by a local memory service. It takes over what goes into every model request: it builds each request from durable, budgeted memory, compacts long conversations, and recalls relevant past work. That memory is organized as per-problem "threads", keyed by extracted symbols and embeddings, and stored as plain-text files under git with crash recovery. It does not provide the UI, sessions, tools, permissions, providers or credentials, and it never calls a model directly; all of that is opencode's. It also prescribes no workflow or interaction mode, and many interactive surfaces remain open (U3/U4).

## 2. Expected parts and their nearest ordinary analogue

| Part | Resembles |
|---|---|
| opencode plugin | Request-interception middleware / proxy hook |
| Memory service | Local sidecar daemon, like a language server |
| Per-delta pipeline (§3.0) | Ingest/ETL middleware chain |
| Composer with budgeted slots (§3.1) | Prompt template renderer with fixed-size sections; a quota allocator |
| Active/dormant sets | LRU cache |
| Symbol extractor (§3.3) | Regex/ctags-style keyword extractor |
| Topic-tag parser (§5.1) | Small line-grammar parser |
| Thread registry + thread dirs (§2.2–2.3) | Issue tracker backed by a sorted JSONL table |
| Thread lifecycle (§2.2.1, §3.5) | Issue-workflow state machine |
| Symbol index (§2.4) | Inverted index |
| Embedding index (§3.4) | Vector store |
| Recall matcher + banding | Search/recommendation with confidence thresholds |
| Staging buffer (§3.10) | TTL pending queue |
| Tracked-file store + dedup (§3.9) | Content-addressed blob store with delta compression (git objects) |
| Summarizer / clusterer (§5.2–5.3) | Batch LLM summarization jobs |
| Cold storage (§3.8) | Archival tier / log rotation |
| Turn repo, history repo, journal, recovery (§3.11, §4.5.8) | Database WAL + checkpoints + fsck |
| Day rollover | Nightly cron maintenance |
| Event log (§2.8) | Append-only structured log |
| Directives with precedence (§2.6) | Layered config (git system/global/local) |
| Memory vetting write path (§1.8) | Code-review gate on a single commit path |
| Project resolver (§4.5) | git-remote/directory identity lookup, like direnv |
| Format check / migration (§9.1) | Schema migrations |
| `personant` CLI | Admin tool like `git fsck` / `git log` |
| Mock LLM + simulation harness (§9) | Test double + seeded soak/load test |

## 3. Self-contradictions

**Strong**

1. **Hand-edits and recovery.** §2.1 says a user's direct edit, "where a torn turn's reset would revert it, [is] preserved byte-exact in quarantine… first". §4.5.8(2) says a hand-edit "is never reverted", and §4.5.8(4) says "a hand-edit is never reset".
2. **When a hand-edit is committed.** §4.5.8(2) says it "is committed at the next clean-stop checkpoint or day rollover, not with the next turn". §3.11 says a checkpoint is also made "after a mid-run structural change (creating or closing a thread…). Every checkpoint commits the whole store working tree, hand-edits included".
3. **When the history repository is written.** §3.11 says "The history repository is written only at day rollover, apart from crash recovery". §4.5.8(8) says a legacy store "is adopted into the history repository on first open". §3.8.3 also says a restore "is committed to the memory store's git history", without saying which repository.
4. **Torn-turn content entering memory.** §4.5.8(3) says the bytes "are never replayed into memory. The user re-issues if they choose." §1.6 and §4.5.8(10) say "turns opencode kept that the memory service rolled back are re-ingested".
5. **The durability bar.**
   - §4.5.8 says "at most one turn may ever be lost, under any termination timing", but §4.5.8(7) says "History-repository loss — unrecoverable."
   - §3.11 says "the in-flight turn's prompt and response are recoverable from the moment the prompt is submitted", but §4.5.8(7)(b) says a journal-write-failure means "losing that one in-flight turn's content".
6. **What updates the registry record.** §3.2 says "the thread's registry record changes only when its anchor set does". §2.2 says the record carries "timestamps, turn count and recall-fire count", and §3.0.4 increments `turn_count` on every owner turn.
7. **The budget check in the pipeline.** §3.0 says "no content escapes extraction, dedup, or the budget check", and §3.0.2 step 4 says the check "triggers budget demotion before the next delta lands". The same step's note says "This step performs no budget demotion in v0.1."
8. **The `active` state versus decayed threads.** §2.2.1 defines `active` as engaged or "within the engagement decay window". §3.5 says an exception close-out is "queued silently" and that a failing summarizer "leaves its thread un-closed". No state exists for a thread that has decayed but is not closed.
9. **Routine close-outs.** §2.2.1's transition trigger is "closing question with chosen outcome", and `resolved` means "problem solved; work complete". §3.5 says a routine close-out "is applied without asking… `resolution=resolved`".
10. **Entering `blocked`.** §2.2.1 says "summarizer detects + user confirms". §5.2 says "The summarizer is invoked when a thread closes". Also, no transition leaves `blocked`, yet §3.2 applies "state→active resurrection" to any engaged thread.
11. **Project resolution.** §4.5.3 and §4.5.7 both resolve opencode's session directory, but differ:
    - Remote: §4.5.3 uses "(`origin` by default; otherwise none)"; §4.5.7 uses "(or the first remote if no `origin`)".
    - Lookup: §4.5.3 searches `remote_urls` only; §4.5.7 searches `remote_urls` and `historical_remote_urls`.
    - Ancestors and last-active: §4.5.3 has no ancestor walk and no last-active step.
    - No match: §4.5.3 offers "[n] cancel"; §4.5.7 offers "[n] no project (use prj_default)".
12. **Cross-project recall.** §2.6.1 defines `recall.cross-project-threshold`, "higher bar for cross-project surfacing", and §4.3 has a `wrong-project` decline reason. Yet the §3.4 auto-fetch band loads "through the SAME fetch path", and §3.1 says "A thread of another project is declined… neither loaded".
13. **History length.** §2.3 says history is "hard-capped at `history.cap-per-thread`". §2.7.4 says "flat history length… is a §9 sim obligation, not a static guarantee".

**Weaker, or reconcilable**

14. **Excerpt file count.** §2.3 says "Appending a turn writes one small file" and also "The number of files in a thread's directory does not grow with its count of excerpts". These reconcile only if the rollover's "settles excerpt storage" (§4.5.8(5)) means packing excerpts.
15. **Digest freshness.** §2.5.2 says the digest is "current with them", and §3.7 says it "stays current". §2.1 says "between day rollovers derived files may lag by design".
16. **Close-out commits.** §3.5 says a routine close-out "adds no commit path of its own". §3.11 says closing a thread triggers a checkpoint commit.
17. **Commit count.** §3.11 says "bounded to ~365 commits a year", and the same paragraph adds "plus the day's archive commits".
18. **Refusing to open.** §4.5.8 says the service "refuses to open on an… unrepairable memory store". §4.5.8(4) says an unrepairable archive entry merely "stays refused".
19. **Diff threshold denominator.** §2.6.1 and §3.9.1 say "of the new content's size". §3.9.2 says "of the file's current size".
20. **Content-id line contents.** §3.9.1 says "content id (a commit hash for `fs.commit`)". §3.10.6 says "a content-id or commit-hash prefix".
21. **Slot count.** §3.1 says "Every slot — the five memory slots and the three above", yet calls "live turn" a dynamic slot. It also lists "project conventions" as a fixed slot, although conventions sit inside the directives slot.
22. **Extraction passes.** §3.3 is titled "three passes", but pass 3 "contributes no symbols".
23. **§3.9.4 heading versus body.** The heading says "content-id-only is insufficient"; the body says "A file at or above it is shown by content id only."
24. **User involvement.** §1 says Personant does not define "how much the user takes part". `closure.ack-mode` and `recall.ack-mode` (§2.6.1) set exactly that.
25. **Tracked-file store (the document flags this itself).** §3.9.1 says "the history repository never holds it" and also "the implementation still commits the tracked-file store to the history repository".
