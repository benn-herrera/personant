package sim

// Tag-fidelity instrumentation for the live-inference rung (A2 / SPEC §9.1;
// first datum: the 1d live run's turn-91 missing tag). It grades how well the
// LIVE model's topic-tag discipline matches the generator's PLAN INTENT across
// three series (keys in the scenarios metric_keys registry):
//
//   - re-engagement miss rate  (MetricTagFidelityReengageMissRate)
//   - spurious *new-topic* rate (MetricTagFidelitySpuriousNewTopicRate)
//   - anchor-emission overlap  (MetricTagFidelityAnchorOverlap)
//
// GROUND TRUTH is PLAN INTENT, never a shadow thread-id: under live inference
// the model engages/creates its OWN threads, so shadow ids are id-blind. The
// generator's canonical (zero-feedback) step stream is a pure function of
// (Seed, Duration, Corpus), so it is REGENERATED here and each canonical step
// carries the plan's intended tag (its scripted MockResponse) plus the prompted
// topic's symbol set and content. The model's ACTUAL per-turn tag outcome is
// read from the event log (thread.engaged / thread.created / topic.tag-missing).
//
// JOIN KEY (burndown 2026-07b item 4). Observed turns are segmented PER TURN
// on the event log's per-turn boundary line — `context.modified
// source=user.prompt`, emitted exactly once per turn (§2.8) — then joined to
// plan turns by (instant, ordinal-within-instant): the harness stamps every
// log line at pinnedClock == Step.At and executes steps in plan order, so the
// k-th observed turn at an instant is the k-th plan turn at that instant.
// (The previous instant-only grouping smeared same-second turns into one
// merged outcome — 380 tag-missing lines collapsed into 256 instant groups on
// the 2026-07-16 1d run.) An instant whose plan/observed turn counts disagree
// (an execution-time-injected refinement turn has no canonical At and can
// land in a plan second) is JOIN-AMBIGUOUS: its turns are counted and
// reported, never force-joined. A log with no boundary lines (pre-boundary
// format) degrades to the old instant-merged grouping, labeled DEGRADED.
//
// The re-engagement grader is HYBRID (ratified 2026-07-14):
//  1. symbolic anchor-overlap FAST PATH — the tagged thread's anchors/history
//     overlap the prompted topic's symbol set → hit, no embed call.
//  2. embedding adjudication of the NON-OVERLAPPING RESIDUE only — embed the
//     prompted content measurement-side (the grader's OWN embedder handle, never
//     the runtime recall stack) and judge RANK-based (C.6: ranking is
//     drift-invariant, absolute thresholds are not): the tagged thread is a hit
//     iff it is the top/near-top (rank ≤ tagFidelityRankK) cosine match among
//     live threads for the prompted content. A rank sitting on the k/(k+1)
//     boundary with a sub-epsilon cosine gap is BORDERLINE — logged with its
//     ranks, binned separately, never forced into hit/miss. With no reachable
//     embedder the residue is UNADJUDICATED (counted, logged, never guessed) so
//     the symbolic-fast-path metrics still emit.

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	pnlog "personant/internal/log"
	"personant/internal/memops"
	"personant/internal/prompt"
	"personant/internal/scenarios"
	"personant/internal/store"
)

// tagFidelityRankK is the near-top rank band for the residue rank adjudication
// (C.6 embedding-ranking finding: the true topic is the #1 cosine match ~73% of
// the time and within top-3 ~92%). A tagged thread ranking within the top-K
// cosine matches for the prompted content is a HIT: "top or near-top". K=3
// captures the ~92% near-top mass without letting a mid-pack match pass.
const tagFidelityRankK = 3

// tagFidelityBorderlineEps is the cosine gap, at the k/(k+1) rank boundary,
// below which a residue adjudication is BORDERLINE rather than a decided
// hit/miss. C.6 says the RANK is drift-invariant, but a near-tie AT the boundary
// is exactly where rank is not decisive, so a boundary gap under this epsilon is
// escalated to the borderline bin (logged with ranks) instead of trusted. 0.01
// is a conservative near-tie margin on cosine.
const tagFidelityBorderlineEps = 0.01

