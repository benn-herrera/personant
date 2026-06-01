package memops

import (
	"errors"
	"fmt"
	"strings"
)

// This file holds the domain data-model types that the MemoryOps port
// owns outright: the spine/thread/project records, the symbol and
// state enums, the provider/config records, and the sentinel errors.
//
// These types are the port's contract. The substrate (internal/store)
// and its adapters depend on memops for them — the abstraction owns its
// own data model, and the dependency arrow runs substrate → port. The
// pure functions ParseModelRef and ValidateConfig live here too: they
// operate only on these types and perform no I/O, so they belong with
// the contract rather than the substrate.

// ---------- Thread / symbol state enums ----------

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

// SymbolLifecycle enumerates a history symbol's lifecycle state (spec
// §2.3 / §2.7.x). It is the canonical source of truth for supersession
// state: "evicted" is the *absence* of the entry, not a stored value.
// The zero value "" is treated as LifecycleActive (back-compatible: an
// old record with no lifecycle field decodes as active).
type SymbolLifecycle string

const (
	// LifecycleActive marks a symbol currently in (or eligible for) the
	// active anchor projection. "" decodes as active.
	LifecycleActive SymbolLifecycle = "active"
	// LifecycleSuperseded marks a once-central symbol that has fallen out
	// of the top-AnchorProjectionMax projection (rank-dropout). It is
	// retained-not-evicted so an abandoned premise stays a findable
	// recall handle.
	LifecycleSuperseded SymbolLifecycle = "superseded"
)

// ---------- Spine / thread records ----------

// SpineRecord is one line of spine.jsonl (spec §2.2). The canonical record
// of a thread's identity, recall surface, and engagement bookkeeping.
type SpineRecord struct {
	ID string `json:"id"`

	Project string `json:"project"`

	// Anchors is the thread's headline symbol set — [derived]: a
	// deterministic re-derived projection of the thread's active
	// history_symbols (spec §2.2 / §2.7.x), not a frozen birth
	// certificate. Range 0..AnchorProjectionMax (the 4-floor is deleted;
	// 0 is legal for a vague-start thread). Projected anchors ≤
	// AnchorProjectionMax is a runtime self-assertion, not a model
	// contract.
	Anchors []string `json:"anchors"`

	Summary string      `json:"summary"`
	State   ThreadState `json:"state"`

	// AnchorsProjectedAtTurn is the staleness watermark: the owner-turn
	// index at which the Anchors projection last changed. It enables the
	// idempotent-write guard — the spine line is treated as changed (and
	// this field bumped) only when the projected anchor set actually
	// moves. A missing field in old JSON decodes to 0.
	AnchorsProjectedAtTurn int `json:"anchors_projected_at_turn"`

	// Description is the triggering utterance — the user prompt that
	// spawned the thread — set once at creation and never rewritten
	// (spec §2.3). Distinct from Summary, which is the curator's closure
	// gist set at retirement. A missing field in old JSON decodes empty
	// (back-compatible; no migration).
	Description string `json:"description"`

	Created      string `json:"created"`
	LastEngaged  string `json:"last_engaged"`
	StateChanged string `json:"state_changed"`

	TurnCount   int `json:"turn_count"`
	RecallFires int `json:"recall_fires"`

	// LastEngagedTurn is the global session-monotonic turn index
	// (State.TurnNumber) at which the thread was last engaged — the
	// basis for turn-based engagement decay (spec §3.5). A missing
	// field in old JSON decodes to 0, treated as "very old".
	LastEngagedTurn int `json:"last_engaged_turn"`
}

