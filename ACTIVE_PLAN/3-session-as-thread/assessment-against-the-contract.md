### Assessment against the contract

- **SPEC §1.1.** "Turns route to threads by topic (§3.2), never by session" inverts: turns route
  by session. "Deleting a session leaves memory untouched" still holds.
- **SPEC §5.1 and the topic tag.** The thread list (`thr_N`, `*new-topic*`, multi-existing and
  mixed lists) has no job left, because the session names the thread. Only the anchor list, the
  model's per-turn symbol contribution (`source = model`), keeps a use. The deterministic §3.3
  passes already extract symbols from every delta. An in-band tag kept for anchors alone still
  costs the per-protocol stream abort and the transcript stripping that U3 names. U3 narrows to
  one question: does the model still contribute anchors, in band or through a tool, or not at all?
- **SPEC §3.2, owner and engaged.** The owner is always the session's thread. The single-owner
  invariant holds trivially, and its structural guard stays. Engaged-but-not-owner reduces to
  "resident in Layer B because recall fetched it". The follow-on must decide whether a fetched
  thread still takes recency and a `history_symbols` merge: merging another session's symbols
  into it rewrites its identity. Phantom resolution and owner fallback go, because the model
  supplies no IDs. Anchor projection at owner-turn close stays.
- **SPEC §3.0.4.** Recovery turns and in-order tool results are unchanged. Union accumulation of
  the turn's symbols stays: it feeds the owner's single engagement and §3.10 staging. The fan-out
  of "engagement fires once per affected thread" collapses to the owner, plus fetched threads if
  they keep recency.
- **SPEC §3.3 and §5.5.** Missing-tag recovery goes. The §5.5 mid-turn fetch loses its trigger, so
  cross-thread content arrives through autonomic recall at turn close, which WP7 builds, and,
  if the follow-on wants one, a recall tool the model calls.
- **SPEC §4.5.** Resolution runs at each session start, and the session's thread belongs to the
  project its directory resolves to. WP9's directory-only resolution is already the "project dir
  = project" rule, so the follow-on adds nothing to §4.5.
- **Data model.** Mapping a returning session to its thread needs the opencode session ID on the
  thread: an optional frontmatter and spine field. That is a stored-format change, so it needs its
  own home-format bump (after WP9's) with a restamp-only migration. The thread's `description`
  comes from the session's title or its first prompt.
- **SPEC §9.1 and the sim.** The tag-fidelity measures go. The generator's unit becomes the
  session: interleaved topics inside one session stop being separate threads, and
  `ROADMAP_PLANS/within-thread-interleaving.md` no longer applies. The recall oracle stays: a new
  session on an old topic must surface the old thread.
- **Knock-on items.** A10 (tag fidelity, `derived_from` lineage) is subsumed. B8: `/clear`
  starting a new session starts a new thread. B6: a forked session is a new thread whose
  `derived_from` names the parent. U9: replaying history per session fits option (b), rebuilding
  from opencode's stored messages.

