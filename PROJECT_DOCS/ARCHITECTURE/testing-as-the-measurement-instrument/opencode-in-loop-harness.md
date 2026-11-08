### opencode-in-loop harness

The prototype extends the existing harness; it does not build a second one. One Go test process hosts
the workload generator, the driver, the mock model endpoint and the memory service, and spawns one
opencode server; the plugin, loaded by opencode, calls back into the test process.

- The workload generator emits steps to the driver, which calls opencode's HTTP API.
- opencode's plugin calls the memory service in the test process over HTTP + JSON.
- opencode's provider `personant-mock` (the mock provider in the harness's isolated opencode config) calls the mock model endpoint, also in the test process,
  over the OpenAI-compatible wire.

Because the memory service runs inside the test process, the harness keeps direct access to the
event log and the memory store; invariants, recall-fidelity scraping and metrics run as they do for
the in-process turn.

- **Turn executor.** `-sim.turn-executor` selects `runwithinfo` (default, the in-process turn),
  `phases` (`RunTurnInProcess`, the prototype phases) or `opencode` (`internal/scenarios/opencode`,
  which replaces only the turn-execution step of `runStep`). `just sim-fidelity EXECUTOR=phases`
  proves the phases reproduce the committed baselines; `just sim-opencode` drives every turn
  through the installed opencode and is not part of `just test`.
- **Mock model endpoint.** `model.MockServer` reports usage so opencode's token accounting sees
  real numbers.
- **Isolation.** The driver points `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME` and
  `XDG_CACHE_HOME` under the run's data home, so the user's opencode database is never touched, and
  writes the isolated opencode config (the mock provider, `compaction.auto: false`, the plugin
  entry).
- **Workload split.** The generator is reused as is: user input goes through the driver,
  the scripted model responses are installed in the mock, and `PreEvents` (`fs.read`, `fs.write`,
  `fs.commit`) reach the service through opencode's own tools against a fixture workspace.
- **Determinism.** Commit author and committer dates are pinned to the memory system's clock
  wherever run-to-run repeatability is measured, so a commit hash repeats across runs (probe results
  §6, lesson 7).
- **Metric requirements.** `oc_missed_deltas` and `oc_uncomposed_requests` must be 0 (probe results
  §2), among the `oc_*` keys in `metric_keys.go` (hook round-trip time, step overhead, opencode
  message row count, database size, hook fail-open counts). `oc_uncomposed_requests` is counted at
  the mock.
- **Prototype targets.** `just probe-smoke` runs one handwritten turn; `just probe-resume` stops or
  kills opencode and the service mid-session and resumes (resume probe).

**Open:** prototype-code retention — whether `model.MockServer` (the mock served as an
OpenAI-compatible endpoint) stays on the main line, as a run with opencode in the loop requires
(SPEC §9.2). The baseline comparator (`sim-baseline`, `sim-fidelity`, the committed baselines) is
named for keeping; the rest of the opencode driver and executor is undecided.

**Open:** whether the §9.1 acceptance criterion needs an opencode-in-loop run configuration.

---