// HistorySymbol is one entry in a thread's frontmatter history_symbols list
// (spec §2.3).
type HistorySymbol struct {
	Raw           string       `json:"raw" yaml:"raw"`
	Normalized    string       `json:"normalized" yaml:"normalized"`
	FirstSeenTurn int          `json:"first_seen_turn" yaml:"first_seen_turn"`
	Count         int          `json:"count" yaml:"count"`
	Source        SymbolSource `json:"source" yaml:"source"`

	// Lifecycle is the symbol's lifecycle state (spec §2.7.x). The zero
	// value "" is treated as LifecycleActive — an old record with no
	// lifecycle field decodes as active. Canonical source of truth for
	// supersession state.
	Lifecycle SymbolLifecycle `json:"lifecycle,omitempty" yaml:"lifecycle,omitempty"`

	// EverCentral is latched true the first time the symbol enters the
	// active anchor projection, and never cleared. It is the retention
	// discriminator: an ever-central symbol is never capacity-evicted, so
	// an abandoned premise stays a findable recall handle. Zero value
	// false ≡ never-central.
	EverCentral bool `json:"ever_central,omitempty" yaml:"ever_central,omitempty"`

	// LastActiveTurn is the most recent turn the symbol was in the active
	// projection — temporal ordering (with FirstSeenTurn) plus an
	// eviction tiebreak. Zero value 0 ≡ never-active.
	LastActiveTurn int `json:"last_active_turn,omitempty" yaml:"last_active_turn,omitempty"`

	// DerivedFrom records the origin thread ID(s) this symbol was carried
	// into this thread from — observed co-incident with a recall hit
	// during the same turn (spec §2.7.3). It is honest origin provenance,
	// populated deterministically at turn-close (no LLM, no human): when a
	// newly-emitted symbol's normalized form also appears in a thread
	// recalled this turn, that thread's id is unioned in. Set-valued,
	// sorted, deduplicated; monotonic — origins accumulate and are never
	// cleared. Provenance only: it does NOT participate in the §3.4 recall
	// match. nil-safe; the zero value (omitted/empty) ≡ organic to this
	// thread (the common case). An old record without the field decodes to
	// empty (back-compatible; no migration).
	DerivedFrom []string `json:"derived_from,omitempty" yaml:"derived_from,omitempty"`
}

// ThreadMeta is the per-thread metadata record at the memops port.
// In the file-substrate adapter, ThreadMeta is serialized as the YAML
// frontmatter of thread.md per spec §2.3; other substrates would
// serialize equivalently in their native shapes. The port name
// describes the abstract concept (per-thread metadata); the spec's
// "frontmatter" vocabulary correctly describes the file-substrate
// storage slot where ThreadMeta lives on disk. See ARCHITECTURE.md
// "Port abstraction policy" for the design rule this rename honors.
//
// Mirrors SpineRecord plus per-thread symbol history. Both JSON and
// YAML tags are present so a future YAML helper is a one-line change;
// no YAML dependency is added at this stage.
type ThreadMeta struct {
	ID      string      `json:"id" yaml:"id"`
	Project string      `json:"project" yaml:"project"`
	Anchors []string    `json:"anchors" yaml:"anchors"`
	Summary string      `json:"summary" yaml:"summary"`
	State   ThreadState `json:"state" yaml:"state"`

	// Description is the triggering utterance — the user prompt that
	// spawned the thread — set once at creation and never rewritten
	// (spec §2.3). Distinct from Summary, the curator's closure gist.
	// A missing field in old YAML decodes empty (back-compatible).
	Description string `json:"description" yaml:"description"`

	Created      string `json:"created" yaml:"created"`
	LastEngaged  string `json:"last_engaged" yaml:"last_engaged"`
	StateChanged string `json:"state_changed" yaml:"state_changed"`

	TurnCount   int `json:"turn_count" yaml:"turn_count"`
	RecallFires int `json:"recall_fires" yaml:"recall_fires"`

	// LastEngagedTurn is the global session-monotonic turn index
	// (State.TurnNumber) at which the thread was last engaged — the
	// basis for turn-based engagement decay (spec §3.5). A missing
	// field in old YAML decodes to 0, treated as "very old".
	LastEngagedTurn int `json:"last_engaged_turn" yaml:"last_engaged_turn"`

	HistorySymbols []HistorySymbol `json:"history_symbols" yaml:"history_symbols"`
}

// Thread is the in-memory shape of a thread: metadata plus markdown
// body. The metadata is canonical; the body is operational content
// (turn excerpts, curator-summarized milestones at retirement). Per
// spec §2.3.
type Thread struct {
	Meta ThreadMeta
	Body string // markdown body; trailing newline preserved
}

// ThreadExcerpt is one retained turn-excerpt of a thread, kept as a
// discrete unit rather than joined into the assembled body. It is the
// chunk unit the fine-tier embedding index (§3.4 / intra-thread recall)
// embeds: one excerpt → one chunk vector. Returned by
// MemoryOps.LoadThreadExcerpts in turn-number order.
type ThreadExcerpt struct {
	TurnNumber int
	Text       string // the excerpt body; trailing newline trimmed
}

