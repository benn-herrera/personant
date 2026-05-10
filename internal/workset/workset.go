// Package workset assembles the working-set inputs (spec §3.1) that
// feed prompt.BuildSystemPrompt. v0.1 MVP populates only Layer A1 (the
// active project's spine entries in §2.2.2 display form); the other
// layers (E directives, A2 cross-project digests, B engaged threads, C
// dormant summaries) land in later phases and currently render as
// empty strings.
package workset

import (
	"fmt"
	"strings"

	"personant/internal/prompt"
	"personant/internal/store"
)

// State carries the inputs Compose needs to render layer content.
type State struct {
	Paths         store.PersonantPaths
	ActiveProject store.ProjectMeta
}

// Compose builds the SystemPromptParams for one turn.
//
// v0.1 MVP behavior:
//   - LayerA1: "\n"-joined display lines for every spine record whose
//     Project field equals state.ActiveProject.ID. Empty when the
//     project has no spine records yet — prompt.BuildSystemPrompt
//     omits the section header in that case.
//   - LayerE / LayerA2 / LayerB / LayerC: empty strings. Future
//     phases populate these.
func Compose(state State) (prompt.SystemPromptParams, error) {
	if state.ActiveProject.ID == "" {
		return prompt.SystemPromptParams{}, fmt.Errorf("workset: ActiveProject.ID is empty")
	}
	records, err := store.SpineRecordsByProject(state.Paths, state.ActiveProject.ID)
	if err != nil {
		return prompt.SystemPromptParams{}, fmt.Errorf("workset: load spine: %w", err)
	}
	lines := make([]string, 0, len(records))
	for _, rec := range records {
		lines = append(lines, RenderSpineDisplay(rec))
	}
	return prompt.SystemPromptParams{
		LayerA1: strings.Join(lines, "\n"),
	}, nil
}

// RenderSpineDisplay turns one SpineRecord into its spec §2.2.2
// display form:
//
//	thr_<id> [<anchors joined with ", ">] — <summary> [<state>]
//
// State markers are uppercased (WIP, RESOLVED, etc.) to match the
// example in §2.2.2. An empty state field renders without the
// trailing `[...]` so partially-populated records render visibly
// rather than producing `... [ ]`.
func RenderSpineDisplay(rec store.SpineRecord) string {
	anchors := strings.Join(rec.Anchors, ", ")
	if rec.State == "" {
		return fmt.Sprintf("%s [%s] — %s", rec.ID, anchors, rec.Summary)
	}
	return fmt.Sprintf("%s [%s] — %s [%s]",
		rec.ID, anchors, rec.Summary, strings.ToUpper(string(rec.State)))
}
