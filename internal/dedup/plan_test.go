package dedup

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

var planBase = time.Date(2026, 5, 8, 3, 0, 0, 0, time.UTC)

func at(minute int) time.Time { return planBase.Add(time.Duration(minute) * time.Minute) }

// describePlan flattens a plan into sorted one-line records so cases can
// state the whole expected plan.
func describePlan(plan map[ExcerptPosition][]Expansion) []string {
	var out []string
	for pos, exps := range plan {
		for _, e := range exps {
			if e.ThreadID != pos.ThreadID || e.Turn != pos.Turn {
				out = append(out, fmt.Sprintf("misfiled %+v under %+v", e, pos))
			}
			switch e.Kind {
			case ExpansionLiteral:
				out = append(out, fmt.Sprintf("%s:%d %s/%s literal %s size=%d cur=%d",
					e.ThreadID, e.Turn, e.Project, e.Path, e.ID, e.Size, e.PathSize))
			case ExpansionDiff:
				out = append(out, fmt.Sprintf("%s:%d %s/%s diff %s>%s size=%d cur=%d",
					e.ThreadID, e.Turn, e.Project, e.Path, e.From, e.To, e.Size, e.PathSize))
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestPlan(t *testing.T) {
	const literalMax = 1000
	tests := []struct {
		name    string
		threads []PlanThread
		want    []string
	}{
		{
			name: "latest position wins across threads",
			threads: []PlanThread{
				{ID: "A", Project: "p", Entries: []FileEntry{{Path: "f", ID: "old", Size: 10, Turn: 5, At: at(1)}}},
				{ID: "B", Project: "p", Entries: []FileEntry{{Path: "f", ID: "new", Size: 12, Turn: 7, At: at(2)}}},
			},
			want: []string{"B:7 p/f literal new size=12 cur=12"},
		},
		{
			name: "equal at: earlier thread holds the latest position",
			threads: []PlanThread{
				{ID: "A", Project: "p", Entries: []FileEntry{{Path: "f", ID: "x", Size: 10, Turn: 5, At: at(1)}}},
				{ID: "B", Project: "p", Entries: []FileEntry{{Path: "f", ID: "y", Size: 10, Turn: 7, At: at(1)}}},
			},
			want: []string{"A:5 p/f literal x size=10 cur=10"},
		},
		{
			name: "same path in another project is another file",
			threads: []PlanThread{
				{ID: "A", Project: "p", Entries: []FileEntry{{Path: "f", ID: "x", Size: 10, Turn: 5, At: at(1)}}},
				{ID: "B", Project: "q", Entries: []FileEntry{{Path: "f", ID: "y", Size: 10, Turn: 7, At: at(2)}}},
			},
			want: []string{"A:5 p/f literal x size=10 cur=10", "B:7 q/f literal y size=10 cur=10"},
		},
		{
			name: "shared id: every path gets a literal candidate and keeps its diffs",
			threads: []PlanThread{
				{ID: "A", Project: "p", Entries: []FileEntry{
					{Path: "x", ID: "s", Size: 100, Turn: 9, At: at(5)},
					{Path: "y", ID: "s", Size: 100, Turn: 4, At: at(2), Changes: []FileChange{
						{Turn: 4, At: at(2), From: "y0", To: "s", Diff: true, Size: 10},
					}},
				}},
				{ID: "B", Project: "p", Entries: []FileEntry{{Path: "z", ID: "s", Size: 100, Turn: 8, At: at(5)}}},
			},
			want: []string{
				"A:4 p/y diff y0>s size=10 cur=100",
				"A:4 p/y literal s size=100 cur=100",
				"A:9 p/x literal s size=100 cur=100",
				"B:8 p/z literal s size=100 cur=100",
			},
		},
		{
			name: "size at literalMax gets nothing, literalMax-1 gets the literal",
			threads: []PlanThread{
				{ID: "A", Project: "p", Entries: []FileEntry{
					{Path: "big", ID: "b", Size: literalMax, Turn: 3, At: at(3), Changes: []FileChange{
						{Turn: 3, At: at(3), From: "b0", To: "b", Diff: true, Size: 1},
					}},
					{Path: "fits", ID: "f", Size: literalMax - 1, Turn: 3, At: at(3)},
				}},
			},
			want: []string{"A:3 p/fits literal f size=999 cur=999"},
		},
		{
			name: "N most recent changes by at, from two threads, the unkept one dropped and not replaced",
			threads: []PlanThread{
				{ID: "A", Project: "p", Entries: []FileEntry{{Path: "f", ID: "c6", Size: 100, Turn: 6, At: at(6), Changes: []FileChange{
					{Turn: 1, At: at(1), From: "c0", To: "c1", Diff: true, Size: 5},
					{Turn: 3, At: at(3), From: "c2", To: "c3", Diff: true, Size: 5},
					{Turn: 6, At: at(6), From: "c5", To: "c6", Diff: false},
				}}}},
				{ID: "B", Project: "p", Entries: []FileEntry{{Path: "f", ID: "c5", Size: 100, Turn: 5, At: at(5), Changes: []FileChange{
					{Turn: 2, At: at(2), From: "c1", To: "c2", Diff: true, Size: 5},
					{Turn: 4, At: at(4), From: "c3", To: "c4", Diff: true, Size: 5},
					{Turn: 5, At: at(5), From: "c4", To: "c5", Diff: true, Size: 5},
				}}}},
			},
			want: []string{
				"A:6 p/f literal c6 size=100 cur=100",
				"B:4 p/f diff c3>c4 size=5 cur=100",
				"B:5 p/f diff c4>c5 size=5 cur=100",
			},
		},
		{
			name: "after the file shrank, diffs at or above 0.7 of current size are dropped, not replaced by older ones",
			threads: []PlanThread{
				{ID: "A", Project: "p", Entries: []FileEntry{{Path: "f", ID: "c3", Size: 100, Turn: 3, At: at(3), Changes: []FileChange{
					{Turn: 0, At: at(0), From: "cx", To: "c0", Diff: true, Size: 5},
					{Turn: 1, At: at(1), From: "c0", To: "c1", Diff: true, Size: 70},
					{Turn: 2, At: at(2), From: "c1", To: "c2", Diff: true, Size: 69},
					{Turn: 3, At: at(3), From: "c2", To: "c3", Diff: true, Size: 300},
				}}}},
			},
			want: []string{
				"A:2 p/f diff c1>c2 size=69 cur=100",
				"A:3 p/f literal c3 size=100 cur=100",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describePlan(Plan(tt.threads, literalMax))
			if !slices.Equal(got, tt.want) {
				t.Errorf("plan:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestPlanPositionOrder(t *testing.T) {
	threads := []PlanThread{
		{ID: "A", Project: "p", Entries: []FileEntry{
			{Path: "b", ID: "b1", Size: 100, Turn: 2, At: at(2), Changes: []FileChange{
				{Turn: 2, At: at(2), From: "b0", To: "b1", Diff: true, Size: 5},
			}},
			{Path: "a", ID: "a2", Size: 100, Turn: 2, At: at(2), Changes: []FileChange{
				{Turn: 2, At: at(2), From: "a0", To: "a1", Diff: true, Size: 5},
				{Turn: 2, At: at(2), From: "a1", To: "a2", Diff: true, Size: 5},
			}},
		}},
	}
	var got []string
	for _, e := range Plan(threads, 1000)[ExcerptPosition{"A", 2}] {
		got = append(got, fmt.Sprintf("%s %s %s%s>%s", e.Path, e.Kind, e.ID, e.From, e.To))
	}
	want := []string{"a diff a0>a1", "a diff a1>a2", "a literal a2>", "b diff b0>b1", "b literal b1>"}
	if !slices.Equal(got, want) {
		t.Errorf("order:\n got %q\nwant %q", got, want)
	}
}
