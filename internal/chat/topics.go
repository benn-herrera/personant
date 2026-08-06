package chat

// topics.go implements /topics — the roster of the active project's topics
// (§4.2). It is the companion `/back-to` was missing: a reference to
// re-engage by is useless without a way to see what exists.
//
// VOCABULARY (user ruling 2026-08-05): every string in this file says
// "topic". The storage-layer term is "thread" and the id scheme stays
// `thr_N`; the id is rendered because it is what the user types back, not
// because the vocabulary leaks.

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/turn"
)

const (
	// recentlyClosedWindow bounds the RECENTLY CLOSED group. Closed topics
	// are in the default roster deliberately: an auto-accepted closure
	// (§3.5) is the runtime's judgment, and /back-to is the user's revision
	// path for it — a revision path they cannot see is not one they have.
	// Past the window a closure has stopped being a recent decision, so it
	// stays behind /topics all.
	recentlyClosedWindow = 14 * 24 * time.Hour

	// topicsDefaultMax bounds the default roster in LINES OF TOPIC, not
	// lines of output: group headers and the trailing hints are the frame,
	// not the list. A long-lived project's roster must not scroll the
	// session away on a glance command.
	topicsDefaultMax = 20

	// topicsGistAnchors bounds the anchor fallback in a topic's gist —
	// enough to identify it, short enough to stay on one line. Mirrors
	// internal/turn's gistAnchors, which bounds the same fallback in the
	// §3.4 recall offer.
	topicsGistAnchors = 4

	// topicsAllArg is the one argument /topics accepts.
	topicsAllArg = "all"

	// topicsBackToHint trails a roster that showed closed topics: it names
	// the revision path for a closure the user did not ack.
	topicsBackToHint = "/back-to <id|name> re-engages (reopens a closed topic)"

	// topicsEmpty is the whole reply for a project with no topics.
	topicsEmpty = "no topics in this project yet — /topic <name> starts one"
)

// cmdTopics implements /topics [all]. One ListThreads call for the whole
// roster: the groups are a partition of the same record set, and reading
// the spine four times to render one screen would be four times the I/O
// for the same bytes.
func cmdTopics(ctx context.Context, out io.Writer, ops memops.MemoryOps, state *turn.State, rest string) error {
	rest = strings.ToLower(strings.TrimSpace(rest))
	if rest != "" && rest != topicsAllArg {
		return fmt.Errorf("usage: /topics [%s]", topicsAllArg)
	}
	recs, err := ops.ListThreads(ctx, memops.ThreadFilter{Project: state.ActiveProject.ID})
	if err != nil {
		return fmt.Errorf("list topics: %w", err)
	}
	for _, line := range renderTopics(recs, state.ActiveThreads, clock.Timeline(), rest == topicsAllArg) {
		fmt.Fprintln(out, line)
	}
	return nil
}

// topicGroup is one rendered section of the roster.
type topicGroup struct {
	label  string
	closed bool // the RECENTLY CLOSED group — the one the /back-to hint is about
	recs   []memops.SpineRecord
}

// renderTopics is the whole of /topics' presentation logic, kept pure so
// the grouping, ordering, bound and age rendering are testable against a
// fixed clock without a session around them.
//
// engaged is the Layer B membership list (§3.1) — engagement is working-set
// membership, not a stored state, so it cannot be read off the record.
func renderTopics(recs []memops.SpineRecord, engaged []string, now time.Time, all bool) []string {
	groups := groupTopics(recs, engaged, now, all)

	total := 0
	for _, g := range groups {
		total += len(g.recs)
	}
	if total == 0 {
		return []string{topicsEmpty}
	}
	limit := total
	if !all {
		limit = min(total, topicsDefaultMax)
	}

	lines := make([]string, 0, total+len(groups)+2)
	shown, showedClosed := 0, false
	for _, g := range groups {
		if len(g.recs) == 0 || shown >= limit {
			continue
		}
		lines = append(lines, g.label)
		for _, rec := range g.recs {
			if shown >= limit {
				break
			}
			lines = append(lines, topicLine(rec, now, g.closed))
			shown++
			showedClosed = showedClosed || g.closed
		}
	}
	if shown < total {
		lines = append(lines, fmt.Sprintf("…and %d more — /topics %s", total-shown, topicsAllArg))
	}
	if showedClosed {
		lines = append(lines, topicsBackToHint)
	}
	return lines
}