// tagFidelity local (sim-internal) diagnostic gauge keys — the obs
// denominators and the borderline/unadjudicated bins that accompany the three
// registry headline series. These are declared AND consumed within package sim
// (recordTagFidelityMetrics writes, the summary/tests read), so per the registry
// SCOPE rule they stay local literals rather than crossing into metric_keys.go.
const (
	metricTagFidelityReengageIntent = "tag_fidelity_reengage_intent"
	metricTagFidelityHits           = "tag_fidelity_hits"
	metricTagFidelityMisses         = "tag_fidelity_misses"
	metricTagFidelitySpuriousCount  = "tag_fidelity_spurious_new_topic_count"
	// metricTagFidelityTagMissing is a PLAN-JOINED classification count — the
	// re-engagement-intent plan turns whose joined observed turn carried a
	// tag-omission marker — NOT the runtime's stream-level tag-missing count
	// (reportInferenceBehavior owns that; the two denominators differ).
	metricTagFidelityTagMissing    = "tag_fidelity_tag_missing_count"
	metricTagFidelityBorderline    = "tag_fidelity_borderline"
	metricTagFidelityUnadjudicated = "tag_fidelity_unadjudicated"
	metricTagFidelityAnchorObs     = "tag_fidelity_anchor_overlap_obs"
	// metricTagFidelityJoinAmbiguous counts re-engagement-intent plan turns at
	// instants whose plan/observed turn counts disagreed (injected turn in the
	// same second) — reported, never force-joined.
	metricTagFidelityJoinAmbiguous = "tag_fidelity_join_ambiguous"
	// metricTagFidelityJoinDegraded is 1 when the event log carried no
	// per-turn boundary lines and the grader fell back to instant-merged
	// grouping (old-log format) — the join is then smear-prone and the series
	// should be read as degraded.
	metricTagFidelityJoinDegraded = "tag_fidelity_join_degraded"
)

// graderEmbedder is the measurement-side embedding surface the residue rank
// adjudication needs. model.Embedder satisfies it; the grader builds its OWN
// instance from the sim-live provider config (never the runtime Recaller), and
// tolerates a nil handle (→ every residue turn is unadjudicated).
type graderEmbedder interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}

// planTurn is one canonical (plan-intent) turn: its simulated-instant join key,
// whether the plan intended re-engagement of a known topic (vs *new-topic*), the
// prompted topic's symbol set (the scripted tag's anchors), and the prompted
// content (for measurement-side embedding). gradeable is false for a canonical
// step whose scripted response carried no parseable plan tag — it still holds
// its ordinal slot in the (instant, ordinal) join so later turns at the same
// instant stay aligned, but nothing is graded for it.
type planTurn struct {
	instant   string
	reengage  bool
	gradeable bool
	topicSym  []string
	content   string
}

// observedTurn is the model's ACTUAL tag outcome for ONE executed turn — the
// lines between two per-turn boundary markers (`context.modified
// source=user.prompt`): the threads it engaged, whether it tagged *new-topic*
// (created a thread), whether it emitted no parseable tag, and whether the D6
// owner-default bound the turn (thread.tag-defaulted). instant is the turn's
// Step.At second (the boundary line's timestamp).
type observedTurn struct {
	instant      string
	engaged      []string
	created      bool
	tagMissing   bool
	tagDefaulted bool
}

// liveThread is one post-run spine thread the grader ranks against: its id, its
// symbol set (anchors ∪ history), and a representative text for embedding.
type liveThread struct {
	id   string
	sym  []string
	text string
}

// tagFidelityResult is the graded tally for the re-engagement series. borderline
// and unadjudicated are held OUT of the miss-rate denominator (which is
// hits+misses) so a run whose residue clusters at the rank boundary or lacks an
// embedder reports that fact rather than skewing the rate.
type tagFidelityResult struct {
	reengageIntent   int
	hits             int
	misses           int
	spuriousNewTopic int
	tagMissing       int // plan-joined omission classification (see metricTagFidelityTagMissing)
	borderline       int
	unadjudicated    int
	joinAmbiguous    int // reengage plan turns at instants whose plan/observed counts disagree
	unmatchedGroups  int // observed turns that joined no plan turn (refinement/injected)
}

