# personant — working principles
Full contract: AGENTS.md. This file is the drift-watch: rules quietly broken when AGENTS.md falls out of context.

## Conversational Tone
Concise. Competent. No unearned praise (e.g. "that's a sharp question" for every query.)
Reserve that language for moments of significant insight, intelligence, capability.

## Task management
- Whenever possible dispatch tasks to sub-agents to remain free for discussion, planning, and other interactive functions. Long spells of unavailability shut out the user's ability to multi-task across the current project needs.
- *INVARIANT* Never continue with a plan or process in response to a user question unless the prompt explicitly says to proceed.
  - If the user has an open question, this must be addressed before proceeding with a plan.
  - Tool use in order to gather data to answer the question is not disallowed unless already restricted by other instructions.

## Planning
While planning, read the primary sources the plan depends on — actual current files and state, not stale data or guesses. Finish that data-gathering before presenting the plan, not during execution: an approved plan runs to completion, so surface any blocker needing user intervention while planning — never let it be a mid-run discovery.

## DRY
- Two functions projecting the same shape is a smell. Keep the most capable; delete the others.
- Any value used in ≥2 places is a named constant — single-edit renames, never global search-and-replace.

## Build & validation
- Never `go build` or `go test` or any other go commands directly. Use the appropriate make target for the task e.g. `make build` or `make test`.
- **Two gates, never conflated (AGENTS.md "Two gates").** `make test` is the CHECKPOINT GATE — full suite, minutes, run *once before a commit*. It is NOT an edit gate: do not re-run it between edits. To verify an edit landed, use the EDIT GATE — `make build` (compile) + `make test-run PKG=<pkg> RUN=<regexp>` (only the touched test). Commit at checkpoints, not per-edit. Spawned coders inherit this via AGENTS.md; a task spec that has a coder re-run the full suite per edit (or revert→full-test→restore→full-test) is a process error — scope it to `test-run`.

## Coding
- Use coder agents for coding work unless directed otherwise. Ensure coder agents receive AGENTS.md to understand full contract when working.
- In the cases when you are asked to do direct coding work, read the appropriate coding agent definition and AGENTS.md if it is not fresh in context. It is crucial to maintain the invariants and contracts specified in those documents.

## Read the docs before ad-hoc grepping
Before discovering a capability by grepping source — a harness/API/tooling
feature, where a subsystem lives, how something is wired — check **AGENTS.md**
(API extensions, `test_inference.sh`/`.py` env knobs, `test_*_equiv.sh`, tooling)
and **ARCHITECTURE.md** (subsystem/source map) first. They summarize these; grep
source only to confirm wiring once the docs point you at the right place. Example
miss: hunting apiserver/engine code for how `"stateless"` is passed when it's
listed in AGENTS.md's API extensions — and the documented "stateless is the only
`ForwardCaptures` mode" fact answers *why* the capture path is stateless.
