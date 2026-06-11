package sim

// Within-session interleaving telemetry (SPEC §9.1 "too coherent" concern).
// These accumulators are pure post-emission bookkeeping folded from each
// session's emitted bufSteps (slotIdx / engagedIdx): they draw no rng and do
// not influence control flow, so they leave the canonical step stream a pure
// function of (Seed, Duration, Corpus). The test folds the per-session and
// per-dwell samples into the metrics blob post-run.

// beginInterleaveSession resets the per-session interleaving accumulators.
// Called once at the top of every runSession.
func (g *generator) beginInterleaveSession() {
	g.sessSlots = map[int]struct{}{}
	g.sessThreads = map[int]struct{}{}
	g.sessTurnCount = 0
	g.dwellPrev = 0
	g.dwellLen = 0
	g.dwellActive = false
}

// observeInterleave folds one just-emitted step into the per-session
// interleaving accumulators. Pure bookkeeping: no rng, no control flow.
func (g *generator) observeInterleave(bs bufStep) {
	g.sessSlots[bs.slotIdx] = struct{}{}
	g.sessThreads[bs.engagedIdx] = struct{}{}
	g.sessTurnCount++

	// Topic dwell: extend the current run while engagedIdx is unchanged;
	// otherwise flush the finished run and start a new one.
	if g.dwellActive && bs.engagedIdx == g.dwellPrev {
		g.dwellLen++
		return
	}
	if g.dwellActive {
		g.topicDwellRuns = append(g.topicDwellRuns, g.dwellLen)
	}
	g.dwellPrev = bs.engagedIdx
	g.dwellLen = 1
	g.dwellActive = true
}

// finalizeInterleaveSession pushes the per-session summary samples and
// flushes the final in-progress dwell run. Called once per non-empty
// session (a session that emitted no steps contributes no samples).
func (g *generator) finalizeInterleaveSession() {
	g.sessionDistinctSlots = append(g.sessionDistinctSlots, len(g.sessSlots))
	g.sessionDistinctThreads = append(g.sessionDistinctThreads, len(g.sessThreads))
	g.sessionTurns = append(g.sessionTurns, g.sessTurnCount)
	if g.dwellActive {
		g.topicDwellRuns = append(g.topicDwellRuns, g.dwellLen)
	}
}
