package prompt

import "strings"

// SystemPromptElements carries the inputs needed to assemble the system
// prompt. Layer fields are pre-rendered strings produced by the working-set
// composer (spec §3.1, Phase 2.e); this package does not assemble them —
// it only specifies the slots and stitches them together with the
// substantive directive content.
//
// Empty layer strings are skipped (their section header is omitted) so the
// prompt stays clean during early Phase 2 work where most layers will be
// empty.
type SystemPromptElements struct {
	LayerE  string // directive files + project conventions (§3.1)
	LayerA1 string // current project's spine entries (display form per §2.2.2)
	LayerA2 string // other projects' compressed digests
	LayerB  string // actively engaged threads, full content
	LayerC  string // recently engaged dormant threads, summaries
}

// systemPromptPreamble is the orientation block that opens every prompt.
// Kept short — the substantive instruction is the topic-tag directive that
// follows it.
const systemPromptPreamble = `You are personant — a research-assistant runtime with persistent working
memory across projects. The information below is your working context for
the current turn.`

// TopicTagDirective is the load-bearing instruction to the model about the
// topic tag emitted at response start (spec §5.1). Exposed so tests can
// assert verbatim presence and so a future hot-reload mechanism (§5.1.3)
// has a stable identifier to swap.
const TopicTagDirective = `TOPIC TAG REQUIREMENT

Begin every response with a topic tag in this exact form, on its own line:

    *topic: <thread-list> [<anchor-list>]*

where:
  - <thread-list> is a comma-separated list of one or more thread IDs of the
    form "thr_<n>" referring to threads from the spine (above), OR the literal
    "*new-topic*" if you are starting a new line of work.
  - <anchor-list> is a comma-separated list of 4 to 8 short symbols that
    capture what this turn is about. Symbols are typically lowercase and
    hyphenated for multi-word concepts (e.g. "body-topology", "electron-shape");
    code identifiers preserve their original case.

Examples:

    *topic: thr_42 [trefoil, unknot, body-topology, electron-shape]*
    *topic: thr_42, thr_88 [trefoil, neutrino, helical-screw, oscillation]*
    *topic: *new-topic* [neutrino, oscillation, mass-hierarchy, beta-decay]*

After the tag, write your response normally. The runtime parses the tag
deterministically; getting the format exactly right matters.`

// layerSection holds one layer's header and rendered content. The order of
// sections in the assembled prompt is fixed by the slice order in
// BuildSystemPrompt; the section is omitted entirely if its content is
// empty.
type layerSection struct {
	header  string
	content string
}

// BuildSystemPrompt assembles the system prompt for a turn. The structure
// is:
//
//  1. Orientation preamble.
//  2. Topic-tag directive (§5.1).
//  3. Layer sections E, A1, A2, B, C — each emitted only if non-empty.
//
// Sections are separated by a blank line. The prompt is not terminated
// with a trailing newline; callers can add framing as needed.
func BuildSystemPrompt(p SystemPromptElements) string {
	sections := []layerSection{
		{header: "PROJECT CONVENTIONS AND DIRECTIVES (Layer E)", content: p.LayerE},
		{header: "CURRENT PROJECT SPINE (Layer A1)", content: p.LayerA1},
		{header: "OTHER PROJECTS (Layer A2 — compressed digests)", content: p.LayerA2},
		{header: "ACTIVELY ENGAGED THREADS (Layer B)", content: p.LayerB},
		{header: "RECENTLY DORMANT THREADS (Layer C — summaries)", content: p.LayerC},
	}

	parts := make([]string, 0, 2+len(sections))
	parts = append(parts, systemPromptPreamble)
	parts = append(parts, TopicTagDirective)

	for _, s := range sections {
		if s.content == "" {
			continue
		}
		parts = append(parts, s.header+"\n"+s.content)
	}

	return strings.Join(parts, "\n\n")
}
