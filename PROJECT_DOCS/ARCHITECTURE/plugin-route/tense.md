### Tense

In every Plugin route leaf, present tense states the production
design. Facts about the prototype, the throwaway implementation built to test this route, are marked
"the prototype"; the production service and plugin are not built. The prototype lives on branch
`probe-path` and is not on the build branch: every path, recipe and executor this document names as
the prototype's (`opencode-plugin/index.ts`, `internal/opencodehook/`,
`internal/turn/probe_phases.go`, `internal/scenarios/opencode/`, the `phases` and `opencode` sim
executors, `sim-opencode`, `probe-smoke`, `probe-resume`, `probe-deps`) resolves there (probe
results §§6–7). Carrying the baseline comparator (the `sim-baseline` and `sim-fidelity` recipes and the committed baselines) to the build branch is ROADMAP A2; rebuilding the rest is A3 and A5.

An **Open:** line marks a decision not yet made, and a tag such as U7 names it (CONVENTIONS "Document conventions"); ROADMAP "Pending decisions" lists the U-tags, and ids such as A2 or B9 are ROADMAP items.

Personant runs under unmodified opencode as two pieces:

- **Plugin** (`opencode-plugin/`). TypeScript, loaded by opencode in its own Bun process. An
  adapter that holds no memory state: it forwards hook inputs and events to the service and applies
  the answers, and imports only what the host supplies, so all memory logic is in Go
  (integration-options report §2.1). It keeps only transport counters, none of them memory: the sequence number it stamps on each forwarded event, because RPCs can reach the service out of order (see "Ordering and correlation"), and a fail-open count the opencode-in-loop harness reads (`oc_*` hook fail-open counts). The prototype also carries round-trip timings for the harness. All of it lives in the plugin process and starts at zero on a reload.
- **Memory service** (Go). The only writer Personant provides for memory state (see "Automatic vs
  model-controlled"); it composes every model request. The plugin reaches it over loopback HTTP +
  JSON with `fetch`, because Bun's `node:net` and child-process APIs are unverified from plugin
  code (integration-options report §5, item 6). Several opencode servers sharing one service: see
  "Concurrent sessions".

Data flow: opencode's session runner calls the plugin's hooks and emits events; the plugin forwards
each to the memory service over loopback HTTP + JSON; the handler drives an `internal/turn`
phase, which reaches storage through the `memops` port; the answer returns the same way.

