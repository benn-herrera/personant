## Anti-patterns to push back on

If you see one of these proposed (or are about to write it), stop and surface the concern:

| Anti-pattern | Why it's wrong | Reach for instead |
|---|---|---|
| "Let the LLM decide where to put state" | Pushes canonical state into the LLM | Deterministic code holds state; the LLM drafts, deterministic code validates and writes |
| "Re-implement something opencode owns" (tools, permissions, UI, session storage, providers) | Wheel re-invention; the project's one hard problem is context | Conform to opencode; take over only what enters the request, through a hook. Elsewhere, borrow existing designs and use well-maintained imports |
| "Put memory logic in the plugin" | Splits the single writer; the plugin cannot be tested with the sim | The plugin forwards and applies; all state and logic stay in Go |
| "Use SQLite for the spine" | Substrate violation; loses inspectability + git-diffability | Text + git; SQLite stays in reserve as a *derived* index if scale forces it |
| "Have the LLM run `git commit` on the home tree" | Mixes LLM-controlled with autonomic git | The service commits autonomically |
| "Add this dep speculatively" | Approved-when-earned violation | Wait until consumer is real; bring dep in *with* consumer |
| "Tests can come later" | Asymmetric-cost violation | Measurement infrastructure built alongside features, not after |
| "Mock LLM is too narrow; let's use a real LLM in tests" | Adds non-determinism, network dependency, cost | Mock with scripted/generated responses; real LLM for `just integration-test` only |
| "Speculatively prefetch threads we *might* need" | Violates "every loaded thread is needed" | Model emits topic tag; runtime fetches what was named |
| "Shorten the topic-tag instruction to save tokens" | While the in-band tag stands (Open: U3), emission is the load-bearing recognition signal | The system prompt's topic-tag directive is non-optional |
| "Reset the worktree when it's dirty on startup" | A markerless-dirty tree is a legitimate hand-edit; resetting on dirtiness destroys user work (and, off primary, a whole day) | Gate `reset --hard` on the turn marker/journal signal, never on dirtiness; absorb hand-edits forward (Spec §4.5.8 cell 3) |
| "Commit durability writes async / deferred (flush later)" | A commit lagging its canonical writes reintroduces the torn-window ambiguity the marker/commit protocol exists to eliminate | Commit ordering is synchronous, always; cost comes from cheaper steps (scoped add, disposable daily), never from widening the loss window |

---

