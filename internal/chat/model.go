package chat

import (
	"context"
	"fmt"
	"strings"

	"personant/internal/curator"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/term"
	"personant/internal/turn"
)

// modelSelector is the session's /model policy — the counterpart of
// [thinking] for the chat model, and the one owner of the session's
// "which endpoint, which model" answer after bootstrap.
//
// It holds what the command needs and Run resolves once: the provider
// pool, the currently-active provider name, and the factory a provider
// switch builds its client through. The model itself is NOT duplicated
// here — it lives on turn.State.Model, which is what the turn path
// reads, and a second copy would be a second truth.
//
// Concurrency: none. The REPL loop is one goroutine, and a slash command
// and a turn are two branches of the same switch — so a switch cannot
// land while a turn is streaming, and the fields the switch writes are
// read by the next turn on the same goroutine. There is nothing to
// synchronize and deliberately no lock: adding one would imply a
// concurrent writer that does not exist.
type modelSelector struct {
	// pool is the WHOLE providers.toml pool. The ambiguity rule below
	// reads it (not the inference subset) so that a `search/...` first
	// segment gets the honest kind-mismatch refusal rather than being
	// silently re-read as a model id.
	pool memops.Providers

	// inference is the subset a chat client may be pointed at.
	inference memops.Providers

	// provider is the active provider's name. Paired with state.Model.
	provider string

	state *turn.State

	// newClient builds the client for a provider switch. Run installs
	// model.NewHTTPClient; a caller that injected Options.Client gets that
	// client back for every provider, which is the test seam.
	newClient func(memops.Provider) model.Client
}

// modelUsage is the one-line hint the bare /model form prints. It names
// the session-only contract in the same breath as the syntax, because
// "did this change my config file?" is the first question a switch
// raises.
const modelUsage = "usage: /model <model-id> | <provider>/<model-id> — session only; config.toml [chat] defaultModel is unchanged"

// current renders the active provider/model reference.
func (s *modelSelector) current() string { return s.provider + "/" + s.state.Model }

// resolveRef splits a /model argument into a provider name and a model id.
//
// The ambiguity is real: model ids commonly contain `/` (HuggingFace-style
// `org/name`), so `a/b` is either provider `a` model `b` or the model
// `a/b` on the current provider. The rule is a LOOKUP, not a guess — the
// text before the FIRST `/` is a provider only when it names a provider
// in providers.toml; otherwise the whole argument is a model id on the
// current provider. That makes the meaning depend on the user's own pool
// rather than on a heuristic about slashes, and it agrees with the
// §8.2.2 config-reference convention (split on the first `/`) wherever a
// provider is genuinely being named.
func (s *modelSelector) resolveRef(arg string) (providerName, modelID string) {
	if p, m, ok := memops.ParseModelRef(arg); ok {
		if _, declared := s.pool[p]; declared {
			return p, m
		}
	}
	return s.provider, arg
}

// cmdModel implements /model (§4.2).
//
//	/model                       report the active provider/model
//	/model <model-id>            switch model within the current provider
//	/model <provider>/<model-id> switch provider AND model
//
// The bare form reports and never mutates (the /thinking convention).
//
// A switch VERIFIES exactly as session open does — the same
// resolveChatModel probe against the target provider's /models — so the
// two paths cannot drift: a model absent from a non-empty list refuses
// the switch and leaves the session untouched, and an unreachable
// /models warns and switches unverified rather than blocking a session
// whose provider is momentarily down.
//
// The switch is session-scoped and deliberately NOT serialized: nothing
// here writes config.toml or home state, so `[chat] defaultModel` stays
// the startup default and no "last model used" record exists to be
// resurrected on the next open.
//
// It takes effect on the NEXT turn by construction, not by scheduling:
// commands and turns are alternating branches of one loop, so there is no
// in-flight turn to touch when this runs.
func cmdModel(ctx context.Context, tm *term.Terminal, ops memops.MemoryOps, sel *modelSelector, rest string) error {
	arg := strings.TrimSpace(rest)
	if arg == "" {
		fmt.Fprintf(tm.Out(), "model: %s\n%s\n", sel.current(), modelUsage)
		return nil
	}

	providerName, modelID := sel.resolveRef(arg)
	target, ok := sel.inference[providerName]
	if !ok {
		// resolveRef only ever yields a declared provider name or the
		// active one (inference by construction), so the miss is always
		// the kind mismatch — named as such, exactly as bootstrap does,
		// because "unknown" would send the user hunting a typo.
		return fmt.Errorf("provider %q is a %q provider, not an inference endpoint; inference providers: %s",
			providerName, sel.pool[providerName].Kind(), strings.Join(sortedProviderNames(sel.inference), ", "))
	}

	// A same-provider switch keeps the session's client — same endpoint,
	// same credentials, and rebuilding it would drop an injected one.
	client := sel.state.Client
	if providerName != sel.provider {
		client = sel.newClient(target)
	}

	verified, err := resolveChatModel(ctx, client, tm.Diag(), providerName, modelID)
	if err != nil {
		// Nothing has been assigned yet, so the refusal is total: the
		// candidate client is dropped and the session keeps working on
		// exactly what it had.
		return fmt.Errorf("%w — keeping %s", err, sel.current())
	}

	from := sel.current()
	sel.state.Client = client
	sel.state.Provider = target
	sel.state.Model = verified
	sel.provider = providerName
	// The §3.5 curator PINS the client and model it was built with, so it
	// must follow the switch or a closure draft would keep going to the
	// old endpoint — which after a provider switch is a different server.
	sel.state.Curator = curator.NewHTTPCurator(client, verified)
	to := sel.current()

	fmt.Fprintf(tm.Out(), "model: %s → %s (session only — config.toml defaultModel unchanged)\n", from, to)
	return ops.Log(ctx, memops.LogCategoryModel, "switched", "from="+from+" to="+to)
}
