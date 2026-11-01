### Tense

In this section and the ones that follow it, present tense states the production
design. Facts about the probe code are marked "the probe"; the production service and plugin are
not built. The probe code lives on branch `probe-path` and is not on the build branch: every path,
recipe and executor this document names as the probe's (`opencode-plugin/index.ts`,
`internal/opencodehook/`, `internal/turn/probe_phases.go`, `internal/scenarios/opencode/`, the
`phases` and `opencode` sim executors, `sim-opencode`, `probe-smoke`, `probe-resume`,
`probe-deps`) resolves there (PR §§6–7). ROADMAP A2 brings the baseline comparator across; A3 and
A5 rebuild the rest.

Personant runs under unmodified opencode as two pieces:

- **Plugin** (`opencode-plugin/`). TypeScript, loaded by opencode in its own Bun process. An
  adapter that holds no memory state: it forwards hook inputs and events to the service and applies
  the answers, and imports only what the host supplies, so all memory logic is in Go (IO §2.1).
- **Memory service** (Go). The only writer Personant provides for memory state (see "Autonomic vs
  LLM-controlled"); it composes every model request. The plugin reaches it over loopback HTTP +
  JSON with `fetch`, because Bun's `node:net` and child-process APIs are unverified from plugin
  code (IO §5, item 6). Several opencode servers sharing one service: see "Concurrent sessions".

Data flow: opencode's session runner calls the plugin's hooks and emits events; the plugin forwards
each to `internal/opencodehook` over loopback HTTP + JSON; the handler drives an `internal/turn`
phase, which reaches storage through the `memops` port; the answer returns the same way.

