// Package tools is the §6.1 external tool surface: a name→(spec, handler)
// registry plus the dispatch that turns one model-requested call into a
// result the model can read.
//
// It is deliberately substrate-free and unaware of internal/turn. A tool
// is a pure (context, raw JSON args) → (bytes, error) function paired with
// the model-facing spec that advertises it; the turn loop owns the
// conversation, this package owns the inventory. Nothing here touches the
// event log, the working set, or the §4.5.8 recovery scope.
//
// The registry ships EMPTY. Registering the real tools (`web.fetch`,
// `web.search`, …) is a separate concern from being able to run them.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"personant/internal/model"
)

// Handler executes one tool call.
//
// args is the RAW JSON `arguments` value exactly as the provider sent it.
// OpenAI-compatible providers encode the argument object as a JSON
// *string*, so a handler unmarshals args into a string first and then that
// string into its own argument struct — see model.ToolCall.Args, which
// documents the same representation on both the blocking and the streaming
// path. A handler that gets a shape it cannot parse returns an error;
// Dispatch shapes it into something the model can act on.
//
// The returned bytes become the tool message's content verbatim. A handler
// never writes protocol text of its own, and never truncates for budget —
// the §6.5 cap is applied by the caller (internal/turn), on the delta, so
// one policy covers every task-class source.
//
// A handler MUST honour ctx: Dispatch gives it a deadline, and the user's
// §4.3.3 Esc abort cancels the turn context it derives from.
type Handler func(ctx context.Context, args json.RawMessage) ([]byte, error)

// Tool is one registered tool: what the model sees, what runs, and the
// policy metadata the permission layer will need.
type Tool struct {
	// Spec is the model-facing declaration (name, description, JSON-Schema
	// parameters). Spec.Name is the registry key and the name the model
	// calls back with.
	Spec model.ToolSpec

	// Handler runs the call. Required.
	Handler Handler

	// Tier is the §6.2 permission tier: 0 silent, 1 first-time ack with a
	// scope-grant offer, 2 always individual ack. TierSilent is the only
	// tier the runtime implements today — the field exists so a tier-1/2
	// tool can be described without re-shaping this struct, NOT because
	// the accrual machinery exists. Every §6.1.1 read/think tool is ruled
	// tier 0 (all reads are tier 0 inside the active project), so nothing
	// currently needs the rest.
	Tier int

	// Mutates reports whether the tool changes state outside personant's
	// own home. Register REFUSES a mutating tool — see
	// ErrMutatingToolUnsupported for the crash-replay reason, which is a
	// real correctness constraint and not a stylistic one.
	Mutates bool
}

// TierSilent (§6.2.1 tier 0, "never ack") is the only permission tier the
// runtime implements. It is named so a registration site states its tier
// rather than leaving a bare 0, and so the day tier 1/2 arrive there is a
// place the reader already looks.
const TierSilent = 0

// ErrMutatingToolUnsupported is returned by Register for a tool declaring
// Mutates. It is a TRIP-WIRE, deliberately placed where a future agent
// adding a mutating tool cannot miss it.
//
// Why: tool rounds run inside the turn's §4.5.8 recovery scope, and the
// calls and their results are NOT journaled (see the journaling decision
// in internal/turn/toolloop.go). A crash mid-tool-round therefore loses
// the turn, and replay re-executes the tools — execution is AT-LEAST-ONCE.
// For a read-only tool that is harmless. For a mutating one it is a
// correctness bug: the mutation happens twice, or happens for a turn the
// user never sees completed.
//
// Enabling one is not a matter of clearing this flag. It requires deciding
// how a mutating call becomes exactly-once (journal the call and its
// result so replay replays the RESULT rather than the call, or make the
// mutation idempotent and prove it), and §6.1.2's `propose_*` tools are
// additionally ack-gated, which is a separate unbuilt mechanism.
var ErrMutatingToolUnsupported = errors.New(
	"mutating tools are unsupported: tool execution is at-least-once under #94 crash replay " +
		"(see the journaling decision in internal/turn/toolloop.go) and no exactly-once mechanism exists")

// ErrUnknownTool is the cause wrapped into a Result.Err when the model
// named a tool the registry does not hold. Match with errors.Is.
var ErrUnknownTool = errors.New("unknown tool")

// DefaultTimeout bounds one handler call. It exists so a wedged handler
// costs the turn a bounded wait instead of the whole session: the model
// gets a timeout result it can act on, the turn continues. A handler with
// a genuinely longer job is the wrong shape for a turn-blocking tool.
const DefaultTimeout = 30 * time.Second

// Registry maps tool name → Tool.
//
// Build it at startup and treat it as immutable afterwards: Register is
// not safe against concurrent Lookup, while Lookup/Specs/Len on a
// fully-built registry are safe for concurrent use (read-only map access).
//
// A nil *Registry is a valid EMPTY registry — every method tolerates it —
// so a caller with no tools configured stores nil rather than a special
// case.
type Registry struct {
	byName map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{byName: make(map[string]Tool)} }

// Register adds t. It fails on an empty name, a nil handler, a duplicate
// name, or a mutating tool (ErrMutatingToolUnsupported).
func (r *Registry) Register(t Tool) error {
	if r == nil {
		return errors.New("tools: register: nil registry")
	}
	name := t.Spec.Name
	switch {
	case name == "":
		return errors.New("tools: register: empty tool name")
	case t.Handler == nil:
		return fmt.Errorf("tools: register %q: nil handler", name)
	case t.Mutates:
		return fmt.Errorf("tools: register %q: %w", name, ErrMutatingToolUnsupported)
	}
	if r.byName == nil {
		r.byName = make(map[string]Tool)
	}
	if _, dup := r.byName[name]; dup {
		return fmt.Errorf("tools: register %q: already registered", name)
	}
	r.byName[name] = t
	return nil
}

// Lookup returns the tool registered under name.
func (r *Registry) Lookup(name string) (Tool, bool) {
	if r == nil {
		return Tool{}, false
	}
	t, ok := r.byName[name]
	return t, ok
}

// Len reports how many tools are registered.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.byName)
}

// Specs returns the model-facing specs, SORTED BY NAME.
//
// Two properties are load-bearing:
//
//   - It returns nil for an empty registry. The caller assigns the result
//     straight to model.Request.Tools, and a nil/empty slice is what keeps
//     the `tools` field off the wire entirely (encodeRequest omits it).
//     Advertising an empty tool list would invite calls nothing can
//     service.
//   - The order is deterministic. Map iteration order is not, and the tool
//     block sits in the request prefix — a request that reshuffles between
//     otherwise identical turns is unreproducible AND defeats the
//     provider's prompt cache (usage.cached_tokens is a measured figure
//     here, see model.Usage).
func (r *Registry) Specs() []model.ToolSpec {
	if r.Len() == 0 {
		return nil
	}
	names := make([]string, 0, len(r.byName))
	for name := range r.byName {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]model.ToolSpec, 0, len(names))
	for _, name := range names {
		out = append(out, r.byName[name].Spec)
	}
	return out
}