// ---------- Project records ----------

// ProjectPattern is a project-scoped regex for the deterministic symbol
// extraction pass (spec §2.5.1).
type ProjectPattern struct {
	Name     string         `json:"name"`
	Regex    string         `json:"regex"`
	Category SymbolCategory `json:"category"`
}

// DefaultProjectID is the reserved id for the no-project escape hatch
// described in spec §2.1 / §2.5.1. The default project always exists
// (synthesized on demand by the substrate when no meta.json has been
// written) so callers never need to special-case "no active project".
const DefaultProjectID = "prj_default"

// AnchorProjectionMax is the hard ceiling on a thread's projected anchor
// count (spec §2.2 / §2.7.x): the deterministic projection takes the
// top-AnchorProjectionMax active history_symbols as the thread's headline
// anchor set. The legacy 4-anchor floor is deleted — 0 anchors is legal
// (a vague-start thread), and over-AnchorProjectionMax emissions fold
// (the projection keeps the strongest; the model never aborts on count).
// The bound is a runtime self-assertion the projection owns
// deterministically, not a contract on the model's §5.1 emission.
//
// §9 calibration window: 8 inherits the Phase C.6 Jaccard dilution
// ceiling and is re-confirmed under drift in the simulation — the value
// is not final.
const AnchorProjectionMax = 8

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

// ---------- Provider pool ----------

// Provider describes one LLM provider's connectivity (spec §8.2.1).
// providers.toml is the provider *pool* — connectivity only; it does
// not pin or prefer anything. The chat/embedding choices that draw
// from this pool live in config.toml (see Config).
//
// The API key is secret-bearing. The recommended form is APIKeyFile (a
// path to a key file kept out of the scannable config); APIKeyUnsafe
// is the legacy inline form. Either way the resolved key lands in
// APIKey, which the runtime is the sole consumer of — it must never
// appear in a log line, error message, or LLM-bound context.

// LocalProviderName is the conventional providers.toml name for the
// on-machine OpenAI-compatible endpoint (e.g. llama-server). It is the
// default provider for CLI subcommands when --provider is unset.
const LocalProviderName = "local"

type Provider struct {
	Name         string `toml:"-"` // table header from TOML; populated post-decode
	BaseURL      string `toml:"baseUrl"`
	DefaultModel string `toml:"defaultModel"`

	// APIKeyUnsafe is an inline API key. Discouraged — it places a
	// secret directly in providers.toml, making the file unsafe to
	// scan. Prefer APIKeyFile.
	APIKeyUnsafe string `toml:"apiKeyUnsafe"`

	// APIKeyFile is a path to a file holding the API key. Relative
	// paths resolve against the providers.toml directory. This is the
	// recommended form: the secret stays out of providers.toml, so the
	// config file itself is safe to scan and edit.
	APIKeyFile string `toml:"apiKeyFile"`

	// APIKey is the resolved key — populated by LoadProviders from
	// APIKeyFile (preferred) or APIKeyUnsafe. Not a TOML field.
	APIKey string `toml:"-"`
}

// Providers is a name-keyed set of providers loaded from providers.toml.
type Providers map[string]Provider

// ProviderFault names a provider that parsed correctly but could not
// be fully loaded — its apiKeyFile was unreadable. The provider is
// omitted from the returned Providers map; the fault lets the caller
// surface the problem without discarding the rest of the pool.
type ProviderFault struct {
	Name   string
	Reason string // never carries key content — path/IO detail only
}

// Get returns the provider by name. The second return is false when the
// name is unknown (i.e. not declared in providers.toml).
func (p Providers) Get(name string) (Provider, bool) {
	v, ok := p[name]
	return v, ok
}

// ---------- Config ----------

// Config is the personant settings file (config.toml) — the choices
// that draw from the providers.toml pool. providers.toml lists what is
// available; config.toml says which to use. config.toml is the home
// for all future personant-level configuration.
type Config struct {
	Chat      ChatConfig      `toml:"chat"`
	Embedding EmbeddingConfig `toml:"embedding"`
}

// ChatConfig is the [chat] section.
type ChatConfig struct {
	// DefaultModel is a "<provider>/<model>" reference into the
	// providers.toml pool — the default chat provider and model. The
	// --provider / --model CLI flags override it.
	DefaultModel string `toml:"defaultModel"`
}

