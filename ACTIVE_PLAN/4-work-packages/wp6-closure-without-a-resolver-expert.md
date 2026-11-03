### WP6: Closure without a resolver (expert)

- `internal/turn/closure.go`, `turn.go`, `commands.go`: the closure rows of section 1 ("Flow
  control"). The surviving write keeps `applyClosureResolution`'s body for the retire case: the
  projection, the spine and frontmatter mirror, the structural count, B/C eviction and
  `retire.complete`. It loses the `ClosureResolution` parameter and the `WIP`/`defer`/`edited`
  branches. `ackKind` stays: `recall.go:441` still uses it, and WP7 removes it.
- `closureDeferUntil`, `closurePendingQueued`, `ClosureNotice` sites outside `closure.go`:
  `turn/lru.go:288,291`, `turn/rollback.go:53,71,91` (the field, its snapshot and restore),
  `turn/rollback_test.go:113–114`, `turn/turn.go:161,167,309–324,395,488,490,513`,
  `turn/recall.go:183` (comment), `scenarios/restart.go:203` (comment).
- Harness: the WP6 rows of "Harness and sim". In `installAckPolicy` delete only
  `state.ClosureAckMode = turn.AckModeAlways` (`harness_setup.go:224`); WP7 renames the function.
  `store/init.go` `seedDefaultsMD`: drop `closure.ack-mode: auto` (the template is rewritten on
  every init). Scenario tests that scripted a closure outcome assert the autonomic result; a test
  whose subject was a removed outcome (`wip`, `defer`, the queue, the drain) is deleted, and the
  coder's report names the surviving test that covers the retire path.
- Tests: new and rewritten tests in `closure_test.go` are named `TestClosure_*`: a decayed
  thread with a draft retires `resolved` with no resolver installed; an empty or failed draft is
  retried (rename `TestAutoAccept_EmptyDraftIsNotAccepted` to
  `TestClosure_EmptyDraftIsRetried`); nothing retires without a curator.
- Re-run `git grep -l -w -E 'ClosureResolver|ClosureOutcome|ClosureAck|AckModeAlways|PendingClosures|DrainClosures|ManualClosure|closureDeferUntil|closurePendingQueued|ClosureNotice|OnAutoClosed' -- '*.go'`
  (today `turn/{checkpoint_test,closure,closure_test,commands,commands_vocab_test,lru,recall,rollback,rollback_test,toolloop,turn}.go`,
  `scenarios/{harness,harness_resolvers,harness_run,harness_setup,restart,scenarios_test}.go`,
  `scenarios/sim/{campaign,intra,wander,workload}.go`; `toolloop.go` is gone after WP3) and edit
  each.
- Contract rows marked 6.
- Done-line: `just build`; `just test-run ./internal/turn 'Closure|Decay|Checkpoint|Rollback'`;
  `just test-run ./internal/scenarios`; the re-run grep → empty. Checkpoint; `just sim`. The
  dispatching session diffs this run's metrics JSON against WP5's
  (`command ls -d test/rundata/sim-workload-seed*-dur168h0m0s.* | tail -2`; the file inside each
  is `<directory name>.metrics.json`). The expected result is no change in closure counts; any
  change goes in the commit message with its cause.

