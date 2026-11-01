#### Banding

Each surfaced candidate falls in exactly one band, by its score against its OWN tier's
threshold (layer 1 is a Jaccard set overlap, layers 2/3 are cosines):

- **AUTO** — score ≥ `recall.symbolic-auto-threshold` (0.65) for a symbolic-scored candidate, or ≥
  `recall.cosine-auto-threshold` (0.75) for an embedding- or intra-thread-scored one. The thread is
  promoted through the SAME fetch path an accepted candidate takes, with no prompt and ONE user-visible
  line, `recalled thr_N: <display name> — <gist>` (the standing `thr_N: display — gist` topic
  reference, so the fetch is verifiable against `/topics`). Logged `recall.accept ... ack=auto`; the
  `recall_fires` increment ([§2.2](../../2-data-model/2.2-spine-record-schema-spine-jsonl/index.md)) applies as for any accept.
- **ASK** — surfaced but below the auto bar. ONE offer block per turn; per candidate: the thread's
  [§2.2.2](../../2-data-model/2.2-spine-record-schema-spine-jsonl/2.2.2-display-form.md) display name (never a bare `thr_N`), a gist (the spine summary, else the projected anchors
  — never blank, never a raw id), WHY it matched (the matched symbols, or for an intra hit the
  matched turn numbers), and a short tier label with the score. The question states what accepting
  does: one candidate → `pull into context? [y]es / [n]o`; several → a selection among the enriched
  lines (**Open:** U4 — its form). No decline-reason question in this mode.
- **Below the surface threshold** — never surfaces.

Under `banded`, a candidate already resident in Layer B is answered by the runtime itself:
`recall.decline reason=already-known`, no prompt, no line. An intra-thread hit is exempt (its
subject is the engaged thread's early content).

