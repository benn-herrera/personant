package memops

import (
	"errors"
	"fmt"
	"slices"
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

// Provider kinds and wire protocols. A pool entry declares both, and
// both are REQUIRED: what the endpoint IS (`type`) and how to talk to
// it (`api`). There is deliberately no default for either. A default
// would have to be "inference over openai", which is a silent guess
// about an endpoint the runtime is about to send a credential to — and
// the failure it produces (a search backend selected as a chat provider)
// is far more confusing than the missing field it papered over. An entry
// that declares neither is a config the user has not finished writing,
// and Provider.Validate says so.
const (
	// ProviderTypeInference is an LLM endpoint: chat, embeddings,
	// model listing. The default when `type` is absent.
	ProviderTypeInference = "inference"
	// ProviderTypeSearch is a §6.1.1 web.search backend. It is NOT an
	// inference endpoint and must never be selected as one.
	ProviderTypeSearch = "search"

	// ProviderAPIOpenAI is the OpenAI-compatible HTTP protocol. The
	// default when `api` is absent.
	ProviderAPIOpenAI = "openai"
	// ProviderAPIExa is Exa's search protocol.
	ProviderAPIExa = "exa"
)

// A provider declares WHERE and HOW to reach an endpoint — never WHICH
// MODEL to use. The former `defaultModel` field was removed (user
// ruling): providers rotate their catalogues constantly, so a model id
// pinned on a pool entry is a field that begs to go stale, and a stale
// fallback fires exactly when the configured model is unavailable —
// the moment it is least likely to still be right. The model choice
// lives in config.toml [chat] / [embedding] (or --model), which is the
// one place a user looks for it.
type Provider struct {
	Name    string `toml:"-"` // table header from TOML; populated post-decode
	BaseURL string `toml:"baseUrl"`

	// Type is the provider kind — ProviderTypeInference or
	// ProviderTypeSearch. Required; see Validate.
	Type string `toml:"type"`

	// API is the wire protocol — ProviderAPIOpenAI or ProviderAPIExa.
	// Required, and it must match the Type; see Validate.
	API string `toml:"api"`

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

// Kind returns the normalized (trimmed, lower-cased) provider type. It
// exists so selection sites compare against the constants without each
// one re-deciding how to normalize a hand-edited TOML value.
func (p Provider) Kind() string { return strings.ToLower(strings.TrimSpace(p.Type)) }

// Protocol returns the normalized wire protocol.
func (p Provider) Protocol() string { return strings.ToLower(strings.TrimSpace(p.API)) }

// protocolsByKind is the legal (type, api) pairing — the single source
// of truth for both the validity check and the error messages it
// produces, so a new backend is one entry rather than three edits.
var protocolsByKind = map[string][]string{
	ProviderTypeInference: {ProviderAPIOpenAI},
	ProviderTypeSearch:    {ProviderAPIExa},
}

// Validate reports whether a pool entry is usable, checking the fields
// that decide WHERE traffic goes rather than the ones that merely
// decorate it.
//
// It is enforced at POOL LOAD, where an invalid entry is dropped and
// reported as a ProviderFault: the rest of the pool still loads, and the
// user gets a named reason instead of an endpoint that quietly went
// missing from the selection list.
func (p Provider) Validate() error {
	kind := p.Kind()
	if kind == "" {
		return fmt.Errorf("no type declared (known types: %s)", strings.Join(sortedKinds(), ", "))
	}
	apis, known := protocolsByKind[kind]
	if !known {
		return fmt.Errorf("unknown type %q (known types: %s)", p.Type, strings.Join(sortedKinds(), ", "))
	}
	api := p.Protocol()
	if api == "" {
		return fmt.Errorf("no api declared (a %s provider speaks: %s)", kind, strings.Join(apis, ", "))
	}
	if !slices.Contains(apis, api) {
		return fmt.Errorf("api %q is not valid for a %s provider (which speaks: %s)",
			p.API, kind, strings.Join(apis, ", "))
	}
	return nil
}

func sortedKinds() []string {
	out := make([]string, 0, len(protocolsByKind))
	for k := range protocolsByKind {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// OfKind returns the subset of the pool whose Kind matches. It exists so
// selection sites state which kind they want: a pool now holds
// non-inference endpoints, and an unfiltered "pick the first provider"
// fallback would happily hand a search backend to the chat client.
func (p Providers) OfKind(kind string) Providers {
	out := make(Providers, len(p))
	for name, prov := range p {
		if prov.Kind() == kind {
			out[name] = prov
		}
	}
	return out
}

// ---------- Config ----------

// Config is the personant settings file (config.toml) — the choices
// that draw from the providers.toml pool. providers.toml lists what is
// available; config.toml says which to use. config.toml is the home
// for all future personant-level configuration.
type Config struct {
	User      UserConfig      `toml:"user"`
	Chat      ChatConfig      `toml:"chat"`
	Embedding EmbeddingConfig `toml:"embedding"`
	Search    SearchConfig    `toml:"search"`
}

// UserConfig is the [user] section — who personant says it is acting for
// when it reaches a third party.
//
// It exists for the §6.1 free-API citizenship rule (CONVENTIONS.md house rule,
// user ruling 2026-08-05): the tools that talk to free, volunteer and
// donor-funded services must send a User-Agent carrying CONTACT
// INFORMATION, and the Wikimedia family REQUIRES it. Compiling a personal
// address into the binary is not an option and neither is sending
// anonymous traffic, so the identity is configuration.
//
// `personant init` populates the section from the installed git binary's
// global user.name / user.email, which is where a developer's identity
// already lives. Nothing here is secret: a name and a mail address that
// personant is about to put in a public HTTP header are, by definition,
// not credentials, so [user] sits in config.toml with the other choices
// rather than in a key file.
//
// It is DELIBERATELY unvalidated, for the same reason [search] is: an
// empty or half-filled section must cost the tools that need contact and
// nothing else — never the session.
type UserConfig struct {
	// Name is the human personant is acting for. Sent in the User-Agent.
	Name string `toml:"name"`

	// Email is the contact address a service operator would write to.
	// Sent in the User-Agent as `mailto:<email>`.
	Email string `toml:"email"`
}

// ChatConfig is the [chat] section.
//
// Layering note (deliberate, do not "fix"): ShowThinking is a pure U/X
// preference sitting in a substrate-owned config type. It lives here
// because internal/chat never sees PersonantPaths — path resolution and
// file reads are the adapter's job, and the application layer receives
// config only through the port (see the Options.Ops godoc in
// internal/chat/chat.go). A separate front-end-owned settings file would
// mean a second config format, a second loader, and a second thing for
// the user to find. One config file, delivered through the one port, is
// the right trade; the type is the substrate's, the meaning is the front
// end's, and nothing in the substrate reads this field.
type ChatConfig struct {
	// DefaultModel is a "<provider>/<model>" reference into the
	// providers.toml pool — the default chat provider and model. The
	// --provider / --model CLI flags override it.
	DefaultModel string `toml:"defaultModel"`

	// ShowThinking makes the REPL stream a thinking model's reasoning
	// deltas to the terminal, dimmed, as they arrive. It is the SESSION
	// DEFAULT only — the /thinking slash command overrides it for the
	// running session and never writes back.
	//
	// Absent → false → off. That default is deliberate: on a thinking
	// model the reasoning stream is the overwhelming majority of the
	// bytes on the wire, so a fresh install must not suddenly fill the
	// terminal with scratch the user did not ask for. It has no bearing
	// on what is stored — reasoning is never journaled, never extracted
	// for symbols, never replayed — so this switch is display-only, and
	// ValidateConfig deliberately has nothing to say about it: an
	// absent, false, or unrecognized display preference must never
	// refuse a config and block the session.
	ShowThinking bool `toml:"showThinking"`
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

// SearchConfig is the [search] section — the CHOICE of §6.1.1
// `web.search` backend from the pool, plus its local query caps.
//
// It follows config.toml's existing division of labour exactly:
// providers.toml lists what is AVAILABLE (a `type = "search"` entry
// with its own apiKeyFile), config.toml says which to USE. The key is
// therefore resolved by the one provider-pool loader, under the one
// apiKeyFile discipline — a path reference, never an inline secret —
// and no second key-resolution path exists to drift from it.
//
// The section is entirely OPTIONAL, and so is the pool entry: with no
// search provider in the pool, `web.search` is simply not registered,
// and §6.1.4's empty-registry semantics already handle an absent tool
// correctly. `web.fetch` needs no credential and registers either way.
type SearchConfig struct {
	// Provider names which pooled search provider to use. It is needed
	// only to disambiguate a pool holding more than one; with exactly
	// one, that one is used. An unrecognized name disables the tool
	// with a warning — it never refuses the session.
	Provider string `toml:"provider"`

	// MaxPerTurn / MaxPerDay are the local query caps, enforced by
	// personant rather than by watching a provider dashboard. 0 → the
	// defaults in internal/tools/web.
	MaxPerTurn int `toml:"maxPerTurn"`
	MaxPerDay  int `toml:"maxPerDay"`
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
// Both the chat and embedding references are validated for shape (a
// well-formed "provider/model" string) and provider-existence (the named
// provider is present in the pool) only. Neither model id is checked
// statically: a pool entry names no models at all (Provider carries no
// defaultModel — see its doc), and a single provider legitimately serves
// both a chat model and a distinct embedding model. The specific model
// id is resolved at runtime against the provider's own catalogue.
//
// [search] is DELIBERATELY UNVALIDATED — absent, partial, or naming a
// backend this binary does not know must never refuse a config. The
// section gates ONE optional tool, and the correct handling of every bad
// value is to not register it (with a warning), not to block the user's
// session. An over-strict check here has already blocked a launch once;
// this validator's failure mode is total, so it stays conservative.
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
		provider, _, ok := ParseModelRef(cfg.Embedding.Model)
		switch {
		case !ok:
			issues = append(issues, ConfigIssue{
				Section: "embedding",
				Message: fmt.Sprintf("model %q is not a \"provider/model\" reference", cfg.Embedding.Model),
			})
		default:
			if _, known := providers[provider]; !known {
				issues = append(issues, ConfigIssue{
					Section: "embedding",
					Message: fmt.Sprintf("embedding references provider %q, which is not in the provider pool", provider),
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
	// ErrFileVersionUnreachable is returned by GetFileVersion when an
	// aged-out committed file version cannot be recovered: the commit hash
	// is not reachable in the workspace repo, or the blob is absent at that
	// path (amend/rebase/gc orphaned it, or the workspace moved). It is the
	// §3.9.1 "recovery path unreachable" sentinel — distinguished via
	// errors.Is from a recovered-empty-file success, which the op never
	// signals as ("", nil).
	ErrFileVersionUnreachable = errors.New("memops: file version unreachable in workspace repo")
)
