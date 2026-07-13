package scenarios

// Metric-key registry — the single source of truth for the metric KEY
// strings that cross the harness↔sim seam (sim-harness review burndown #1).
//
// These keys are WRITTEN by the harness (package scenarios — harness_run.go's
// h.Metrics.Counter/Record/Set calls) and READ back by the sim summary/gate
// code (package sim, which imports this package). Before this registry the
// keys were declared independently on each side (a local `const metric* =
// "..."` block in harness_run.go AND a parallel block in sim/workload.go, plus
// raw "..." string literals in sim's tests). Nothing forced the copies to
// agree: a writer-side rename compiled green and silently made the reader's
// CounterValue("...") read 0 — zeroing a measurement series AND any gate on it.
//
// Owning the strings here, referenced by BOTH writer and reader, makes a
// rename a single edit that either updates every reference or fails to compile.
//
// SCOPE: only the cross-seam keys live here. Keys declared AND consumed
// entirely within package sim (the §4.3 perf-decay series — coarse_size /
// fine_chunks / cosine_ops / latency / flush / hop / depth / coherence) are
// already single-source within that package and are NOT mirrored here — moving
// them would not close a seam.
//
// JSON struct tags in the sim daily-record cannot reference a const (a Go
// language limitation), so any daily-record tag encoding one of these keys is
// pinned to its registry const by TestMetricKeyTagsMatchRegistry (sim package),
// so a registry rename that forgets a tag fails loudly.
const (
	// Sleep-cycle keys (#108). MetricSleepCycles counts sleep/consolidation
	// passes (one per day-off); the GitDirBytes trio are the substrate .git
	// footprint before/after the gc plus the total reclaimed across cycles —
	// the gc-reclamation proof.
	MetricSleepCycles          = "sleep_cycles"
	MetricGitDirBytesPreGC     = "git_dir_bytes_pre_gc"
	MetricGitDirBytesPostGC    = "git_dir_bytes_post_gc"
	MetricGitDirBytesReclaimed = "git_dir_bytes_reclaimed"

	// MetricRecallIntraTreeRebuildCalls counts within-thread summary-tree
	// (re)builds across the run — the design §7.2 / fork-F-B trip-wire (#111).
	MetricRecallIntraTreeRebuildCalls = "recall_intra_tree_rebuild_calls"

	// §6.5 fine-tier flush cost — the embed-call rate the debt-cap + dormancy
	// triggers pay (§6.2/§9.2). MetricRecallIndexFlushCalls is the run-total
	// number of flushes the runtime fired; MetricRecallIndexFlushChunks the
	// total turn-excerpts those flushes carried into the fine tier. OBSERVED,
	// not modeled (#126): the harness folds each session's
	// turn.State.FlushCalls/FlushChunks into these run-totals (a restart rebuilds
	// State, so the per-session counts are drained before the State is discarded),
	// rather than dividing scrolled-out chunks by a mirror of
	// turn.EmbeddingDebtCap. They cross the harness↔sim seam (harness writes the
	// counter, the sim summary/daily-record reads it), so they live in the
	// registry like the other cross-seam keys.
	MetricRecallIndexFlushCalls  = "recall_index_flush_calls"
	MetricRecallIndexFlushChunks = "recall_index_flush_chunks"

	// W1 descent-vs-flat quality measure (#111 / design §7.1; gate→measure in
	// #119). MetricRecallIntraDescentDivergence is the run-total top-Kf leaf set
	// difference between the summary-tree descent and the flat scan;
	// MetricRecallIntraDescentProbes is its denominator (W1 probes evaluated).
	MetricRecallIntraDescentDivergence = "recall_intra_descent_divergence"
	MetricRecallIntraDescentProbes     = "recall_intra_descent_probes"

	// W1 per-run classification tally (#111 §7.1 diagnostic): of the divergence
	// probes, how many were a genuine recall loss (a strictly-better leaf
	// pruned), an equal-cosine tie-boundary substitution, or a tree-mismatch
	// (the flat scan ranked a leaf the tree does not contain). All 0 on a mock
	// run (no divergence occurs).
	MetricRecallIntraW1StrictMiss   = "recall_intra_w1_strict_miss"
	MetricRecallIntraW1Tie          = "recall_intra_w1_tie"
	MetricRecallIntraW1TreeMismatch = "recall_intra_w1_tree_mismatch"

	// MetricRequestPromptTokens is the per-turn provider-reported prompt-token
	// count for the FULLY-assembled model request (system prompt + replayed
	// history + userInput + tool-result deltas) — turn.TurnInfo.PromptTokens,
	// sourced from model.Response.Usage.PromptTokens. The harness records ONE
	// sample per turn into this histogram, but ONLY on a live-inference run
	// (h.liveClient != nil): on the mock path the value is the mock's canned 8
	// (mockllm.go), which is true-by-construction and cannot stand in for the
	// token-ceiling gate (X4-PROD FM3), so it is deliberately NOT recorded there
	// — keeping the mock metrics blob byte-identical (B1+X4 rung invariant 8).
	// The token-ceiling gate (tokenCeilingAsserts) reads max(this) and asserts
	// <= the configured ceiling; the rung also reports max + P99 (the FM4
	// non-vacuity forensic — a max far below the ceiling means the large-input
	// workload never stressed the assembled request). Cross-seam: harness writes,
	// sim gate reads.
	MetricRequestPromptTokens = "request_prompt_tokens"
)