// gradeTagFidelity is the PURE grader core: given the plan (ground-truth
// intent), the observed PER-TURN tag outcomes grouped by instant (in executed
// order), the live threads, and an (optional) embedder, it grades the
// re-engagement series. The join is (instant, ordinal-within-instant): every
// plan turn — gradeable or not — consumes one ordinal slot, so the k-th plan
// turn at an instant meets the k-th executed turn at that instant. An instant
// whose plan/observed counts disagree is join-ambiguous: nothing there is
// graded (counted in joinAmbiguous for the reengage turns). It is embedder-
// and I/O-injectable so the unit tests can drive every branch (fast-path hit,
// residue rank-hit, residue rank-miss, borderline, unadjudicated) with canned
// vectors. rankK/eps are passed in so a test can pin the boundary behavior.
func gradeTagFidelity(ctx context.Context, emb graderEmbedder, plan []planTurn,
	observed map[string][]observedTurn, live []liveThread, rankK int, eps float64) tagFidelityResult {

	var res tagFidelityResult
	liveByID := make(map[string]liveThread, len(live))
	for _, lt := range live {
		liveByID[lt.id] = lt
	}
	var threadVecs map[string][]float64 // lazily embedded on first residue

	// Plan-side per-instant turn counts, for the count-agreement check.
	planCount := make(map[string]int, len(plan))
	for _, p := range plan {
		planCount[p.instant]++
	}

	ordinal := make(map[string]int, len(planCount)) // next plan ordinal per instant
	for _, p := range plan {
		k := ordinal[p.instant]
		ordinal[p.instant]++
		if !p.reengage || !p.gradeable {
			continue // holds its ordinal slot; nothing to grade
		}
		obsAt, ok := observed[p.instant]
		if !ok {
			continue // no observed turn at this instant (skipped, counted below via caller diff)
		}
		if len(obsAt) != planCount[p.instant] {
			// Injected/refinement turn landed in this second: the ordinal zip
			// would mis-join. Report, never force.
			res.joinAmbiguous++
			continue
		}
		o := obsAt[k]
		res.reengageIntent++

		switch {
		case o.tagMissing || o.tagDefaulted:
			// Model omission FIRST: a D6 owner-defaulted turn emits BOTH
			// thread.engaged (for the runtime-bound owner) AND
			// topic.tag-missing (+ thread.tag-defaulted) at one instant. The
			// engaged id is the RUNTIME's recovery choice, not a model tag —
			// crediting it via the engaged case below would grade the
			// owner-default as a model hit. An omission is its own bin
			// (tagMissing) and always a miss, whatever the runtime bound.
			res.tagMissing++
			res.misses++
		case len(o.engaged) > 0:
			// Fast path: does any engaged thread's symbol set overlap the
			// prompted topic's symbol set? A non-empty normalized intersection
			// is the overlap signal (no embed call).
			if anyEngagedOverlaps(o.engaged, liveByID, p.topicSym) {
				res.hits++
				continue
			}
			// Residue: rank adjudication (needs the embedder).
			if emb == nil {
				res.unadjudicated++
				continue
			}
			if threadVecs == nil {
				threadVecs = embedLiveThreads(ctx, emb, live)
			}
			switch adjudicateRank(ctx, emb, p.content, o.engaged, live, threadVecs, rankK, eps) {
			case rankHit:
				res.hits++
			case rankMiss:
				res.misses++
			case rankBorderline:
				res.borderline++
			default: // rankUnadjudicated (embed failure)
				res.unadjudicated++
			}
		case o.created:
			// Plan wanted re-engagement; model spun a new topic.
			res.spuriousNewTopic++
			res.misses++
		default:
			// Observed group carried none of the tag markers (e.g. only
			// context.modified lines) — no tag outcome to grade. Do not count
			// it as a decided turn; back it out of the intent tally.
			res.reengageIntent--
		}
	}
	return res
}

// rankOutcome enumerates a residue rank adjudication.
type rankOutcome int

const (
	rankMiss rankOutcome = iota
	rankHit
	rankBorderline
	rankUnadjudicated
)

