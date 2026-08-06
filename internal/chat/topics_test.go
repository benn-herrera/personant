package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/model"
)

// rosterNow is the roster tests' clock. Every stamp below is expressed as an
// offset from it, so the rendered ages are exact rather than approximate.
var rosterNow = time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

// ago builds an RFC3339 stamp d before rosterNow.
func ago(d time.Duration) string {
	return rosterNow.Add(-d).Format(time.RFC3339)
}

// topicRec is a terse spine-record builder for the roster tests. Engaged
// membership is NOT a record field (it is Layer B), so it is passed to
// renderTopics separately.
func topicRec(id, desc, summary string, state memops.ThreadState, engagedAgo, changedAgo time.Duration) memops.SpineRecord {
	return memops.SpineRecord{
		ID:           id,
		Description:  desc,
		Summary:      summary,
		State:        state,
		LastEngaged:  ago(engagedAgo),
		StateChanged: ago(changedAgo),
	}
}

const day = 24 * time.Hour

// TestRenderTopicsGrouping: the four groups, in order, each holding the
// records its classification rule claims — including the state-first rule
// that a closed topic still listed in Layer B renders as closed.
func TestRenderTopicsGrouping(t *testing.T) {
	recs := []memops.SpineRecord{
		topicRec("thr_1", "emulsion separating", "thickener pinned at 0.4", memops.ThreadActive, time.Hour, time.Hour),
		topicRec("thr_2", "supplier shortlist", "", memops.ThreadPaused, 3*day, 2*day),
		topicRec("thr_3", "old rheology notes", "", memops.ThreadActive, 9*day, 9*day),
		topicRec("thr_4", "shear model choice", "picked Carreau", memops.ThreadResolved, 5*day, 4*day),
		topicRec("thr_5", "ancient history", "dropped", memops.ThreadAbandoned, 200*day, 100*day),
		topicRec("thr_6", "stale membership", "closed but still in layer B", memops.ThreadDecided, 2*day, 2*day),
	}
	// thr_1 is genuinely engaged; thr_6 is the stale working-set entry.
	got := strings.Join(renderTopics(recs, []string{"thr_1", "thr_6"}, rosterNow, false), "\n")

	wantOrder := []string{
		"ENGAGED", "thr_1",
		"PAUSED", "thr_2",
		"DORMANT", "thr_3",
		"RECENTLY CLOSED", "thr_6", "thr_4",
	}
	assertOrdered(t, got, wantOrder)
	if strings.Contains(got, "thr_5") {
		t.Errorf("a 100-day-old closure is in the default roster:\n%s", got)
	}
	if !strings.Contains(got, topicsBackToHint) {
		t.Errorf("closed topics shown without the /back-to hint:\n%s", got)
	}
}

// TestRenderTopicsRecencyOrder: newest first within a group, on the stamp
// that group's recency MEANS — last-engaged for open topics, closed-at for
// closed ones.
func TestRenderTopicsRecencyOrder(t *testing.T) {
	recs := []memops.SpineRecord{
		topicRec("thr_1", "oldest open", "", memops.ThreadActive, 8*day, 8*day),
		topicRec("thr_2", "newest open", "", memops.ThreadActive, time.Hour, time.Hour),
		topicRec("thr_3", "middle open", "", memops.ThreadActive, 2*day, 2*day),
		// Engaged long ago, closed yesterday: it must sort by the closure.
		topicRec("thr_4", "closed yesterday", "done", memops.ThreadResolved, 30*day, day),
		topicRec("thr_5", "closed last week", "done", memops.ThreadResolved, 8*day, 7*day),
	}
	got := strings.Join(renderTopics(recs, nil, rosterNow, false), "\n")
	assertOrdered(t, got, []string{"thr_2", "thr_3", "thr_1", "thr_4", "thr_5"})
}

