### The model drafts, deterministic code writes

Standard pattern: THESIS "The building blocks", row "Model judgments".

The model never mutates authoritative state directly. It proposes content; deterministic code decides what of it is kept, and writes it.

| Judgment | Deterministic check before it is kept |
|---|---|
| Topic tag: which threads a turn engages, plus an anchor list (SPEC §5.1) | `prompt.Parse` accepts only a line matching the tag pattern within the preamble scan; each thread entry must match `thr_\d+` or `*new-topic*` (the unclosed `*new-topic` is normalized to it); extra tags are ignored with a `topic.warning`. Anchors are normalized (SPEC §2.7.2) and enter the symbol history as `model`-sourced symbols, never directly as anchors: the projection picks the anchors and caps their count. An unknown or other-project thread id is resolved or declined (SPEC §3.2). Anchor length is not checked (ROADMAP Near-term, "Anchor projection can emit an anchor `personant verify` rejects"). |
| Closing summary (the summarizer, SPEC §5.2) | Trimmed; an empty draft is not applied. The anchors written at close come from the projection, not the draft. Length is not checked before the write: `spine.entry-max-chars` is enforced only by `personant verify` at the history-repository write (ROADMAP Near-term, "Closing summaries are not length-bounded before they are written"). |
| The clusterer's grouping (SPEC §5.3) | Not built. |
| Model judgment on recall candidates (SPEC §3.4) | Not built: recall composes the symbolic and embedding signals only. |
| Memory-vetting review (SPEC §1.8) | Not built. |
| Lifetime tag (SPEC §3.10.8) | Not built (the keep/toss stage is deferred); SPEC fixes the fail-safe: a missing or malformed tag keeps the content. |

Recall scoring and loading a thread into the active set are deterministic (SPEC §3.4). The behavior- and process-memory (SPEC §1.8) write path is "Memory vetting"'s, which is not built.