// adjudicateRank embeds the prompted content and judges whether the best-ranked
// engaged thread is a top/near-top cosine match among the live threads. Threads
// the model named that are not on the live spine cannot rank — an engaged set
// with none present ranks as a miss (the model referenced a thread off the
// surface). A best rank on the k/(k+1) boundary with a sub-eps cosine gap is
// borderline.
func adjudicateRank(ctx context.Context, emb graderEmbedder, content string, engaged []string,
	live []liveThread, threadVecs map[string][]float64, rankK int, eps float64) rankOutcome {

	if threadVecs == nil || len(threadVecs) == 0 {
		return rankUnadjudicated
	}
	qv, err := embedOne(ctx, emb, content)
	if err != nil || qv == nil {
		return rankUnadjudicated
	}

	// Cosine of every live thread that has a vector, sorted descending.
	type scored struct {
		id  string
		cos float64
	}
	scores := make([]scored, 0, len(live))
	for _, lt := range live {
		v := threadVecs[lt.id]
		if v == nil {
			continue
		}
		scores = append(scores, scored{id: lt.id, cos: cosine(qv, v)})
	}
	if len(scores) == 0 {
		return rankUnadjudicated
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].cos > scores[j].cos })

	engagedSet := make(map[string]struct{}, len(engaged))
	for _, id := range engaged {
		engagedSet[id] = struct{}{}
	}
	bestRank := 0
	for i, s := range scores {
		if _, ok := engagedSet[s.id]; ok {
			bestRank = i + 1 // 1-based
			break
		}
	}
	if bestRank == 0 {
		return rankMiss // no engaged thread is on the ranked live surface
	}

	// Borderline: the engaged thread sits adjacent to the k/(k+1) decision
	// boundary and the cosine gap across that boundary is a near-tie.
	if len(scores) > rankK && (bestRank == rankK || bestRank == rankK+1) {
		gap := scores[rankK-1].cos - scores[rankK].cos
		if gap < eps {
			pnlog.Info("tag-fidelity: borderline residue — best rank=%d k=%d boundary gap=%.4f < eps=%.4f",
				bestRank, rankK, gap, eps)
			return rankBorderline
		}
	}
	if bestRank <= rankK {
		return rankHit
	}
	return rankMiss
}

// anyEngagedOverlaps reports whether any engaged thread's symbol set has a
// non-empty normalized intersection with the prompted topic's symbol set.
func anyEngagedOverlaps(engaged []string, liveByID map[string]liveThread, topicSym []string) bool {
	if len(topicSym) == 0 {
		return false
	}
	want := make(map[string]struct{}, len(topicSym))
	for _, s := range topicSym {
		want[s] = struct{}{}
	}
	for _, id := range engaged {
		lt, ok := liveByID[id]
		if !ok {
			continue
		}
		for _, s := range lt.sym {
			if _, hit := want[s]; hit {
				return true
			}
		}
	}
	return false
}

// jaccard returns |a∩b| / |a∪b| over the two normalized string sets, or 0 for a
// pair whose union is empty.
func jaccard(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	as := make(map[string]struct{}, len(a))
	for _, x := range a {
		as[x] = struct{}{}
	}
	bs := make(map[string]struct{}, len(b))
	for _, x := range b {
		bs[x] = struct{}{}
	}
	inter := 0
	for x := range as {
		if _, ok := bs[x]; ok {
			inter++
		}
	}
	union := len(as) + len(bs) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// cosine is the measurement-side cosine similarity of two equal-length vectors.
// A zero-norm vector yields 0 (undefined direction).
func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// embedOne embeds a single text and returns its vector (nil on error/empty).
func embedOne(ctx context.Context, emb graderEmbedder, text string) ([]float64, error) {
	vs, err := emb.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, nil
	}
	return vs[0], nil
}

// embedLiveThreads batches every live thread's representative text through the
// embedder ONCE and returns id→vector. A batch failure yields an empty map (the
// residue then falls back to unadjudicated), never a guess.
func embedLiveThreads(ctx context.Context, emb graderEmbedder, live []liveThread) map[string][]float64 {
	if len(live) == 0 {
		return map[string][]float64{}
	}
	texts := make([]string, len(live))
	for i, lt := range live {
		texts[i] = lt.text
	}
	vs, err := emb.Embed(ctx, texts)
	if err != nil || len(vs) != len(live) {
		if err != nil {
			pnlog.Warn("tag-fidelity: live-thread embed batch failed (%v) — residue unadjudicated", err)
		}
		return map[string][]float64{}
	}
	out := make(map[string][]float64, len(live))
	for i, lt := range live {
		out[lt.id] = vs[i]
	}
	return out
}

