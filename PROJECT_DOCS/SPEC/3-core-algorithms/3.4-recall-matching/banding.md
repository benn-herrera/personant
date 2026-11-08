#### Banding

Each surfaced candidate falls in exactly one band, by its score against its OWN signal's
threshold:

- **Auto-fetch band** — score ≥ `recall.symbolic-auto-threshold` (0.65) for a symbolic-scored
  candidate, or ≥ `recall.cosine-auto-threshold` (0.75) for an embedding- or intra-thread-scored
  one. The thread is loaded through the SAME fetch path an accepted candidate takes, with no
  question and ONE user-visible line, `recalled thr_N: <display name> — <gist>` (the standing
  `thr_N: display — gist` thread reference, so the fetch is verifiable against `/topics`; the
  **gist** is the thread's registry summary, else its projected anchors). Logged
  `recall.accept ... ack=auto`; the `recall_fires` increment ([§2.2](../../2-data-model/2.2-thread-registry-record-schema-spine-jsonl/index.md)) applies as for any accept.
- **Ask band** — surfaced but below the auto-fetch bar. ONE offer block per turn; per candidate: the
  thread's [§2.2.2](../../2-data-model/2.2-thread-registry-record-schema-spine-jsonl/2.2.2-display-form.md) display name (never a bare `thr_N`), a gist (the thread registry summary, else the
  projected anchors — never blank, never a raw id), WHY it matched (the matched symbols, or for an
  intra hit the matched turn numbers), and a short signal label with the score. The question states
  what accepting does: one candidate → `pull into context? [y]es / [n]o`; several → a selection
  among the enriched lines (**Open:** U4 — its form). No decline-reason question in this mode.
- **Below the surface threshold** — never surfaces. (The surface threshold is each signal's lowest
  bar: `recall.symbolic-threshold` for symbolic scores, `recall.cross-project-threshold` across
  projects; the cosine signals' floor is not a directive key.)

Under `banded`, a candidate already resident in the active set is answered by the memory service
itself: `recall.decline reason=already-known`, no question, no line. An intra-thread hit is exempt
(its subject is the engaged thread's early content).

