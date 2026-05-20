// Package clock centralizes every clock read in the runtime so that the
// six-month simulation can run on a logical timeline while latency
// profiling stays on the real wall clock.
//
// Two distinct clocks are exposed:
//
//   - Timeline is the simulated-world timestamp. It is real (time.Now) by
//     default but overridable, so a simulation can drive it from a logical
//     clock.
//   - Profiling (and the Since helper) is the real, uncorruptible clock.
//     It is never overridable and is used for latency measurement and for
//     any timestamp that must reflect real wall time even while a
//     simulation has overridden Timeline.
//
// Invariant: direct reads of the time package clock (time.Now, time.Since)
// are forbidden outside this package. Use Timeline for simulated-world
// timestamps; Profiling/Since for real-time and latency.
package clock

import "time"

// timelineFn is the source for Timeline. nil means "use time.Now".
// It is set at process init and must not be re-set mid-run from multiple
// goroutines; SetTimeline carries no synchronization for that reason.
var timelineFn func() time.Time

// Timeline returns the simulated-world timestamp: time.Now by default, or
// the installed override during a simulation.
func Timeline() time.Time {
	if timelineFn != nil {
		return timelineFn()
	}
	return time.Now()
}

// SetTimeline installs fn as the Timeline source and returns a restore
// closure that reverts to whatever source was in effect before. Passing
// nil reverts Timeline to its default (time.Now).
//
// The override is process-global. Every caller (notably tests) MUST call
// the returned restore closure when done, or the override leaks into
// subsequent work.
func SetTimeline(fn func() time.Time) (restore func()) {
	prev := timelineFn
	timelineFn = fn
	return func() { timelineFn = prev }
}

// ensure that time.Time can't casually/accidentally be used in Since()
type ProfilingTime struct {
	t time.Time
}

func (this ProfilingTime) Format(fmt string) string {
	return this.t.Format(fmt)
}

func (this ProfilingTime) UTC() ProfilingTime {
	return ProfilingTime{t: this.t.UTC()}
}

// UnixNano returns the real wall-clock time expressed as nanoseconds
// since the Unix epoch. Routed through here (rather than calling
// time.Now().UnixNano() at the call site) so the package retains its
// single-point invariant: every clock read in the runtime goes through
// internal/clock. The reading is real wall time and is not affected by
// any Timeline override.
func (this ProfilingTime) UnixNano() int64 {
	return this.t.UnixNano()
}

// Profiling returns the real wall-clock time. It is never overridable;
// use it for latency measurement and for timestamps that must stay
// real-world even while a simulation has overridden Timeline.
func Profiling() ProfilingTime {
	return ProfilingTime{t: time.Now()}
}

// Since reports the real-clock duration elapsed since t. It is the
// elapsed-measurement helper, provided here so that banning time.Since
// elsewhere has no backdoor.
func Since(t ProfilingTime) time.Duration {
	return time.Since(t.t)
}