// groupTopics partitions the project's records into the four display
// groups, each recency-sorted, newest first.
//
// Classification is STATE-first and membership-last: a stale working-set
// entry pointing at a retired topic must render as closed (what the record
// says) rather than as engaged (what the session believes).
func groupTopics(recs []memops.SpineRecord, engaged []string, now time.Time, all bool) []topicGroup {
	engagedSet := make(map[string]struct{}, len(engaged))
	for _, id := range engaged {
		engagedSet[id] = struct{}{}
	}

	var eng, paused, dormant, closed []memops.SpineRecord
	for _, rec := range recs {
		switch {
		case closedState(rec.State):
			// Out-of-window closures are the bulk of an old project's spine;
			// they are the thing /topics all is for.
			if all || now.Sub(parseStamp(rec.StateChanged)) <= recentlyClosedWindow {
				closed = append(closed, rec)
			}
		case rec.State == memops.ThreadPaused:
			paused = append(paused, rec)
		default:
			if _, ok := engagedSet[rec.ID]; ok {
				eng = append(eng, rec)
			} else {
				// Everything still open but not in play: Layer C plus the
				// threads that have aged out of the working set entirely.
				dormant = append(dormant, rec)
			}
		}
	}

	// A closed topic's recency is WHEN IT CLOSED; an open one's is when it
	// was last worked on.
	sortByStamp(eng, func(r memops.SpineRecord) string { return r.LastEngaged })
	sortByStamp(paused, func(r memops.SpineRecord) string { return r.StateChanged })
	sortByStamp(dormant, func(r memops.SpineRecord) string { return r.LastEngaged })
	sortByStamp(closed, func(r memops.SpineRecord) string { return r.StateChanged })

	return []topicGroup{
		{label: "ENGAGED", recs: eng},
		{label: "PAUSED", recs: paused},
		{label: "DORMANT", recs: dormant},
		{label: "RECENTLY CLOSED", closed: true, recs: closed},
	}
}

// closedState reports whether a §2.2.1 state is a closure outcome (§3.5).
// `blocked` is deliberately NOT one — it is gated, not finished.
func closedState(s memops.ThreadState) bool {
	switch s {
	case memops.ThreadWIP, memops.ThreadResolved, memops.ThreadDecided, memops.ThreadAbandoned:
		return true
	}
	return false
}

// sortByStamp orders newest-first. Stable, so equal (or equally
// unparseable) stamps keep ListThreads' deterministic by-id order.
func sortByStamp(recs []memops.SpineRecord, key func(memops.SpineRecord) string) {
	slices.SortStableFunc(recs, func(a, b memops.SpineRecord) int {
		return strings.Compare(key(b), key(a))
	})
}

// topicLine renders one roster entry:
//
//	<display name> (thr_N) — <gist> · <relative age>
func topicLine(rec memops.SpineRecord, now time.Time, isClosed bool) string {
	stamp := rec.LastEngaged
	if isClosed {
		stamp = rec.StateChanged
	}
	return fmt.Sprintf("  %s · %s",
		topicLabel(topicDisplay(rec), rec.ID, topicGist(rec)), relativeAge(now, stamp))
}

// topicLabel renders the ONE shape the front end names a topic in:
//
//	<display name> (thr_N) — <tail>
//
// The roster, the §3.5 closure offer and the auto-closure one-liner all
// print it, so a user reads the same three parts in the same order
// wherever a topic is named — and the id, which /back-to and /done take as
// an argument, is always on the line.
//
// Both trailing parts degrade rather than render an empty fragment: a
// display name that is only the id collapses to the id (never
// "thr_3 (thr_3)"), and an empty tail drops the em-dash with it.
func topicLabel(display, id, tail string) string {
	label := id
	if display != "" && display != id {
		label = display + " (" + id + ")"
	}
	if tail != "" {
		label += " — " + tail
	}
	return label
}

// topicDisplay is the topic's working name — the §2.2.2 display form's
// human half. The Description (the utterance that spawned it, set once at
// creation) is the name the user recognizes; the id is the last resort.
func topicDisplay(rec memops.SpineRecord) string {
	switch {
	case rec.Description != "":
		return rec.Description
	case rec.Summary != "":
		return rec.Summary
	default:
		return rec.ID
	}
}

// topicGist is what the topic is about, in one line: the curator's closure
// Summary, else the projected anchors — the §3.4 fallback chain
// internal/turn's describeCandidates walks for the recall offer. It is
// duplicated rather than shared because that helper is unexported in
// internal/turn, keyed on a measure.Result, and reachable only by widening
// a package outside this change's scope.
//
// Description is deliberately NOT a step in this chain even though it is
// one in the recall offer's: topicDisplay already prefers it, so a
// Description that exists is on the line as the NAME, and repeating it as
// the gist would say one thing twice. The same reasoning drops a Summary
// that is serving as the display name — /topic sets both fields to the name
// it was given, and "knot theory — knot theory" is not a description of
// anything.
func topicGist(rec memops.SpineRecord) string {
	if rec.Summary != "" && rec.Summary != topicDisplay(rec) {
		return rec.Summary
	}
	if len(rec.Anchors) > 0 {
		return strings.Join(rec.Anchors[:min(len(rec.Anchors), topicsGistAnchors)], ", ")
	}
	return "no summary yet"
}

// relativeAge renders an RFC3339 stamp as an age against now — coarse on
// purpose, since the roster answers "how long ago" and never "when".
// An unparseable or absent stamp says so rather than rendering an age
// against the zero time.
func relativeAge(now time.Time, stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return "age unknown"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
	}
}

// parseStamp resolves an RFC3339 stamp, yielding the zero time on failure
// — which puts an unparseable record outside the recently-closed window
// rather than inside it.
func parseStamp(stamp string) time.Time {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return time.Time{}
	}
	return t
}
