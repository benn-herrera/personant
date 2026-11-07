### All clock reads use `internal/clock`

Direct reads of the `time` package clock
(`time.Now`, `time.Since`) are forbidden outside `internal/clock`. Use `clock.Timeline()` for
simulated-world timestamps and `clock.Profiling()` / `clock.Since()` for real-time and latency
measurement.
