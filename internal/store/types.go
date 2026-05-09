package store

import "regexp"

// ThreadState enumerates the lifecycle states of a thread (spec §2.2.1).
type ThreadState string

const (
	ThreadActive    ThreadState = "active"
	ThreadPaused    ThreadState = "paused"
	ThreadBlocked   ThreadState = "blocked"
	ThreadWIP       ThreadState = "wip"
	ThreadResolved  ThreadState = "resolved"
	ThreadDecided   ThreadState = "decided"
	ThreadAbandoned ThreadState = "abandoned"
)

// SymbolCategory enumerates the symbol classification tiers (spec §2.7.1).
// "phrasal" is deferred to v0.2 and intentionally absent.
type SymbolCategory string

const (
	SymbolIdentifier SymbolCategory = "identifier"
	SymbolEntity     SymbolCategory = "entity"
	SymbolTag        SymbolCategory = "tag"
)

// SymbolSource enumerates the provenance of a symbol emission (spec §2.7.3).
type SymbolSource string

const (
	SourceDeterministic SymbolSource = "deterministic"
	SourceModel         SymbolSource = "model"
	SourceUser          SymbolSource = "user"
	SourceCurator       SymbolSource = "curator"
)

// ID format patterns. RE2-compiled once at package init for reuse by JSONL
// readers/writers and the future `personant verify` command.
var (
	ThreadIDPattern    = regexp.MustCompile(`^thr_\d+$`)
	ProjectIDPattern   = regexp.MustCompile(`^prj_\d+$`)
	ProjectNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// SpineRecord is one line of spine.jsonl (spec §2.2). The canonical record
// of a thread's identity, recall surface, and engagement bookkeeping.
type SpineRecord struct {
	ID      string   `json:"id"`
	Project string   `json:"project"`
	Anchors []string `json:"anchors"`
	Summary string   `json:"summary"`
	State   ThreadState `json:"state"`

	Created      string `json:"created"`
	LastEngaged  string `json:"last_engaged"`
	StateChanged string `json:"state_changed"`

	TurnCount   int `json:"turn_count"`
	RecallFires int `json:"recall_fires"`
}

// HistorySymbol is one entry in a thread's frontmatter history_symbols list
// (spec §2.3).
type HistorySymbol struct {
	Raw            string       `json:"raw" yaml:"raw"`
	Normalized     string       `json:"normalized" yaml:"normalized"`
	FirstSeenTurn  int          `json:"first_seen_turn" yaml:"first_seen_turn"`
	Count          int          `json:"count" yaml:"count"`
	Source         SymbolSource `json:"source" yaml:"source"`
}

// ThreadFrontmatter is the YAML frontmatter for threads/thr_<id>.md
// (spec §2.3). Mirrors SpineRecord plus per-thread symbol history.
//
// Both JSON and YAML tags are present so a future YAML helper is a one-line
// change; no YAML dependency is added at this stage.
type ThreadFrontmatter struct {
	ID      string      `json:"id" yaml:"id"`
	Project string      `json:"project" yaml:"project"`
	Anchors []string    `json:"anchors" yaml:"anchors"`
	Summary string      `json:"summary" yaml:"summary"`
	State   ThreadState `json:"state" yaml:"state"`

	Created      string `json:"created" yaml:"created"`
	LastEngaged  string `json:"last_engaged" yaml:"last_engaged"`
	StateChanged string `json:"state_changed" yaml:"state_changed"`

	TurnCount   int `json:"turn_count" yaml:"turn_count"`
	RecallFires int `json:"recall_fires" yaml:"recall_fires"`

	HistorySymbols []HistorySymbol `json:"history_symbols" yaml:"history_symbols"`
}

// SymbolRecord is one line of symbols.jsonl (spec §2.4). Derived inverse
// index: symbol → threads where it appears.
type SymbolRecord struct {
	Symbol         string       `json:"symbol"`
	Threads        []string     `json:"threads"`
	AnchorIn       []string     `json:"anchor_in"`
	SourceDominant SymbolSource `json:"source_dominant"`
}

// ProjectPattern is a project-scoped regex for the deterministic symbol
// extraction pass (spec §2.5.1).
type ProjectPattern struct {
	Name     string         `json:"name"`
	Regex    string         `json:"regex"`
	Category SymbolCategory `json:"category"`
}

// ProjectMeta is projects/prj_<n>/meta.json (spec §2.5.1). Canonical
// project metadata; the storage key is ID, the display label is Name.
type ProjectMeta struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	CurrentRootPath     string   `json:"current_root_path"`
	HistoricalRootPaths []string `json:"historical_root_paths,omitempty"`

	RemoteURLs           []string `json:"remote_urls,omitempty"`
	HistoricalRemoteURLs []string `json:"historical_remote_urls,omitempty"`

	Created     string `json:"created"`
	LastActive  string `json:"last_active"`
	ThreadCount int    `json:"thread_count"`

	ConventionsPaths []string         `json:"conventions_paths"`
	SymbolPatterns   []ProjectPattern `json:"symbol_patterns"`
	IgnoreSymbols    []string         `json:"ignore_symbols"`
}

// ProjectDigest is projects/prj_<n>/digest.json (spec §2.5.2). Derived
// Layer A2 content; regenerated when any thread in the project changes.
type ProjectDigest struct {
	Project        string   `json:"project"`
	DisplayName    string   `json:"display_name"`
	ThreadCount    int      `json:"thread_count"`
	RecentAnchors  []string `json:"recent_anchors"`
	OneLineSummary string   `json:"one_line_summary"`
	ByteSize       int      `json:"byte_size"`
}