// EmbeddingConfig is the [embedding] section — the §3.4 layer-2
// embedding choice.
type EmbeddingConfig struct {
	// Model is a "<provider>/<model>" reference — the pinned embedding
	// provider and model. The embedding model defines the vector
	// space, so this pin is deliberate and stable: it must not drift
	// with the chat provider, and changing it invalidates any
	// persisted embedding index.
	Model string `toml:"model"`

	// VectorLength, when > 0, requests a Matryoshka-truncated
	// embedding of that dimensionality. 0 → the model's native
	// dimension.
	VectorLength int `toml:"vectorLength"`
}

// ConfigIssue is one cross-file validation problem found by ValidateConfig.
type ConfigIssue struct {
	Section string // "chat" or "embedding"
	Message string
}

// ValidateConfig cross-checks a loaded config.toml against the provider
// pool. It returns one ConfigIssue per problem; an empty slice means the
// config is valid. An empty [chat] or [embedding] reference is NOT an
// issue — it means "not configured, fall back to defaults".
//
// The chat model id is deliberately not validated here: chat model
// correctness is resolved at runtime against the provider's /models
// endpoint. The embedding model id, by contrast, is a hard static pin —
// it defines the vector space — so it must match the provider's
// defaultModel verbatim.
func ValidateConfig(cfg Config, providers Providers) []ConfigIssue {
	var issues []ConfigIssue

	if cfg.Chat.DefaultModel != "" {
		provider, _, ok := ParseModelRef(cfg.Chat.DefaultModel)
		switch {
		case !ok:
			issues = append(issues, ConfigIssue{
				Section: "chat",
				Message: fmt.Sprintf("defaultModel %q is not a \"provider/model\" reference", cfg.Chat.DefaultModel),
			})
		default:
			if _, known := providers[provider]; !known {
				issues = append(issues, ConfigIssue{
					Section: "chat",
					Message: fmt.Sprintf("chat references provider %q, which is not in the provider pool", provider),
				})
			}
		}
	}

	if cfg.Embedding.Model != "" {
		provider, model, ok := ParseModelRef(cfg.Embedding.Model)
		switch {
		case !ok:
			issues = append(issues, ConfigIssue{
				Section: "embedding",
				Message: fmt.Sprintf("model %q is not a \"provider/model\" reference", cfg.Embedding.Model),
			})
		default:
			p, known := providers[provider]
			switch {
			case !known:
				issues = append(issues, ConfigIssue{
					Section: "embedding",
					Message: fmt.Sprintf("embedding references provider %q, which is not in the provider pool", provider),
				})
			case model != p.DefaultModel:
				issues = append(issues, ConfigIssue{
					Section: "embedding",
					Message: fmt.Sprintf("embedding model %q does not match provider %q defaultModel %q", model, provider, p.DefaultModel),
				})
			}
		}
	}

	return issues
}

// ParseModelRef splits a "<provider>/<model>" config reference. The
// split is on the FIRST '/', so the model part may itself contain
// slashes (e.g. "openrouter/google/gemma-4-31b-it"). ok is false when
// the reference is empty or lacks a non-empty provider and model.
func ParseModelRef(ref string) (provider, model string, ok bool) {
	ref = strings.TrimSpace(ref)
	i := strings.IndexByte(ref, '/')
	if i <= 0 || i == len(ref)-1 {
		return "", "", false
	}
	return ref[:i], ref[i+1:], true
}

// ---------- Sentinel errors ----------

var (
	// ErrThreadNotFound is returned when an operation targets a
	// thread ID that has no spine record.
	ErrThreadNotFound = errors.New("store: thread id not found in spine")
	// ErrDuplicateThreadID is returned by CreateThread when its
	// rec.ID already exists in the spine.
	ErrDuplicateThreadID = errors.New("store: duplicate thread id in spine")
	// ErrThreadFileNotFound is returned by LoadThread when the
	// canonical thread file is absent. Callers use it to
	// distinguish "fresh thread about to be created" from a parse
	// failure.
	ErrThreadFileNotFound = errors.New("store: thread file not found")
	// ErrProjectNotFound is returned by LoadProject when a
	// non-default project's meta.json is absent.
	ErrProjectNotFound = errors.New("store: project not found")
)
