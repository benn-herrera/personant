## Anti-patterns to push back on

| Anti-pattern | Why it's wrong | Reach for instead |
|---|---|---|
| "Let the model decide where to put state" | Pushes authoritative state into the model | Deterministic code holds state; the model drafts, deterministic code validates and writes |
| "Re-implement something opencode owns" (tools, permissions, UI, session storage, providers) | Wheel re-invention; the project's one hard problem is context | Conform to opencode; take over only what enters the request, through a hook. Elsewhere, borrow existing designs and use well-maintained imports |
| "Put memory logic in the plugin" | Splits the single writer; the plugin cannot be tested with the sim | The plugin forwards and applies; all state and logic stay in Go |
| "Use SQLite for the thread registry" | Storage violation; loses inspectability + git-diffability | Text + git; a derived index only, if scale forces one (ROADMAP Far-horizon). |
| "Have the model run `git commit` on the store working tree" | Mixes model-controlled with automatic git | The service commits automatically |
| "Add this dep speculatively" | Approved-when-earned violation | Wait until consumer is real; bring dep in *with* consumer |
| "Tests can come later" | Asymmetric-cost violation | Measurement infrastructure built alongside features, not after |
| "Mock model is too narrow; let's use a real model in tests" | Adds non-determinism, network dependency, cost | Mock with scripted/generated responses as the default loop; a real model or embedder only in the opt-in live runs (`just integration-test`, the `sim` live toggles, `just sim-completeness-rung`, `just sim-tokenceiling-rung`) |
| "Speculatively prefetch threads we *might* need" | Violates "every loaded thread is needed" | Model emits topic tag; memory service fetches what was named |
| "Shorten the topic-tag instruction to save tokens" | While the in-band tag stands (Open: U3), emission is the load-bearing recognition signal | The system prompt's topic-tag directive is non-optional |
| "Reset the store working tree when it's dirty on startup" | A markerless-dirty tree is a legitimate hand-edit; resetting on dirtiness destroys user work (and, when it is not yet in the history repository, a whole day) | Make `reset --hard` conditional on the operation marker/journal signal, never on dirtiness; absorb hand-edits forward (see "Crash recovery and the turn and history repositories", case 3) |
| "Commit durability writes async / deferred (flush later)" | A commit lagging its authoritative writes reintroduces the torn-window ambiguity the operation marker/commit protocol exists to eliminate | Commit ordering is synchronous, always; cost comes from cheaper steps (scoped add, disposable turn repository), never from widening the loss window |

---