// TestTopicGistFallbackChain walks Summary → Description → anchors → the
// honest placeholder, plus the rule that a gist equal to the display name
// is no gist at all (/topic sets both fields to the same string).
func TestTopicGistFallbackChain(t *testing.T) {
	tests := []struct {
		name string
		rec  memops.SpineRecord
		want string
	}{
		{
			name: "summary wins",
			rec:  memops.SpineRecord{ID: "thr_1", Description: "why is it separating", Summary: "thickener pinned"},
			want: "thickener pinned",
		},
		{
			name: "description is the display, never the gist",
			rec:  memops.SpineRecord{ID: "thr_1", Summary: "", Description: "why is it separating"},
			want: "no summary yet",
		},
		{
			name: "summary is the display when there is no description",
			rec:  memops.SpineRecord{ID: "thr_1", Summary: "thickener pinned", Anchors: []string{"emulsion", "viscosity"}},
			want: "emulsion, viscosity",
		},
		{
			name: "anchors when both text fields are the same string",
			rec: memops.SpineRecord{
				ID: "thr_1", Description: "knot theory", Summary: "knot theory",
				Anchors: []string{"trefoil", "unknot", "genus", "braid", "seifert"},
			},
			want: "trefoil, unknot, genus, braid",
		},
		{
			name: "nothing at all",
			rec:  memops.SpineRecord{ID: "thr_1"},
			want: "no summary yet",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := topicGist(tt.rec); got != tt.want {
				t.Errorf("topicGist = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRenderTopicsBound: the default roster stops at topicsDefaultMax TOPIC
// lines and says how many it withheld; /topics all withholds nothing.
func TestRenderTopicsBound(t *testing.T) {
	const n = topicsDefaultMax + 7
	recs := make([]memops.SpineRecord, 0, n)
	for i := range n {
		id := fmt.Sprintf("thr_%d", i+1)
		// Descending ages, so the id order and the recency order agree.
		recs = append(recs, topicRec(id, "topic "+id, "", memops.ThreadActive,
			time.Duration(i+1)*time.Hour, time.Duration(i+1)*time.Hour))
	}

	bounded := renderTopics(recs, nil, rosterNow, false)
	if got := countTopicLines(bounded); got != topicsDefaultMax {
		t.Errorf("default roster listed %d topics, want %d", got, topicsDefaultMax)
	}
	wantMore := fmt.Sprintf("…and %d more — /topics all", n-topicsDefaultMax)
	if last := bounded[len(bounded)-1]; last != wantMore {
		t.Errorf("bound notice = %q, want %q", last, wantMore)
	}

	all := renderTopics(recs, nil, rosterNow, true)
	if got := countTopicLines(all); got != n {
		t.Errorf("/topics all listed %d topics, want %d", got, n)
	}
	if strings.Contains(strings.Join(all, "\n"), "more — /topics all") {
		t.Error("/topics all still printed the bound notice")
	}
}

// TestRenderTopicsClosedWindow: the 14-day boundary. Inside → default
// roster; outside → /topics all only.
func TestRenderTopicsClosedWindow(t *testing.T) {
	recs := []memops.SpineRecord{
		topicRec("thr_1", "just inside", "done", memops.ThreadResolved, 20*day, recentlyClosedWindow-time.Hour),
		topicRec("thr_2", "just outside", "done", memops.ThreadResolved, 20*day, recentlyClosedWindow+time.Hour),
		// An unparseable stamp must not fall INSIDE the window by accident.
		{ID: "thr_3", Description: "no stamp", Summary: "done", State: memops.ThreadResolved},
	}
	def := strings.Join(renderTopics(recs, nil, rosterNow, false), "\n")
	if !strings.Contains(def, "thr_1") {
		t.Errorf("a closure inside the window is missing:\n%s", def)
	}
	for _, id := range []string{"thr_2", "thr_3"} {
		if strings.Contains(def, id) {
			t.Errorf("%s is outside the window but in the default roster:\n%s", id, def)
		}
	}
	all := strings.Join(renderTopics(recs, nil, rosterNow, true), "\n")
	for _, id := range []string{"thr_1", "thr_2", "thr_3"} {
		if !strings.Contains(all, id) {
			t.Errorf("/topics all is missing %s:\n%s", id, all)
		}
	}
}

// TestRelativeAge pins the rendering against a fixed clock, including the
// two edges that are not arithmetic: an unparseable stamp and a stamp in
// the future (clock skew must not render a negative age).
func TestRelativeAge(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{2 * day, "2d ago"},
		{20 * day, "2w ago"},
		{400 * day, "1y ago"},
		{-time.Hour, "just now"},
	}
	for _, tt := range tests {
		t.Run(tt.want+"/"+tt.d.String(), func(t *testing.T) {
			if got := relativeAge(rosterNow, ago(tt.d)); got != tt.want {
				t.Errorf("relativeAge(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
	if got := relativeAge(rosterNow, "not-a-stamp"); got != "age unknown" {
		t.Errorf("unparseable stamp = %q, want %q", got, "age unknown")
	}
}

// TestRenderTopicsEmpty: an empty project gets one friendly line and no
// group scaffolding at all.
func TestRenderTopicsEmpty(t *testing.T) {
	got := renderTopics(nil, nil, rosterNow, false)
	if len(got) != 1 || got[0] != topicsEmpty {
		t.Errorf("empty roster = %#v, want the single line %q", got, topicsEmpty)
	}
}

// TestSlashTopicsSession wires the command end to end: a session that
// starts a topic and then lists it, plus the empty-project reply and the
// argument guard.
func TestSlashTopicsSession(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	out, errb := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
		"prj_1", "/topics\n/topic knot theory\n/topics\n/topics sideways\n/quit\n")

	if !strings.Contains(out, topicsEmpty) {
		t.Errorf("/topics on an empty project did not say so:\n%s", out)
	}
	if !strings.Contains(out, "ENGAGED") || !strings.Contains(out, "knot theory (thr_1)") {
		t.Errorf("/topics did not list the new topic as engaged:\n%s", out)
	}
	if !strings.Contains(errb, "usage: /topics [all]") {
		t.Errorf("a bad argument did not produce the usage line:\n%s", errb)
	}
}

// countTopicLines counts roster entries — the indented `name (thr_N) — …`
// lines — ignoring group headers and trailing hints.
func countTopicLines(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "  ") {
			n++
		}
	}
	return n
}

// assertOrdered checks that each want appears in got, in the given order.
func assertOrdered(t *testing.T, got string, want []string) {
	t.Helper()
	at := 0
	for _, w := range want {
		i := strings.Index(got[at:], w)
		if i < 0 {
			t.Fatalf("missing %q after offset %d:\n%s", w, at, got)
		}
		at += i + len(w)
	}
}
