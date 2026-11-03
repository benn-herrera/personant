### WP4: Drop the dead provider parameter (mechanical)

- Remove `turn.State.Provider` (`turn.go:93`, its assignment at `:483`) and the
  `provider memops.Provider` parameter of `turn.NewState` and `turn.LoadSession`. Its value is
  never read (E5).
- Callers: re-run `git grep -l -E 'NewState\(|LoadSession\(' -- '*.go'`. After WP3 the answer is
  E5's list without `internal/turn/toolloop_test.go`. Edit every listed file.
- `internal/scenarios`: delete `Harness.Provider` (`harness.go:731`), its assignment
  (`harness_setup.go:111`), the `memops.Provider` literal and the sentinel `providers.toml`
  write in `harness_setup.go`, and the `h.Provider` argument at `harness_run.go:705`.
- Done-line: `just build`; `just test-run ./internal/turn`; `just test-run ./internal/scenarios`;
  `git grep -n -w 'memops\.Provider' -- internal/turn internal/scenarios ':!internal/scenarios/sim/sim_live_test.go'`
  → empty. Checkpoint.