// buildPlanTurns regenerates the canonical (zero-feedback) step stream for cfg
// and derives one planTurn per consult step, keyed by the step's simulated
// instant. The plan's intended tag is the step's scripted MockResponse (parsed
// with prompt.Parse): a tag naming any thr_<n> is re-engagement intent; a pure
// *new-topic* tag is new-topic intent. The prompted topic's symbol set is the
// scripted tag's anchors; the prompted content is the step's UserInput.
// SleepCycle steps carry no turn and are skipped.
func buildPlanTurns(cfg WorkloadConfig) []planTurn {
	sc := GenerateWorkload(cfg)
	src := sc.StepSource
	if src == nil {
		return nil
	}
	// Drain the canonical (zero-feedback, Index -1) stream directly — the same
	// pure workload drainSteps produces, inlined so this production file carries
	// no test-tier dependency.
	var out []planTurn
	for {
		s, ok := src.Next(scenarios.StepFeedback{Index: -1})
		if !ok {
			break
		}
		if s.SleepCycle {
			continue
		}
		pt := planTurn{instant: s.At.Format(time.RFC3339)}
		// A scripted step with no parseable plan tag still emits a turn (the
		// runtime executes it), so it must HOLD its ordinal slot in the
		// (instant, ordinal) join — gradeable=false, nothing graded for it.
		if pr, err := prompt.Parse(s.MockResponse.Content); err == nil {
			pt.gradeable = true
			pt.topicSym = append([]string(nil), pr.Tag.Anchors...)
			pt.content = s.UserInput
			for _, th := range pr.Tag.Threads {
				if th != prompt.NewTopicLiteral {
					pt.reengage = true
					break
				}
			}
		}
		out = append(out, pt)
	}
	return out
}

// turnBoundaryAction / turnBoundaryDetail identify the event log's per-turn
// boundary line — `context.modified source=user.prompt`, emitted exactly once
// per turn by the §3.0 chain (§2.8) — which buildObservedTurns segments on.
const (
	turnBoundaryAction = "context.modified"
	turnBoundaryDetail = "source=user.prompt"
)

// buildObservedTurns scrapes the event-log day files (in filename order —
// chronological, since the log is append-only and day-partitioned) and
// segments the model's tag markers PER TURN on the per-turn boundary line:
// each `context.modified source=user.prompt` opens a new observed turn, and
// subsequent marker lines fold into it until the next boundary. Turns are
// returned grouped by their simulated instant (the boundary line's leading
// RFC3339 field, equal to the turn's Step.At), in executed order within each
// instant — the (instant, ordinal) join key gradeTagFidelity consumes.
//
// Recognized markers: thread.engaged / thread.engaged-non-owner (→ engaged
// id), thread.created / thread.created-meta-only (→ *new-topic*),
// topic.tag-missing (→ no parseable tag), thread.tag-defaulted (→ the D6
// owner-default bound the turn; the engaged id is the runtime's recovery
// choice, not a model emission — the grader classifies these as omissions
// before it ever looks at engaged).
//
// FALLBACK: a log with no boundary lines at all (pre-boundary format) falls
// back to the old instant-MERGED grouping — one observedTurn per instant with
// every marker folded in — and returns degraded=true so the caller labels the
// series honestly (same-second turns smear in that mode).
func buildObservedTurns(logsDir string) (out map[string][]observedTurn, degraded bool) {
	out = map[string][]observedTurn{}
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return out, false
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var (
		cur   *observedTurn
		flush = func() {
			if cur != nil {
				out[cur.instant] = append(out[cur.instant], *cur)
				cur = nil
			}
		}
		sawBoundary bool
		merged      = map[string]*observedTurn{} // degraded-fallback accumulator
	)
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(logsDir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			instant, catAction := fields[0], fields[1]
			if catAction == turnBoundaryAction {
				if len(fields) >= 3 && fields[2] == turnBoundaryDetail {
					sawBoundary = true
					flush()
					cur = &observedTurn{instant: instant}
				}
				continue
			}

			// Marker target: the in-progress per-turn segment, or (fallback)
			// the instant-merged accumulator. Markers before the first
			// boundary in a boundary-bearing log (bootstrap noise) fold into
			// the merged map too, but a boundary-bearing log discards it below.
			ot := cur
			if ot == nil {
				if m, ok := merged[instant]; ok {
					ot = m
				} else {
					ot = &observedTurn{instant: instant}
					merged[instant] = ot
				}
			}
			switch catAction {
			case "thread.engaged", "thread.engaged-non-owner":
				if len(fields) >= 3 {
					ot.engaged = append(ot.engaged, fields[2])
				}
			case "thread.created", "thread.created-meta-only":
				ot.created = true
			case "topic.tag-missing":
				ot.tagMissing = true
			case "thread.tag-defaulted":
				ot.tagDefaulted = true
			}
		}
	}
	flush()

	if sawBoundary {
		return out, false
	}
	// Degraded fallback: no boundary line anywhere — old-format log. Emit the
	// instant-merged groups (one turn per instant), deterministically.
	for instant, ot := range merged {
		out[instant] = append(out[instant], *ot)
	}
	return out, true
}

