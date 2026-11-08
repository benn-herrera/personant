### opencode clone: referent only

The opencode source clone at `../opencode` is read-only. Never edit it and never move it off
the installed release's tag; it moves only in the change that updates the `sim-opencode` version
check (ARCHITECTURE "Plugin route", "Pinned release").
- **Open:** U11 — whether to adopt the rule: never place external state (an agent's data home,
  telemetry) inside the store directory or a test memory store, because crash recovery
  quarantines untracked files. opencode's data directory is already excluded (ARCHITECTURE "One inspection location").
- **Spawned processes: self-limiting, or verified dead** (origin: commit 4fcb0e5). Binding on any
  background process an agent starts — load generators, watchers, servers, opencode servers,
  children of stress harnesses:
  1. **Fail safe, not fail hot.** Every spawned process bounds its own lifetime — a loop with an
     iteration cap, or a `timeout N` wrapper. A bare `while :; do :; done &` has "spin forever" as
     its failure mode.
  2. **Kill by collected PIDs, never `$(jobs -p)`.** Record `$!` at spawn and kill that list. In
     zsh, `$(jobs -p)` expands in a subshell whose job table is empty, so `kill $(jobs -p)` is a
     silent no-op.
  3. **Verify the kill.** A post-cleanup count probe (`ps ... | wc -l`) returning 0 is part of the
     task's definition of done. Nonzero is a failed gate, not a warning.
  4. **Never silence cleanup stderr.** A cleanup command's stderr is the only witness when it
     breaks; `kill ... 2>/dev/null` hides the error that exposes a no-op.
  5. **Prefer in-process contention.** `go test -cpu 8,8,...` and `-count` create scheduler pressure
     inside the test process and leak nothing; external burners are a last resort and carry rules
     1–4.
