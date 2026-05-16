package scenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"personant/internal/store"
)

// madlibsQueriesPath is the derived query artifact produced by
// testdata/recall_madlibs/generate.py. It is .gitignore'd; `make
// recall-madlibs` (a dependency of `make test`) regenerates it.
var madlibsQueriesPath = filepath.Join("testdata", "recall_madlibs", "queries.json")

type madlibsDoc struct {
	Seed      int `json:"seed"`
	Templates []struct {
		Name    string   `json:"name"`
		Anchors []string `json:"anchors"`
	} `json:"templates"`
	Queries []struct {
		ID        string   `json:"id"`
		Template  string   `json:"template"`
		Tags      []string `json:"tags"`
		UserInput string   `json:"user_input"`
	} `json:"queries"`
}

// loadMadlibsQueries reads the generated query set. When the artifact
// is absent the calling test is skipped with a regeneration hint —
// `go test ./...` run without `make` degrades gracefully rather than
// hard-failing.
func loadMadlibsQueries(t *testing.T) madlibsDoc {
	t.Helper()
	body, err := os.ReadFile(madlibsQueriesPath)
	if os.IsNotExist(err) {
		t.Skipf("recall-madlibs artifact %s absent — run `make recall-madlibs`", madlibsQueriesPath)
	}
	if err != nil {
		t.Fatalf("read %s: %v", madlibsQueriesPath, err)
	}
	var doc madlibsDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse %s: %v", madlibsQueriesPath, err)
	}
	if len(doc.Templates) == 0 || len(doc.Queries) == 0 {
		t.Fatalf("%s: empty templates or queries", madlibsQueriesPath)
	}
	return doc
}

// TestScenario_RecallMadlibs drives the Phase C.2 mad-libs query set
// through the harness end-to-end. Each query becomes its own isolated
// scenario: a fresh home seeded with one thread per topic template,
// then a single turn carrying the mad-libs query.
//
// Per-query isolation is deliberate. A multi-step scenario would have
// each query's *new-topic* throwaway thread linger as a confounding
// recall target for later queries that sample overlapping cells; a
// fresh home per query keeps the match surface to exactly the three
// pristine seeded topics.
//
// C.2 templates are authored so every column cell is an anchor of its
// source topic — each query carries 4 distinct anchors of one topic,
// yielding Jaccard 4/(4+8-4) = 0.5 against that topic's 8-anchor set
// and 0 against the other two. ExpectedRecallMatches is therefore the
// single source thread; the harness records precision/recall/F1 and
// fails on any drift. Genuine vocabulary drift (sub-threshold scores)
// is a C.3 concern.
func TestScenario_RecallMadlibs(t *testing.T) {
	doc := loadMadlibsQueries(t)

	// Threads are seeded in template-array order → thr_1, thr_2, ...
	threadID := map[string]string{}
	for i, tpl := range doc.Templates {
		threadID[tpl.Name] = fmt.Sprintf("thr_%d", i+1)
	}

	for _, q := range doc.Queries {
		target, ok := threadID[q.Template]
		if !ok {
			t.Errorf("query %s references unknown template %q", q.ID, q.Template)
			continue
		}
		t.Run(q.ID, func(t *testing.T) {
			sc := Scenario{
				Name:  "recall-madlibs-" + q.ID,
				Setup: seedMadlibsThreads(t, doc),
				Steps: []Step{
					{
						UserInput: q.UserInput,
						// The throwaway *new-topic* thread is engaged this
						// turn → excluded from recall. Its anchors are the
						// query's own cells, so the turn's coalesced symbol
						// set is exactly those cells.
						MockResponse: NewMockResponseWithTag(
							[]string{"*new-topic*"}, q.Tags,
							"Working from the query terms."),
						Annotation:            q.ID,
						ExpectedRecallMatches: []string{target},
					},
				},
			}
			RunScenario(t, sc)
		})
	}
}

// seedMadlibsThreads returns a Scenario.Setup that writes one seeded
// thread per topic template (spine record + thread file), anchor sets
// taken verbatim from the template. IDs are assigned thr_1, thr_2, …
// in template-array order so the caller can resolve a template name to
// its thread ID positionally.
func seedMadlibsThreads(t *testing.T, doc madlibsDoc) func(*Harness) error {
	return func(h *Harness) error {
		ts := "2026-05-01T12:00:00Z"
		for i, tpl := range doc.Templates {
			rec := store.SpineRecord{
				ID:           fmt.Sprintf("thr_%d", i+1),
				Project:      h.Project.ID,
				Anchors:      append([]string(nil), tpl.Anchors...),
				Summary:      tpl.Name + " seed",
				State:        store.ThreadActive,
				Created:      ts,
				LastEngaged:  ts,
				StateChanged: ts,
				TurnCount:    1,
			}
			seedThread(t, h, rec)
		}
		return nil
	}
}