// buildLiveThreads reads the post-run spine + frontmatter into the grader's
// live-thread set: each thread's symbol set (spine anchors ∪ history_symbols)
// and a representative text (anchors + summary + description) for embedding.
func buildLiveThreads(paths store.PersonantPaths) []liveThread {
	recs, err := store.ReadSpine(paths.Spine)
	if err != nil {
		return nil
	}
	fms, _ := store.LoadAllThreadFrontmatter(paths, nil)
	fmByID := make(map[string]memops.ThreadMeta, len(fms))
	for _, fm := range fms {
		fmByID[fm.ID] = fm
	}
	out := make([]liveThread, 0, len(recs))
	for _, r := range recs {
		symSet := map[string]struct{}{}
		for _, a := range r.Anchors {
			symSet[a] = struct{}{}
		}
		var textParts []string
		textParts = append(textParts, r.Anchors...)
		if fm, ok := fmByID[r.ID]; ok {
			for _, hs := range fm.HistorySymbols {
				symSet[hs.Normalized] = struct{}{}
				textParts = append(textParts, hs.Normalized)
			}
			if fm.Summary != "" {
				textParts = append(textParts, fm.Summary)
			}
			if fm.Description != "" {
				textParts = append(textParts, fm.Description)
			}
		}
		if r.Summary != "" {
			textParts = append(textParts, r.Summary)
		}
		sym := make([]string, 0, len(symSet))
		for s := range symSet {
			sym = append(sym, s)
		}
		out = append(out, liveThread{
			id:   r.ID,
			sym:  sym,
			text: strings.Join(textParts, " "),
		})
	}
	return out
}

// anchorEmissionOverlap computes the run-level anchor-emission overlap: the mean
// per-thread Jaccard between the model-emitted anchors (history_symbols with
// source=model) and the deterministic extraction pass's symbols (source=
// deterministic), over threads carrying ≥1 model anchor. It reads the
// authoritative post-run frontmatter (each history symbol carries its Source), so
// no runtime hook is needed. Returns (meanJaccard, observedThreadCount,
// detSymbolTotal); (0, 0, 0) on a load failure.
//
// VACUITY (burndown 2026-07b item 5): detSymbolTotal is the run-total count of
// persisted source=deterministic history symbols across ALL threads. When it is
// 0 the mean Jaccard is 0-against-empty-set BY CONSTRUCTION — the metric is
// UNMEASURED, not 0.000, and the caller must report it so (the Jul-16 live run's
// "0.000 over 151 threads" false alarm). The §3.3 deterministic pass extracts
// only identifier-class symbols (URLs / file paths / hex IDs), so a workload
// whose conversational content carries none yields detSymbolTotal == 0 honestly.
//
// NOTE: this is a RUN-LEVEL realization of the "per-turn" intent — the
// authoritative per-turn source split (model anchors vs the §3.3 deterministic
// pass, per turn) is not in the event log; the frontmatter carries the same two
// sets accumulated per thread. A true per-turn series would need a one-line
// runtime log hook (see the A2 report).
func anchorEmissionOverlap(paths store.PersonantPaths) (float64, int, int) {
	fms, err := store.LoadAllThreadFrontmatter(paths, nil)
	if err != nil {
		return 0, 0, 0
	}
	return anchorOverlapFromMeta(fms)
}

// anchorOverlapFromMeta is the pure half of anchorEmissionOverlap (unit-testable
// without a substrate): mean model-vs-deterministic Jaccard over threads with
// ≥1 model anchor, plus the observation count and the run-total deterministic
// symbol count (the measurability signal).
func anchorOverlapFromMeta(fms []memops.ThreadMeta) (float64, int, int) {
	var sum float64
	obs, detTotal := 0, 0
	for _, fm := range fms {
		var modelSyms, detSyms []string
		for _, hs := range fm.HistorySymbols {
			switch hs.Source {
			case memops.SourceModel:
				modelSyms = append(modelSyms, hs.Normalized)
			case memops.SourceDeterministic:
				detSyms = append(detSyms, hs.Normalized)
			}
		}
		detTotal += len(detSyms)
		if len(modelSyms) == 0 {
			continue
		}
		obs++
		sum += jaccard(modelSyms, detSyms)
	}
	if obs == 0 {
		return 0, 0, detTotal
	}
	return sum / float64(obs), obs, detTotal
}
