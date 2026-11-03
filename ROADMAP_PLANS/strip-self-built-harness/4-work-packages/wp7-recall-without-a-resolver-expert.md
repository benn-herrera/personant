### WP7: Recall without a resolver (expert)

- `internal/turn/recall.go`, `turn.go`: the recall rows of section 1 ("Flow control").
  `acceptRecallCandidate` takes a `measure.Result` and stays the one accept path (invariant 5).
  `ackKind`, `ackHuman`, `ackAuto` go.
- Harness: the WP7 rows of "Harness and sim". In `installAckPolicy` delete
  `state.RecallAckMode = turn.RecallAckAlways`, rename the function `installCurator` (callers
  `harness_setup.go:177`, `harness_run.go:720`), and replace its doc comment with "installCurator
  installs the harness's deterministic curator on a State, at construction and after a simulated
  relaunch, since turn.LoadSession builds a State without one." Add the `StepFeedback` field for
  fetched thread IDs, filled from `recall.accept` lines the way `RecallMatchFireIDs` is filled,
  and apply the IDs in `advanceRuntimeMirror` so the BD-4 cross-check holds (invariant 6).
- Tests: new and rewritten tests are named `TestRecall_*`: an auto-band candidate is fetched with
  no resolver; a below-bar candidate is not fetched; a resident non-intra candidate is skipped;
  accept suppression still holds for intra hits. Delete tests whose subject was the offer, the
  decline reason or the ack mode.
- Re-run `git grep -l -w -E 'RecallResolver|RecallOffer|RecallResolution|RecallNotice|RecallAck|RecallAckMode|DeclineReason|DeclineNotRelevant|OnAutoRecalled|putOffer|describeCandidates|ackKind|ackHuman|ackAuto|installAckPolicy' -- '*.go'`
  (today `scenarios/{harness,harness_resolvers,harness_run,harness_setup}.go`,
  `scenarios/sim/{campaign,intra,wander,workload}.go`,
  `turn/{closure,closure_test,commands,display,embed_recall_test,recall,recall_test,toolloop,turn}.go`;
  after WP3 and WP6, `toolloop.go` is gone and `closure*.go`/`commands.go` should no longer match;
  `display.go` matches on its comment at line 11) and edit each.
- Contract rows marked 7.
- Done-line: `just build`; `just test-run ./internal/turn 'Recall|Embed'`;
  `just test-run ./internal/scenarios`; the re-run grep → empty. Checkpoint; `just sim`, diffed
  against WP6's run as in WP6. This package is expected to move recall, engagement and
  `derived_from` metrics; the coder's report gives each gate's value before and after, for the
  commit message. A failing gate stops the package and goes back to the dispatching session; it
  is diagnosed, not loosened.

