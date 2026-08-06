// Package chat implements the interactive REPL for `personant chat`
// (and the bare `personant` invocation, which delegates here).
//
// The REPL bootstraps the active project (spec §4.5.7), prints a
// banner, and loops on stdin: dispatches slash commands, runs §4.4 shell
// escapes through internal/shell, and otherwise hands input to the turn
// package (internal/turn) for the §3.0 chain.
package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"personant/internal/clock"
	"personant/internal/curator"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/shell"
	"personant/internal/term"
	"personant/internal/turn"
	"personant/internal/version"
	"personant/internal/workset"
)

// Options carries the knobs the cobra layer passes through.
type Options struct {
	// Ops is the substrate port the REPL drives. Required.
	//
	// Constructing the adapter (and resolving $PERSONANT_HOME paths) is the
	// caller's responsibility — cmd/chat.go does this for the CLI, tests
	// build a fileadapter rooted at t.TempDir(). Keeping path resolution
	// out of chat is the architectural rule: application packages never
	// see PersonantPaths, only the port.
	Ops memops.MemoryOps

	ExplicitProject string // --project flag; "" → run the §4.5.7 waterfall
	ProviderName    string // optional override of the default provider name
	Model           string // optional override of the provider's default model

	// The session's three streams. Required, and supplied by the caller:
	// cmd/ names the process's real fds (composition root), tests supply
	// buffers. This package never names os.Stdin/Stdout/Stderr itself —
	// deciding WHICH fds a session runs on is a wiring question, not a
	// policy one, and the W5 gate-1 burn-down is exactly that entry.
	//
	// They are handed straight to internal/term at Open and go no further:
	// every byte the REPL emits afterwards goes out through term's four
	// channels, which share one serialization point. Reaching for a stream
	// below that line would put a second writer on the terminal, which is
	// the defect class term exists to remove.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Client overrides the default HTTP client. Tests use this to inject a
	// mock; production callers leave it nil.
	Client model.Client

	// Banner overrides the default startup banner. Empty → use Banner.
	Banner string

	// HistoryFile is the absolute path to the REPL line-edit history file
	// (§2.1 <home>/history; §4.3.1). Empty → history is not persisted (the
	// buffered/test path never persists regardless). cmd/chat.go sets this
	// from the resolved PERSONANT_HOME; keeping path resolution in cmd
	// preserves the rule that application packages never see PersonantPaths.
	HistoryFile string
}

const (
	// Banner is printed once on startup. Single line, terse.
	Banner = "personant — type /help for commands; Ctrl-D to exit"

	// replPrompt is the REPL's own single-line prompt — the one string the
	// editor renders and must not repaint above.
	replPrompt = "> "

	// turnTimeout caps a single LLM round-trip. Phase 2.c.2 does not yet
	// thread a deadline through from a directive; this is a process-wide
	// safety bound.
	turnTimeout = 5 * time.Minute
)

// Run starts the chat REPL. Returns nil on a clean exit (Ctrl-D,
// /quit, /exit) and a non-nil error only on bootstrap failure or an
// unrecoverable mid-session condition. Per-turn errors are surfaced
// to stderr but do not terminate the session.
func Run(opts Options) error {
	if opts.Ops == nil {
		return errors.New("chat: Options.Ops is required")
	}

	// The arbiter (SOLUTION.md §1). Opened before ANY output, so that
	// every byte this session emits — banner, warning, error, indicator
	// frame, reasoning — passes through one serialization point and an
	// error mid-stream cannot interleave into the wait indicator's line.
	//
	// From W3 this call also captures the entry terminal mode, installs the
	// ONE session mode, and starts the single input pump — so it must
	// happen before anything else touches the terminal, and the deferred
	// Close below is what puts the mode back. Every prompt in this package
	// goes through tm.ReadLine, which is why the history file is handed
	// over here rather than opened a second time.
	tm, err := term.Open(term.Options{
		Stdin:       opts.Stdin,
		Stdout:      opts.Stdout,
		Stderr:      opts.Stderr,
		HistoryFile: opts.HistoryFile,
		TermEnv:     os.Getenv("TERM"),
	})
	if err != nil {
		return fmt.Errorf("chat: open terminal: %w", err)
	}
	defer func() { _ = tm.Close() }()

	ctx := context.Background()
	ops := opts.Ops

	// Idempotent home scaffold. If the home is already initialized this is
	// a near-noop; if it's a bare directory (e.g. user manually created
	// providers.toml without running `personant init`), this lands the
	// canonical layout (spine.jsonl, threads/, projects/, directives/, .git/,
	// etc.) without clobbering accrued state files (user.md, providers.toml).
	if err := ops.Init(ctx, memops.InitOptions{Quiet: true}); err != nil {
		return fmt.Errorf("chat: scaffold home: %w", err)
	}

	// Startup reconcile (#94, SPEC §4.5.8): Init → Reconcile →
	// LoadSession, in that order. Reconcile repairs any unclean-shutdown
	// state before a single session read happens; a failure REFUSES the
	// session — opening an unreconciled substrate is how torn state gets
	// read back as truth. The retry is idempotent (the adapter leaves an
	// in-progress recovery marker behind), so the user just relaunches
	// once the underlying condition is fixed.
	recoveryReport, err := ops.Reconcile(ctx)
	if err != nil {
		return fmt.Errorf("chat: substrate reconcile failed — refusing to open (relaunch retries recovery): %w", err)
	}
	printRecoveryBanner(tm.Out(), recoveryReport)

	// Day barrier (#94 R3b): the session-open half of the new-day check —
	// Init → Reconcile → (barrier if new-day) → LoadSession. Ordering is
	// load-bearing: Reconcile has already repaired any torn last turn, so
	// the barrier day-commits a reconciled, consistent worktree into
	// permanent history. The pre-turn half lives in turn.RunWithInfo. A
	// barrier failure refuses the session; the op=barrier marker it
	// leaves behind makes the next open detect and complete it.
	if res, err := ops.MaybeDayBarrier(ctx); err != nil {
		return fmt.Errorf("chat: day barrier failed — refusing to open (relaunch completes it): %w", err)
	} else if len(res.DaysSealed) > 0 {
		fmt.Fprintf(tm.Out(), "day barrier: sealed day(s) %v into permanent history\n", res.DaysSealed)
	}

	rawProviders, faults, err := ops.LoadProviders(ctx)
	if err != nil {
		return fmt.Errorf("chat: load providers: %w", err)
	}
	// The port hands back the bare map; the named type is what carries
	// the pool's own vocabulary (OfKind, Get).
	providers := memops.Providers(rawProviders)
	// A broken pool entry is non-fatal: warn and carry on, so a session
	// that does not use the broken provider still starts.
	for _, fault := range faults {
		fmt.Fprintf(tm.Diag(), "warn: provider %q unavailable: %s\n", fault.Name, fault.Reason)
	}
	// The chat client may only be pointed at an INFERENCE provider. The
	// pool now also holds non-inference endpoints (a §6.1.1 `type =
	// "search"` backend), and the unfiltered default-selection fallback
	// below picks the first provider by name — which would hand the chat
	// client a search API the moment one sorts ahead of the LLM
	// endpoints.
	inference := providers.OfKind(memops.ProviderTypeInference)
	if len(inference) == 0 {
		return errors.New("chat: no inference providers configured in providers.toml; " +
			"edit it to add one (a `type = \"search\"` entry is not a chat endpoint — see spec §8.2.1)")
	}

	cfg, err := ops.LoadConfig(ctx)
	if err != nil {
		return fmt.Errorf("chat: load config: %w", err)
	}

	// Cross-file validation: every [chat]/[embedding] reference must be
	// well-formed and name a provider in the pool. Issues here are fatal
	// — a misconfigured session must fail loud at bootstrap, not midway.
	if issues := memops.ValidateConfig(cfg, providers); len(issues) > 0 {
		var b strings.Builder
		b.WriteString("chat: config.toml validation failed:")
		for _, iss := range issues {
			fmt.Fprintf(&b, "\n  [%s] %s", iss.Section, iss.Message)
		}
		return errors.New(b.String())
	}

	// Resolve the chat provider and model. Precedence: CLI flag >
	// config.toml [chat] > the conventional default provider name.
	// config.toml's "<provider>/<model>" reference names both;
	// ValidateConfig has already verified the reference is well-formed
	// and names a provider in the pool. Selection runs over the
	// INFERENCE subset only.
	providerName := opts.ProviderName
	explicitProvider := opts.ProviderName != ""
	chatModel := opts.Model
	if cfg.Chat.DefaultModel != "" {
		cp, cm, _ := memops.ParseModelRef(cfg.Chat.DefaultModel)
		if providerName == "" {
			providerName = cp
		}
		if chatModel == "" {
			chatModel = cm
		}
	}
	if providerName == "" {
		providerName = memops.LocalProviderName
	}
	provider, ok := inference[providerName]
	if !ok {
		// An explicitly-requested provider (--provider) that doesn't resolve
		// is a HARD bootstrap error: silently retargeting to another endpoint
		// would send traffic to an unintended provider. Name the unknown value
		// and list the available provider names (names only — never key
		// material or apiKeyFile paths). A named provider that exists but is
		// not an inference endpoint gets its own message: "unknown" would be
		// a lie, and the user would go looking for a typo that isn't there.
		if explicitProvider {
			if p, declared := providers[providerName]; declared {
				return fmt.Errorf("chat: provider %q is a %q provider, not an inference endpoint; "+
					"inference providers: %s", providerName, p.Kind(), strings.Join(sortedProviderNames(inference), ", "))
			}
			return fmt.Errorf("chat: unknown provider %q; available inference providers: %s",
				providerName, strings.Join(sortedProviderNames(inference), ", "))
		}
		// No explicit provider and the conventional default ("local") isn't in
		// the pool: keep the documented default-selection behavior and pick the
		// first INFERENCE provider by name. The empty case is already rejected
		// above, so this always resolves.
		provider, providerName = firstProvider(inference)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("chat: getwd: %w", err)
	}

	// §4.4 shell escape. The shell cwd starts at the process cwd and is
	// tracked SEPARATELY from the active project root for the rest of the
	// session (§4.5.1): `$ cd /etc` moves the shell and nothing else.
	sh := shell.NewRunner(cwd)

	// §6.4/§8.2.1 redaction over `#` captures. The key set is the loaded
	// pool's ALREADY-RESOLVED keys — this reads no key files of its own and
	// never emits what it matched, only a count.
	keys := make([]string, 0, len(providers))
	for _, p := range providers {
		if p.APIKey != "" {
			keys = append(keys, p.APIKey)
		}
	}
	redactor := shell.NewRedactor(keys)

	// The phase-labeled wait indicator. Built before the signal handler is
	// installed so the forced-exit path can always clear it. Inert (zero
	// bytes) unless the session is driving a real terminal.
	pr := newProgress(tm)
	defer pr.stop()

	project, err := bootstrapProject(tm, ops, cwd, opts.ExplicitProject)
	if err != nil {
		return err
	}
	if project.ID == "" {
		// User picked cancel at every prompt; treat as clean exit.
		return nil
	}

	// Build the model client — through a factory, so that a mid-session
	// /model provider switch and session open construct a client the one
	// same way rather than two that can drift. An INJECTED client is
	// returned for every provider — production never injects, and a test
	// that switches providers keeps its mock rather than acquiring a real
	// HTTP client aimed at a scaffolded base URL.
	newClient := func(p memops.Provider) model.Client { return model.NewHTTPClient(p) }
	if opts.Client != nil {
		injected := opts.Client
		newClient = func(memops.Provider) model.Client { return injected }
	}
	client := newClient(provider)

	// Resolve the chat model against the provider's /models endpoint.
	// config.toml's [chat] defaultModel (and --model) name a chat model
	// that is not statically validated — it is checked here, when the
	// provider is actually used. Embedding is validated separately by
	// ValidateConfig; this probe is chat-provider only.
	effectiveModel, err := resolveChatModel(ctx, client, tm.Diag(), providerName, chatModel)
	if err != nil {
		return err
	}

	// session.started, not system.bootstrap: §2.8 defines system.bootstrap
	// as the version/identity line (emitted once at the process boundary by
	// eventlog.LogBootstrap). THIS is the session/project event — which the
	// warning below has always called it — and it pairs with the
	// session.ended line at the bottom of Run.
	if err := ops.Log(ctx, memops.LogCategorySession, "started",
		fmt.Sprintf("active=%s provider=%s", project.ID, providerName)); err != nil {
		fmt.Fprintf(tm.Diag(), "warn: log session.started: %v\n", err)
	}

	// LoadSession (not NewState): reload the persisted Layer B/C working
	// set so a clean shutdown→relaunch resumes the working set instead of
	// cold-starting it empty. A missing artifact (fresh install) yields an
	// empty working set — identical to NewState.
	state, err := turn.LoadSession(ctx, ops, project, provider, client)
	if err != nil {
		return fmt.Errorf("chat: load session: %w", err)
	}
	if effectiveModel != "" {
		state.Model = effectiveModel
	}

	// §6.1 tool surface. Built from config once, immutable thereafter.
	// `web.fetch` always registers; `web.search` registers only when
	// [search] carries a readable key, and its absence is silent — an
	// empty slot in the inventory, not a fault.
	toolReg, err := buildToolRegistry(cfg, providers, tm.Diag())
	if err != nil {
		return err
	}
	state.Tools = toolReg

	// §3.4 layer-2 embedding recall. The embedding provider+model are
	// pinned explicitly via config.toml [embedding] model — never
	// inferred from the chat provider. The embedding model defines the
	// vector space; a model that could drift with the chat provider
	// would silently corrupt the embedding index. Unset → embedding
	// recall stays off (symbolic-only). ValidateConfig has already
	// verified the reference is well-formed, names a provider in the
	// pool, and matches that provider's defaultModel.
	if cfg.Embedding.Model != "" {
		ep, em, _ := memops.ParseModelRef(cfg.Embedding.Model)
		embProvider := providers[ep]
		embedder := model.NewHTTPEmbedder(embProvider, em, cfg.Embedding.VectorLength)
		state.Recaller = measure.NewService(ops, embedder)
	}
	// Index-build failure (e.g. the embedding endpoint unreachable)
	// degrades gracefully to symbolic-only recall — never blocks the
	// session. Prepare on the default symbolic-only Service is a no-op.
	if err := state.Recaller.Prepare(ctx); err != nil {
		fmt.Fprintf(tm.Diag(), "warn: embedding recall unavailable: %v\n", err)
		state.Recaller = measure.NewService(ops, nil)
	}

	// Reasoning is its own channel now (§5's (tty-only, scrollback)
	// cell). The three-way contention for the line — indicator frames,
	// reasoning deltas, committed content — is resolved inside term's
	// serialization point rather than by routing every producer through
	// the thinking object, which is all that object was ever for. What is
	// left here is the /thinking policy.
	th := newThinking(tm, cfg.Chat.ShowThinking)
	state.OnReasoning = th.reasoning

	// §6.1 tool receipts (user ruling 2026-08-05): one committed dimmed
	// line per executed tool call, so tool activity is verified by eyes
	// rather than by asking the model. A second, independent client of the
	// same reasoning channel — deliberately NOT gated by /thinking, which
	// is a policy about the model's scratch, not about what the runtime
	// did. See receipts.go.
	state.OnToolReceipt = toolReceipts(tm)

	// §3.4 recall UI surface: the §2.6.1 ack-mode policy, an interactive
	// resolver for the ASK band, and the one committed line the AUTO band
	// prints (AMENDED 2026-08-05 — the runtime decides the bands, the front
	// end owns the wording, mirroring §3.5's OnAutoClosed).
	state.RecallAckMode = recallAckMode(ctx, ops, project.ID, tm.Diag())
	state.RecallResolver = interactiveRecallResolver(tm, state.RecallAckMode)
	state.OnAutoRecalled = func(n turn.RecallNotice) {
		fmt.Fprintln(tm.Out(), autoRecalledLine(n))
	}

	// §3.5 decay-triggered closure flow: a model-backed curator drafts
	// the closure summary, and an interactive resolver lets the user
	// pick the retire / wip / defer outcome. Both must be installed for
	// the per-turn decay scan to run; the curator reuses the session's
	// chat client and resolved chat model. resolveChatModel guarantees a
	// non-empty model or an error, so there is nothing to fall back to.
	state.Curator = curator.NewHTTPCurator(client, effectiveModel)
	// /model owns the session's endpoint+model answer from here on — the
	// state fields above plus the curator that pins them. Built after
	// them so it has exactly what bootstrap resolved.
	sel := &modelSelector{
		pool:      providers,
		inference: inference,
		provider:  providerName,
		state:     state,
		newClient: newClient,
	}
	state.ClosureResolver = interactiveClosureResolver(tm)
	state.ClosureAckMode = closureAckMode(ctx, ops, project.ID, tm.Diag())
	state.OnAutoClosed = func(n turn.ClosureNotice) {
		fmt.Fprintln(tm.Out(), autoClosedLine(n))
	}

	banner := opts.Banner
	if banner == "" {
		banner = Banner
	}
	fmt.Fprintln(tm.Out(), banner)
	fmt.Fprintf(tm.Out(), "active: %s (%s)\n", project.Name, project.ID)

	// §3.5: a pending exception queue is ANNOUNCED at session start, never
	// prompted. The user opened a session to work, not to clear a backlog;
	// the drain is theirs to start with /closures (or it runs at exit).
	if pending, perr := turn.PendingClosures(ctx, state); perr != nil {
		fmt.Fprintf(tm.Diag(), "warn: pending closures: %v\n", perr)
	} else if n := len(pending); n > 0 {
		fmt.Fprintf(tm.Out(), "%d closure(s) pending review — /closures\n", n)
	}

	// Terminal control (§4 clean shutdown + Esc-to-abort). Ctrl-C ends the
	// session: the first interrupt cancels the session context so an
	// in-flight turn unwinds and the loop exits through the clean-shutdown
	// path below (recaller close → §3.11 session-close checkpoint →
	// session-end log), and a second during shutdown forces an immediate
	// exit.
	//
	// Where a Ctrl-C ARRIVES changed in W3. term clears ISIG while it owns
	// the fd, so at the prompt it is the editor's key (term.ReadLine
	// returns term.ErrAborted after a deliberate second press) and mid-turn
	// it is the turn registration's key. What still reaches the signal
	// handler is a `kill -INT`, a Ctrl-C inside a term.Handoff window, and
	// the in-band key control forwards onto the same queue.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctl := newControl(tm, pr, cancel)
	// Written once here, before the servicing goroutine below starts — the
	// goroutine-creation edge is the happens-before that makes it safe to
	// read from there. Never written again.
	ctl.sh = sh
	// The turn's registration holds term's abort window, so it must be
	// released on every return path or the next prompt inherits it.
	defer ctl.disarm()
	stopInterrupts := ctl.watchInterrupts()
	defer stopInterrupts()

	// Progress reporting: the turn pipeline announces the stage it is in,
	// control routes it — closing the Esc window at the pre-canonical
	// boundary before the indicator re-labels. Both interactive resolvers
	// above write through pr.writer so that taking the terminal to ask the
	// user a question retires any in-flight indicator first.
	state.OnPhase = ctl.onPhase

	if err := loop(runCtx, ops, state, ctl, th, sel, sh, redactor); err != nil {
		return err
	}

	// §3.5 boundary drain: the queued exception closures get their
	// interactive ack here, on the way out, where an interruption costs
	// nothing. Skipped when the session is already cancelled (a Ctrl-C
	// exit) — the user asked to leave, and putting questions to them on
	// the way out is the interruption this flow exists to remove.
	if runCtx.Err() == nil {
		if n, derr := turn.DrainClosures(runCtx, state); derr != nil {
			fmt.Fprintf(tm.Diag(), "warn: closure drain: %v\n", derr)
		} else if n > 0 {
			fmt.Fprintf(tm.Out(), "closed %d topic(s)\n", n)
		}
	}

	// Stop the recaller's indexer goroutine (no-op on the symbolic-only
	// recaller). Done before the session-close checkpoint so no in-flight
	// index write races the final commit.
	if err := state.Recaller.Close(); err != nil {
		fmt.Fprintf(tm.Diag(), "warn: recaller close: %v\n", err)
	}

	// §3.11 session-close commit: the safety net that flushes content-only
	// turns accumulated since the last structural commit. A clean tree (the
	// last turn was structural, or nothing changed) is a benign no-op inside
	// Checkpoint (ErrEmptyCommit swallowed). Non-fatal: warn and continue to
	// the session-end log, consistent with the per-turn cadence call.
	//
	// A marker refusal is NOT a fault: a marker-retained turn failure left
	// torn state that only the next open's Reconcile may touch — a
	// checkpoint here would commit the torn prefix trailer-less and make it
	// permanent (Checkpoint's guard exists for exactly this). Tell the user
	// the state is preserved and skip, rather than warning as if broken.
	if err := ops.Checkpoint(ctx, "session-close"); err != nil {
		if errors.Is(err, memops.ErrConflictingMarker) {
			fmt.Fprintln(tm.Out(), "unclean turn state preserved for recovery — will reconcile on next open")
		} else {
			fmt.Fprintf(tm.Diag(), "warn: session-close checkpoint: %v\n", err)
		}
	}

	if err := ops.Log(ctx, memops.LogCategorySession, "ended", "active="+project.ID); err != nil {
		fmt.Fprintf(tm.Diag(), "warn: log session.end: %v\n", err)
	}
	return nil
}

// printRecoveryBanner renders the startup RecoveryReport as a terse
// informational banner. Routine opens (clean, or a plain derived
// refresh) print nothing; anything that repaired state gets one line
// per fact. The preserved-content line is the load-bearing one: the
// rolled-back turn's bytes are surfaced here and never auto-replayed.
func printRecoveryBanner(w io.Writer, rep memops.RecoveryReport) {
	if rep.Quiet() {
		return
	}
	if rep.ClearedOp != "" {
		detail := rep.ClearedOp
		if rep.ClearedTurn != "" {
			detail += " " + rep.ClearedTurn
		}
		fmt.Fprintf(w, "recovery: unclean shutdown detected (in-flight %s); substrate reconciled\n", detail)
	}
	if rep.ResetPerformed {
		fmt.Fprintf(w, "recovery: rolled back to last recovery point (%d path(s) reverted, %d debris file(s) quarantined)\n",
			len(rep.RevertedPaths), len(rep.DebrisSwept))
	}
	if rep.PreservedContentPath != "" {
		fmt.Fprintf(w, "recovery: in-flight content for turn %s preserved at %s — not replayed; re-supply it if still wanted\n",
			rep.PreservedTurn, rep.PreservedContentPath)
	}
	if n := len(rep.ScopedRestored); n > 0 {
		fmt.Fprintf(w, "recovery: %d broken path(s) quarantined and restored from permanent history: %s\n",
			n, strings.Join(rep.ScopedRestored, ", "))
	}
	if rep.AdoptCommit != "" {
		fmt.Fprintln(w, "recovery: pre-existing uncommitted content adopted forward (verified)")
	}
	if n := len(rep.StampRepaired); n > 0 {
		fmt.Fprintf(w, "recovery: repaired %d archive entr%s\n", n, pluralY(n))
	}
	if n := len(rep.Unrepairable); n > 0 {
		fmt.Fprintf(w, "recovery: %d archive entr%s unrepairable — recovery for them stays refused (see event log)\n", n, pluralY(n))
	}
	if n := len(rep.TmpSwept); n > 0 {
		fmt.Fprintf(w, "recovery: swept %d interrupted-write temp file(s)\n", n)
	}
	if n := len(rep.LogTailsHealed); n > 0 {
		fmt.Fprintf(w, "recovery: healed %d torn event-log tail(s)\n", n)
	}
	if rep.QuarantineDir != "" {
		fmt.Fprintf(w, "recovery: nothing was deleted — %d file(s) preserved byte-exact under %s\n",
			len(rep.Quarantined), rep.QuarantineDir)
	}
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// sortedProviderNames returns the pool's provider names in deterministic
// order. Names only — never key material or apiKeyFile paths — so the
// result is safe to surface in an error message.
func sortedProviderNames(providers map[string]memops.Provider) []string {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func firstProvider(providers map[string]memops.Provider) (memops.Provider, string) {
	names := sortedProviderNames(providers)
	if len(names) == 0 {
		return memops.Provider{}, ""
	}
	return providers[names[0]], names[0]
}

// modelResolveTimeout caps the single /models probe at chat startup.
const modelResolveTimeout = 30 * time.Second

// resolveChatModel verifies the configured chat model against the
// provider's /models endpoint and returns the model the session uses.
//
// There is NO provider-side fallback: a pool entry names no models (see
// memops.Provider's doc — `defaultModel` was removed because a pinned id
// goes stale as providers rotate their catalogues, and a stale fallback
// fires exactly when the configured model is unavailable, which is when
// it is least likely to still be right). The model comes from --model or
// config.toml [chat] defaultModel, and nowhere else.
//
// Graceful degradation: if /models cannot be reached or reports no
// models, the check is skipped — a warning is printed and the configured
// model is returned unverified, so an offline provider does not block
// the session.
//
// When /models returns a non-empty list, a configured model that is not
// on it is a hard error naming what the provider does offer: silently
// proceeding would produce a 404 at the first turn instead of a fixable
// message at startup.
func resolveChatModel(ctx context.Context, client model.Client, stderr io.Writer, providerName, configModel string) (string, error) {
	if configModel == "" {
		return "", fmt.Errorf("chat: no model configured for provider %q — set [chat] defaultModel "+
			"in config.toml as \"%s/<model>\", or pass --model", providerName, providerName)
	}

	probeCtx, cancel := context.WithTimeout(ctx, modelResolveTimeout)
	defer cancel()
	models, err := client.ListModels(probeCtx)
	if err != nil {
		fmt.Fprintf(stderr, "warn: could not verify model against provider %q /models: %v\n", providerName, err)
		return configModel, nil
	}
	if len(models) == 0 {
		fmt.Fprintf(stderr, "warn: could not verify model against provider %q /models: provider reported no models\n", providerName)
		return configModel, nil
	}

	offered := make([]string, 0, len(models))
	for _, m := range models {
		if m.ID == configModel {
			return configModel, nil
		}
		offered = append(offered, m.ID)
	}
	sort.Strings(offered)
	return "", fmt.Errorf("chat: provider %q does not offer model %q; it offers: %s",
		providerName, configModel, strings.Join(offered, ", "))
}

// promptLine reads the next REPL line. An Esc-retracted input is
// re-offered as an EDITABLE DEFAULT — cursor at end — so the user can fix
// and resubmit it, or clear it and move on. Re-submission is the ONLY way
// a retracted prompt re-enters the pipeline (§4.3.3): the abort rolled
// the session back, so nothing of it is in memory until the user
// deliberately sends it again.
//
// An empty retracted value is an absent Default, not an empty one: term
// reads that as "no value to pre-fill" and prompts normally.
//
// This is the one [term.ActivityEditor] read in the program — the read AT
// the prompt, the outermost input consumer of the session. Every other
// read in this package is an ask nested inside turn close.
func promptLine(ctx context.Context, tm *term.Terminal, retracted string) (string, error) {
	answer, err := tm.ReadLine(ctx, term.ActivityEditor, term.Question{
		Prompt:  replPrompt,
		Default: retracted,
	})
	return answer.Text, err
}

// loop is the core REPL. Returns nil on a clean exit; non-nil on an
// unrecoverable I/O error (e.g. the input reader failing, not a
// per-turn LLM error which is logged and tolerated).
func loop(ctx context.Context, ops memops.MemoryOps, state *turn.State, ctl *control, th *thinking, sel *modelSelector, sh *shell.Runner, red *shell.Redactor) error {
	tm := ctl.t
	// retracted carries an Esc-aborted input to the next prompt, where it
	// is re-offered as an editable default. It is deliberately a local:
	// nothing outside this loop may resurrect a retracted prompt.
	retracted := ""
	// pending holds §4.4 `#` captures taken since the last turn, in order,
	// to be delivered as that turn's pre-prompt deltas. It is deliberately
	// NOT durable: a capture is a convenience for the next thing the user
	// types, and a crash (or a session that ends before the next prompt)
	// simply loses it. Inventing durability for it would mean journaling
	// arbitrary command output on a path that has no recovery contract.
	var pending []turn.Delta
	defer func() {
		if len(pending) == 0 {
			return
		}
		// context.WithoutCancel: the common way to reach here with captures
		// still buffered is a Ctrl-C exit, which has already cancelled ctx.
		_ = ops.Log(context.WithoutCancel(ctx), memops.LogCategoryUser, "shell-capture-dropped",
			fmt.Sprintf("count=%d", len(pending)))
	}()
	for {
		if ctx.Err() != nil {
			// A SIGINT during a turn cancelled the session — unwind cleanly.
			return nil
		}
		line, err := promptLine(ctx, tm, retracted)
		retracted = ""
		switch {
		case errors.Is(err, term.ErrAborted):
			if ctx.Err() != nil {
				// The read was cancelled BY the shutdown, not by the user:
				// term's pump now honours ctx mid-read, so a session cancel
				// arriving while the prompt is up unblocks it here. Counting
				// it as a fresh interrupt would make one `kill -INT` the
				// second press and force-quit past the clean path.
				return nil
			}
			// Ctrl-C at the prompt — the user asked to leave, so this is
			// /exit with a different key (after the deliberate second press
			// term's editor requires): begin the same clean shutdown and say
			// nothing. It still counts as the first interrupt, so a further
			// Ctrl-C while the shutdown runs force-quits.
			ctl.exit(false)
			return nil
		case errors.Is(err, io.EOF):
			if line == "" {
				fmt.Fprintln(tm.Out()) // newline after the dangling prompt
				return nil
			}
			// fall through: process the final line, then EOF on next read.
		case err != nil:
			return fmt.Errorf("chat: read input: %w", err)
		}
		input := line
		trimmed := strings.TrimSpace(input)
		if trimmed == "" {
			continue
		}
		tm.AppendHistory(input)

		switch {
		case strings.HasPrefix(trimmed, "/"):
			done, derr := dispatchSlash(ctx, tm, ops, state, th, sel, trimmed)
			if derr != nil {
				fmt.Fprintf(tm.Diag(), "command error: %v\n", derr)
				continue
			}
			if done {
				return nil
			}
		case strings.HasPrefix(trimmed, "$") || strings.HasPrefix(trimmed, "#"):
			delta, serr := runShellEscape(ctx, ops, ctl, sh, red, trimmed)
			if serr != nil {
				fmt.Fprintf(tm.Diag(), "shell error: %v\n", serr)
				continue
			}
			if delta != nil {
				pending = appendCapture(pending, *delta)
			}
		default:
			// The buffered captures are consumed by this turn. On an Esc
			// retraction they come BACK: the turn was rolled back whole
			// (§4.3.3), and the captures were taken before it, so dropping
			// them would silently detach output the user is about to
			// resubmit a prompt about.
			consumed := pending
			pending = nil
			back, err := runOneTurn(ctx, ops, ctl, state, consumed, input)
			retracted = back
			if back != "" {
				pending = consumed
			}
			if err != nil {
				// A bad turn does not kill the session — log the error and
				// loop. The user can usually retry; the one exception is a
				// marker-retained failure (a CommitTurn failure this turn, or
				// the JournalTurn refusal every later turn hits on the retained
				// scope): in-session retry CANNOT work, and the prompt just
				// typed was refused before it was journaled — it is not
				// captured anywhere. Say both, instead of implying retry.
				fmt.Fprintf(tm.Diag(), "turn error: %v\n", err)
				if errors.Is(err, turn.ErrMarkerRetained) || errors.Is(err, memops.ErrConflictingMarker) {
					fmt.Fprintln(tm.Diag(), "unrecoverable in this session: the failed turn's recovery scope is still open — restart personant to recover; prompts typed until then are NOT captured, so save this one if you still need it")
				}
				continue
			}
		}
	}
}

// abortNotice marks an Esc-aborted turn on screen. If the abort landed
// mid-stream, partial response tokens are already on the user's terminal
// and cannot be reliably erased — they may have scrolled, and they may
// span many rows. Left unmarked, the transcript would assert something
// false: a response the user can read that is not, and never was, in
// memory. The brackets make the line unmistakably not model output.
const abortNotice = "[cancelled — input retracted; nothing from this turn was saved]"

// runOneTurn drives one turn. It returns retracted != "" when the user
// pressed Esc: the input the REPL should re-offer as an editable default.
// The turn's own error is separate — an abort is not a failure.
//
// pre carries the §4.4 `#` captures taken since the last prompt. They fire
// through the §3.0 chain BEFORE the user.prompt delta and share its turn
// number, so the prompt can cite symbols the capture staged.
func runOneTurn(ctx context.Context, ops memops.MemoryOps, ctl *control, state *turn.State, pre []turn.Delta, input string) (retracted string, err error) {
	turnCtx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	pr, tm := ctl.pr, ctl.t
	// Open the Esc window on the turn's own context — Esc abandons the
	// turn, never the session. Closed again at the pre-canonical phase
	// boundary (control.onPhase); the defer is the backstop for a turn that
	// never reaches it.
	ctl.arm(cancel)
	defer ctl.disarm()
	// The body streams through term's content channel: its first byte
	// closes any open reasoning run and retires the pre-token indicator,
	// and the turn's closing phase restarts the indicator for the
	// post-stream wait. Both are term's doing, at one serialization
	// point, so there is nothing here to keep in sync.
	_, info, err := turn.RunWithInfo(turnCtx, state, pre, input, tm.Out())
	// Retire the closing-phase indicator on EVERY exit — normal, per-turn
	// error, or a cancelled turn — before anything else touches the
	// terminal. Releasing the slot also closes a still-open reasoning
	// run, which is the turn that produced reasoning and no body (the D6
	// empty-response case) and the one shape that would otherwise leave
	// the terminal dim.
	pr.stop()
	// Re-align BEFORE the branches, for the same reason pr.stop() runs
	// before them: a turn that failed or was aborted mid-stream has partial
	// body text on screen exactly as a successful one does, and whatever
	// comes next — the loop's `turn error:` Diag line, the abort notice,
	// the next prompt — must not land on that body's tail.
	realign(tm)
	// An Esc abort is an ORDINARY event, not a turn failure: the abort
	// landed in the pre-canonical window, so RunWithInfo's deferred handler
	// already rolled the session back and released the #94 recovery scope,
	// and the next launch opens quiet. Keyed off the flag rather than the
	// error because a Ctrl-C session cancel returns the same
	// context.Canceled and must not be reported as "back to the prompt".
	if err != nil && ctl.tookAbort() {
		if lerr := ctl.reportRetraction(ctx, ops, info.TurnID, input); lerr != nil {
			fmt.Fprintf(tm.Diag(), "warn: log system.turn-aborted: %v\n", lerr)
		}
		return input, nil
	}
	if err != nil {
		return "", err
	}
	return "", nil
}

// realign puts the cursor at the start of a line, so that whatever is
// written next begins on its own row. term knows the cursor column — every
// byte that reached the terminal went through it — which a streamed body,
// already tag-stripped and newline-trimmed, does not. The column is a
// tri-state and ColumnUnknown (a child wrote whatever it liked) takes the
// newline too: the deterministic answer rather than a guess.
//
// This is policy's job, on every path where a partial body may be on
// screen. term deliberately does NOT force a newline of its own for a Diag
// write: in a pipe stdout and stderr are separate streams, and term must
// not inject bytes into stdout on stderr's behalf.
func realign(tm *term.Terminal) {
	if tm.State().Column != term.ColumnStart {
		fmt.Fprintln(tm.Out())
	}
}

// dispatchSlash returns done=true to signal the loop should exit. A
// returned error is per-command and non-fatal — the loop prints it and
// continues. Stubs and unknown commands write to stderr and return nil.
func dispatchSlash(ctx context.Context, tm *term.Terminal, ops memops.MemoryOps, state *turn.State, th *thinking, sel *modelSelector, line string) (bool, error) {
	out, diag := tm.Out(), tm.Diag()
	cmd, rest := splitCommand(line)
	switch cmd {
	case "/quit", "/exit":
		return true, nil
	case "/help":
		fmt.Fprintln(out, helpText())
	case "/stats":
		printStats(out, ops, state)
	case "/version":
		printVersion(ctx, out, ops)
	case "/topic":
		return false, cmdTopic(ctx, out, state, rest)
	case "/topics":
		return false, cmdTopics(ctx, out, ops, state, rest)
	case "/done":
		return false, cmdDone(ctx, state, rest)
	case "/closures":
		return false, cmdClosures(ctx, out, state)
	case "/pause":
		return false, cmdPause(ctx, out, state, rest)
	case "/resume":
		return false, cmdResume(ctx, out, state, rest)
	case "/back-to":
		return false, cmdBackTo(ctx, out, state, rest)
	case "/project":
		return false, cmdProject(ctx, tm, ops, state, rest)
	case "/thinking":
		return false, cmdThinking(out, th, rest)
	case "/model":
		return false, cmdModel(ctx, tm, ops, sel, rest)
	case "/terminal-setup":
		return false, cmdTerminalSetup(ctx, tm)
	case "/no-revisit":
		fmt.Fprintln(diag, "/no-revisit is not yet implemented (recall accrual loop, §3.4)")
	case "/cd-project":
		fmt.Fprintf(diag, "%s is not yet implemented (later phase)\n", cmd)
	default:
		fmt.Fprintf(diag, "unknown command: %s; type /help for available commands\n", cmd)
	}
	return false, nil
}

// cmdTopic dispatches the /topic family: `rename <new-name>` (§4.2), and
// otherwise `<name>` — force a new thread engaged for the next turn.
//
// PARSE CONVENTION, matched from /project (cmdProject): the first word of
// the argument is read as a subcommand. /project can do that without
// ambiguity because its bare form takes no operand; /topic's operand is
// free text, so the split has one accepted corner — `/topic rename widget
// sizing` renames the active topic and there is no way to CREATE a topic
// literally named "rename widget sizing". That is the trade the ruling
// accepts (2026-08-06): the collision is vanishingly rare, and matching
// /project's shape is worth more than reserving it. The topic is
// renameable afterwards regardless, which is the escape hatch.
func cmdTopic(ctx context.Context, out io.Writer, state *turn.State, rest string) error {
	if sub, arg := splitCommand(rest); sub == "rename" {
		return cmdTopicRename(ctx, out, state, arg)
	}
	name := strings.TrimSpace(rest)
	if name == "" {
		return errors.New("usage: /topic <name> | /topic rename <new-name>")
	}
	id, err := turn.CreateTopic(ctx, state, name)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "started %s\n", topicLabel(name, id, ""))
	return nil
}

// cmdTopicRename implements /topic rename <new-name> (§4.2): change the
// active topic's display name. The sibling of /project rename one level
// down, and cosmetic in the same way — the id, the anchors, the symbol
// history and everything recall matches on are unchanged (turn.RenameTopic).
//
// Duplicate names are permitted, because topic CREATION permits them: two
// topics may share a display name, and the existing ambiguity error from
// the /back-to name resolver is what a user who then types the shared name
// gets. Rename does not impose a uniqueness rule creation does not have.
func cmdTopicRename(ctx context.Context, out io.Writer, state *turn.State, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("usage: /topic rename <new-name>")
	}
	id, old, err := turn.RenameTopic(ctx, state, name)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "renamed %s: %q → %q\n", id, old, name)
	return nil
}

// cmdDone implements /done [thr_id|name] — manual §3.5 closure on the
// current owner thread (or the named one). SPEC §4.2 defines only the
// no-arg form (close the active thread); the optional reference is a
// dogfooding convenience for closing a specific thread.
func cmdDone(ctx context.Context, state *turn.State, rest string) error {
	return turn.ManualClosure(ctx, state, rest)
}

// cmdClosures implements /closures — drain the §3.5 pending-review queue
// on demand (§4.2). The same drain runs at clean session exit; this is
// the user choosing the moment instead.
func cmdClosures(ctx context.Context, out io.Writer, state *turn.State) error {
	n, err := turn.DrainClosures(ctx, state)
	if err != nil {
		return err
	}
	if n == 0 {
		fmt.Fprintln(out, "no closures pending review")
		return nil
	}
	fmt.Fprintf(out, "closed %d topic(s)\n", n)
	return nil
}

// closureAckMode resolves the §2.6.1 closure.ack-mode directive into the
// session's policy. Absent → the auto default. An unreadable directive
// file or an unrecognized value warns and keeps the default: an ack
// policy is not worth refusing a session over, and the default is the
// non-interrupting one either way.
func closureAckMode(ctx context.Context, ops memops.MemoryOps, projectID string, diag io.Writer) turn.ClosureAckMode {
	raw, ok, err := ops.DirectiveParam(ctx, projectID, turn.DirectiveClosureAckMode)
	if err != nil {
		fmt.Fprintf(diag, "warn: read %s: %v\n", turn.DirectiveClosureAckMode, err)
		return turn.AckModeAuto
	}
	if !ok {
		return turn.AckModeAuto
	}
	mode, valid := turn.ParseClosureAckMode(raw)
	if !valid {
		fmt.Fprintf(diag, "warn: %s=%q is not auto|always; using auto\n",
			turn.DirectiveClosureAckMode, raw)
		return turn.AckModeAuto
	}
	return mode
}

// recallAckMode resolves the §2.6.1 recall.ack-mode directive into the
// session's policy. Same contract as closureAckMode: absent, unreadable
// or unrecognized keeps the banded default rather than refusing a session
// over an ack preference.
func recallAckMode(ctx context.Context, ops memops.MemoryOps, projectID string, diag io.Writer) turn.RecallAckMode {
	raw, ok, err := ops.DirectiveParam(ctx, projectID, turn.DirectiveRecallAckMode)
	if err != nil {
		fmt.Fprintf(diag, "warn: read %s: %v\n", turn.DirectiveRecallAckMode, err)
		return turn.RecallAckBanded
	}
	if !ok {
		return turn.RecallAckBanded
	}
	mode, valid := turn.ParseRecallAckMode(raw)
	if !valid {
		fmt.Fprintf(diag, "warn: %s=%q is not banded|always; using banded\n",
			turn.DirectiveRecallAckMode, raw)
		return turn.RecallAckBanded
	}
	return mode
}

// cmdPause implements /pause [thr_id|name] (§2.2.1).
func cmdPause(ctx context.Context, out io.Writer, state *turn.State, rest string) error {
	id, err := turn.PauseThread(ctx, state, rest)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "paused %s\n", id)
	return nil
}

// cmdResume implements /resume [thr_id|name] (§2.2.1).
func cmdResume(ctx context.Context, out io.Writer, state *turn.State, rest string) error {
	id, err := turn.ResumeThread(ctx, state, rest)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "resumed %s\n", id)
	return nil
}

// cmdBackTo implements /back-to <thr_id|name> — explicit re-engagement
// (§4.2), promoting the thread into Layer B.
func cmdBackTo(ctx context.Context, out io.Writer, state *turn.State, rest string) error {
	if strings.TrimSpace(rest) == "" {
		return errors.New("usage: /back-to <thr_id|name>")
	}
	id, err := turn.BackToThread(ctx, state, rest)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "re-engaged %s\n", id)
	return nil
}

// cmdThinking implements /thinking [on|off] — the session-scoped
// override of the config.toml [chat] showThinking default. The bare form
// reports the current state. The override is session-scoped by design: it
// is never written back to config.toml, so the file stays the user's
// hand-edited default and the command stays a display toggle.
//
// The setting is reported honestly on a non-terminal session: it is
// accepted and remembered, and the reply says plainly that nothing will
// be shown, rather than silently pretending it took effect.
func cmdThinking(out io.Writer, th *thinking, rest string) error {
	if strings.TrimSpace(rest) != "" {
		on, ok := parseThinkingArg(rest)
		if !ok {
			return fmt.Errorf("usage: /thinking [on|off]")
		}
		th.setOn(on)
	}
	suffix := ""
	if th.on && !th.interactive {
		suffix = " (not a terminal — nothing will be shown)"
	}
	fmt.Fprintf(out, "thinking display: %s%s\n", thinkingStateLabel(th.on), suffix)
	return nil
}

// cmdProject dispatches the /project family: bare (print info), rename, and
// switch (§4.2 / §4.5.6).
func cmdProject(ctx context.Context, tm *term.Terminal, ops memops.MemoryOps, state *turn.State, rest string) error {
	if rest == "" {
		printProjectInfo(tm.Out(), state.ActiveProject)
		return nil
	}
	sub, arg := splitCommand(rest)
	switch sub {
	case "rename":
		return cmdProjectRename(ctx, tm.Out(), ops, state, arg)
	case "switch":
		return cmdProjectSwitch(ctx, tm.Out(), ops, state, arg)
	default:
		return fmt.Errorf("unknown /project subcommand %q; use rename or switch", sub)
	}
}

// cmdProjectRename implements /project rename <new-name> (§4.5.6): update
// the display name only — id, path, and remote URL are unchanged, and no
// spine entries are rewritten.
func cmdProjectRename(ctx context.Context, out io.Writer, ops memops.MemoryOps, state *turn.State, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("usage: /project rename <new-name>")
	}
	meta, err := ops.LoadProject(ctx, state.ActiveProject.ID)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	old := meta.Name
	meta.Name = name
	if err := ops.SaveProject(ctx, meta); err != nil {
		return fmt.Errorf("save project: %w", err)
	}
	state.ActiveProject.Name = name
	if err := ops.Log(ctx, memops.LogCategoryProject, "renamed",
		"id="+meta.ID+" old="+old+" new="+name); err != nil {
		return err
	}
	fmt.Fprintf(out, "renamed project %s: %q → %q\n", meta.ID, old, name)
	return nil
}

// cmdProjectSwitch implements /project switch <name-or-id> (§4.2 / §4.5.6):
// change the active project to a known one. The Layer B/C working set is
// membership for the OUTGOING project (closure/recall are project-scoped),
// so turn.SwitchProject resets it — v0.1 keeps no per-project working-set
// snapshot, so the switched-to project cold-starts.
func cmdProjectSwitch(ctx context.Context, out io.Writer, ops memops.MemoryOps, state *turn.State, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("usage: /project switch <name-or-id>")
	}
	metas, err := ops.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}
	var target *memops.ProjectMeta
	for i := range metas {
		if metas[i].ID == ref || strings.EqualFold(metas[i].Name, ref) {
			target = &metas[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("no known project matching %q", ref)
	}
	if target.ID == state.ActiveProject.ID {
		fmt.Fprintf(out, "already on project %s (%s)\n", target.Name, target.ID)
		return nil
	}
	from := state.ActiveProject.ID
	if err := ops.SetLastActiveProject(ctx, target.ID); err != nil {
		return fmt.Errorf("set last-active: %w", err)
	}
	if err := turn.SwitchProject(ctx, state, *target); err != nil {
		return err
	}
	if err := ops.Log(ctx, memops.LogCategoryProject, "switched",
		"from="+from+" to="+target.ID); err != nil {
		return err
	}
	fmt.Fprintf(out, "switched to project %s (%s)\n", target.Name, target.ID)
	return nil
}

func splitCommand(line string) (cmd, rest string) {
	line = strings.TrimSpace(line)
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		return line[:i], strings.TrimSpace(line[i+1:])
	}
	return line, ""
}

// helpText is the /help body. It opens with the two-line hierarchy
// orientation (user ruling 2026-08-05): the command list names both
// containers, and a user who has not been told how they nest reads
// "project" and "topic" as synonyms. The second line is the ONE place the
// user surface says "thread" — it is the bridge to the storage vocabulary
// the ids, the spec and the event log use, and saying it once here is what
// lets every other string say "topic".
func helpText() string {
	return `project  — top-level container for an endeavor (one active at a time)
topic    — a conversation strand within the project (stored as thread thr_N)

available commands:
  /help                        show this help
  /quit, /exit                 exit the session
  /topic <name>                start a new topic and engage it
  /topic rename <new-name>     rename the active topic (display name only)
  /topics [all]                list this project's topics (all = include old closed)
  /done [thr_id|name]          close the active (or named) topic (§3.5)
  /closures                    review the closures queued for your ack (§3.5)
  /pause [thr_id|name]         pause the active (or named) topic
  /resume [thr_id|name]        resume a paused topic
  /back-to <thr_id|name>       re-engage a topic into the working set — see /topics
  /project                     print active project info
  /project rename <new-name>   rename the active project
  /project switch <name-or-id> switch to a known project
  /thinking [on|off]           show/hide the model's live reasoning (dimmed)
  /model [id|provider/id]      switch the session's model (bare form reports it)
  /stats                       print session and spine statistics
  /version                     print version identity and home format
  /terminal-setup              set this terminal up for Shift+Enter (§4.3.1)

stubbed (later phase):
  /no-revisit                  tighten recall threshold (recall accrual)
  /cd-project <path>           set active project root

shell escape (§4.4 — your shell, your privileges; interactive apps
such as vim/less/top are not supported):
  $<cmd>                       run it; output goes to your terminal only
  #<cmd>                       run it; output also joins the next turn's context
                               (cd persists between commands, separately
                                from the active project root)`
}

func printProjectInfo(w io.Writer, p memops.ProjectMeta) {
	fmt.Fprintf(w, "id:           %s\n", p.ID)
	fmt.Fprintf(w, "name:         %s\n", p.Name)
	if p.CurrentRootPath != "" {
		fmt.Fprintf(w, "root path:    %s\n", p.CurrentRootPath)
	}
	if len(p.RemoteURLs) > 0 {
		fmt.Fprintf(w, "remote urls:  %s\n", strings.Join(p.RemoteURLs, ", "))
	}
}

// printVersion implements /version: this binary's identity plus the
// on-disk format of the home the session is running against. The CLI's
// `personant version` prints the same two things; both render through
// internal/version so they cannot drift.
//
// Never fails — an unreadable stamp renders as text. The session is
// already open by the time this runs, so there is nothing to gate.
func printVersion(ctx context.Context, w io.Writer, ops memops.MemoryOps) {
	fmt.Fprint(w, version.Long())
	found, format, err := ops.HomeFormat(ctx)
	fmt.Fprint(w, version.Row("home stamp", version.HomeFormatLabel(found, format, err)))
}

func printStats(w io.Writer, ops memops.MemoryOps, state *turn.State) {
	records, err := ops.ListThreads(context.Background(), memops.ThreadFilter{Project: state.ActiveProject.ID})
	if err != nil {
		fmt.Fprintf(w, "stats unavailable: %v\n", err)
		return
	}
	turnsThisSession := len(state.History) / 2 // history alternates user/assistant
	fmt.Fprintf(w, "active project:        %s (%s)\n", state.ActiveProject.Name, state.ActiveProject.ID)
	fmt.Fprintf(w, "spine records (this):  %d\n", len(records))
	fmt.Fprintf(w, "turns this session:    %d\n", turnsThisSession)
	// Show the §2.2.2 display lines so the user sees what the model sees.
	for _, r := range records {
		fmt.Fprintln(w, "  "+workset.RenderSpineDisplay(r))
	}
}

// bootstrapProject runs §4.5.7. The five BootstrapResult.Step branches
// are handled here — the resolver itself lives behind
// ops.ResolveActiveProject; surface UI prompts live in this package.
//
// Returns a zero ProjectMeta only if the user cancels at every prompt.
// The caller treats that as a clean exit.
func bootstrapProject(tm *term.Terminal, ops memops.MemoryOps, cwd, explicit string) (memops.ProjectMeta, error) {
	return bootstrapProjectWithExplicit(tm, ops, cwd, explicit)
}

func bootstrapProjectWithExplicit(tm *term.Terminal, ops memops.MemoryOps, cwd string, explicit string) (memops.ProjectMeta, error) {
	ctx := context.Background()
	result, err := ops.ResolveActiveProject(ctx, memops.BootstrapHints{
		ExplicitProject: explicit,
		CWD:             cwd,
	})
	if err != nil {
		return memops.ProjectMeta{}, fmt.Errorf("chat: bootstrap: %w", err)
	}

	switch result.Step {
	case memops.StepExplicit, memops.StepRemoteMatch, memops.StepPathMatch:
		if result.Resolved == nil {
			return memops.ProjectMeta{}, errors.New("chat: bootstrap: resolver returned nil project on success step")
		}
		return *result.Resolved, nil

	case memops.StepNeedsConfirmation:
		return promptConfirmation(tm, ops, cwd, result.Candidate)

	case memops.StepNeedsFallback:
		return promptFallback(tm, ops, cwd)

	default:
		return memops.ProjectMeta{}, fmt.Errorf("chat: bootstrap: unrecognized step %v", result.Step)
	}
}

// promptConfirmation surfaces the §4.5.7 last-active resume prompt.
//
// No Keys: this menu also accepts <other-name-or-id>, and a project named
// "nomad" starts with a letter the menu would otherwise claim. A fast path
// here would answer "no" to a user who is typing a name — which is why
// [term.Question.Keys] is set only where every unlisted answer is already
// invalid.
func promptConfirmation(tm *term.Terminal, ops memops.MemoryOps, cwd string, candidate *memops.ProjectMeta) (memops.ProjectMeta, error) {
	if candidate == nil {
		return promptFallback(tm, ops, cwd)
	}
	ctx := context.Background()
	q := term.Question{Prompt: fmt.Sprintf(
		"Resume work on '%s'? [y]es / [n]o / <other-name-or-id>: ", candidate.Name)}
	if candidate.LastActive != "" {
		q.Prompt = fmt.Sprintf("Resume work on '%s' (last active %s)? [y]es / [n]o / <other-name-or-id>: ",
			candidate.Name, candidate.LastActive)
	}
	answer, err := tm.ReadLine(ctx, term.ActivityAsk, q)
	if err != nil {
		return memops.ProjectMeta{}, err
	}
	ans := strings.TrimSpace(answer.Text)
	switch ans {
	case "", "y", "Y", "yes":
		if err := ops.SetLastActiveProject(ctx, candidate.ID); err != nil {
			return memops.ProjectMeta{}, fmt.Errorf("chat: write last-active: %w", err)
		}
		return *candidate, nil
	case "n", "N", "no":
		return promptFallback(tm, ops, cwd)
	default:
		// Treat as <other-name-or-id> — re-resolve with explicit override.
		return bootstrapProjectWithExplicit(tm, ops, cwd, ans)
	}
}

// promptFallback surfaces the §4.5.7 final fallback prompt.
//
// Keys is safe here (unlike the resume prompt above) because every answer
// that is not one of the three letters is already invalid — and from W3 it
// means what it says: on a terminal term owns, the first keystroke in the
// set answers the menu WITHOUT Enter. Off one it degrades to the first
// rune of the submitted line, which is what these menus have always done.
func promptFallback(tm *term.Terminal, ops memops.MemoryOps, cwd string) (memops.ProjectMeta, error) {
	ctx := context.Background()
	for {
		answer, err := tm.ReadLine(ctx, term.ActivityAsk, term.Question{
			Preamble: []string{
				"No active project resolved.",
				"  [c] create a new project rooted at this directory",
				"  [s] switch to a known project",
				"  [n] no project (use prj_default)",
			},
			Prompt: "choice: ",
			Keys:   "csn",
		})
		if err != nil {
			return memops.ProjectMeta{}, err
		}
		switch strings.TrimSpace(strings.ToLower(answer.Text)) {
		case "c":
			meta, err := createNewProject(tm, ops, cwd)
			if err != nil {
				fmt.Fprintf(tm.Diag(), "create failed: %v\n", err)
				continue
			}
			return meta, nil
		case "s":
			meta, ok, err := pickExistingProject(tm, ops)
			if err != nil {
				return memops.ProjectMeta{}, err
			}
			if !ok {
				continue
			}
			if err := ops.SetLastActiveProject(ctx, meta.ID); err != nil {
				return memops.ProjectMeta{}, fmt.Errorf("chat: write last-active: %w", err)
			}
			return meta, nil
		case "n":
			meta, err := ops.LoadProject(ctx, memops.DefaultProjectID)
			if err != nil {
				return memops.ProjectMeta{}, fmt.Errorf("chat: load default: %w", err)
			}
			if err := ops.SetLastActiveProject(ctx, memops.DefaultProjectID); err != nil {
				return memops.ProjectMeta{}, fmt.Errorf("chat: write last-active: %w", err)
			}
			return meta, nil
		default:
			fmt.Fprintln(tm.Diag(), "invalid choice; enter c, s, or n")
		}
	}
}

func createNewProject(tm *term.Terminal, ops memops.MemoryOps, cwd string) (memops.ProjectMeta, error) {
	ctx := context.Background()
	answer, err := tm.ReadLine(ctx, term.ActivityAsk, term.Question{Prompt: "display name: "})
	if err != nil {
		return memops.ProjectMeta{}, err
	}
	name := strings.TrimSpace(answer.Text)
	if name == "" {
		return memops.ProjectMeta{}, errors.New("chat: empty project name")
	}
	id, err := ops.NextProjectID(ctx)
	if err != nil {
		return memops.ProjectMeta{}, err
	}
	now := clock.Timeline().UTC().Format(time.RFC3339)
	meta := memops.ProjectMeta{
		ID:              id,
		Name:            name,
		CurrentRootPath: cwd,
		Created:         now,
		LastActive:      now,
	}
	if err := ops.CreateProject(ctx, meta); err != nil {
		return memops.ProjectMeta{}, err
	}
	if err := ops.SetLastActiveProject(ctx, id); err != nil {
		return memops.ProjectMeta{}, fmt.Errorf("chat: write last-active: %w", err)
	}
	if err := ops.Log(ctx, memops.LogCategoryProject, "created", "id="+id+" name="+name); err != nil {
		fmt.Fprintf(tm.Diag(), "warn: log project.created: %v\n", err)
	}
	// §3.11: a project creation is a structural change. This runs during
	// bootstrap (a NEW project chosen at session start), distinct from
	// `personant init` which commits the default project at scaffold time —
	// so it needs its own recovery point rather than riding init's commit.
	// Non-fatal: the session-close commit is the backstop. (last-active is a
	// substrate marker kept out of git, so it rides this commit only if the
	// adapter tracks it; either way the project meta is captured.)
	if err := ops.Checkpoint(ctx, "project-create "+id); err != nil {
		fmt.Fprintf(tm.Diag(), "warn: project-create checkpoint: %v\n", err)
	}
	return meta, nil
}

func pickExistingProject(tm *term.Terminal, ops memops.MemoryOps) (memops.ProjectMeta, bool, error) {
	ctx := context.Background()
	metas, err := ops.ListProjects(ctx)
	if err != nil {
		return memops.ProjectMeta{}, false, err
	}
	if len(metas) == 0 {
		fmt.Fprintln(tm.Diag(), "no known projects to switch to")
		return memops.ProjectMeta{}, false, nil
	}
	preamble := make([]string, 0, len(metas)+1)
	preamble = append(preamble, "known projects:")
	for _, m := range metas {
		preamble = append(preamble, fmt.Sprintf("  %s  %s", m.ID, m.Name))
	}
	answer, err := tm.ReadLine(ctx, term.ActivityAsk, term.Question{
		Preamble: preamble,
		Prompt:   "id or name: ",
	})
	if err != nil {
		return memops.ProjectMeta{}, false, err
	}
	ans := strings.TrimSpace(answer.Text)
	for _, m := range metas {
		if m.ID == ans || m.Name == ans {
			return m, true, nil
		}
	}
	fmt.Fprintf(tm.Diag(), "no project matched %q\n", ans)
	return memops.ProjectMeta{}, false, nil
}

// recallOfferLines renders one offered candidate as the two lines a user
// can act on without a lookup (user ruling 2026-08-05 — the superseded
// form was `[1] thr_3 score=0.56 (intra-thread)`, which named nothing the
// user could recognize and asked nothing they could weigh):
//
//	[1] <display name>
//	    <gist> · <why> · <tier> <score>
//
// "why" is the evidence the match actually rests on: the matched symbols
// for a symbolic hit, the matched turn numbers for an intra-thread hit
// (there are no symbols there — the evidence is WHERE in this thread),
// and for an embedding-only hit the honest answer, which is that the
// similarity IS the evidence.
func recallOfferLines(n int, c turn.RecallCandidate) []string {
	why := "similar wording"
	switch {
	case c.Symbolic != nil && len(c.Symbolic.MatchedSymbols) > 0:
		why = "matched " + strings.Join(c.Symbolic.MatchedSymbols, ", ")
	case c.IntraThread != nil && len(c.IntraThread.Turns) > 0:
		why = "earlier here, turn " + joinTurns(c.IntraThread.Turns)
	}
	tier := "related"
	if c.IntraThread != nil {
		tier = "earlier in this topic"
	} else if c.Embedding == nil && c.Symbolic != nil {
		tier = "shared topics"
	}
	return []string{
		fmt.Sprintf("  [%d] %s", n, c.Display),
		fmt.Sprintf("      %s · %s · %s %.2f", c.Gist, why, tier, c.Score),
	}
}

// joinTurns renders turn numbers for the offer line, capped so a wide
// intra-thread hit cannot run the line off the screen.
func joinTurns(ns []int) string {
	const showMax = 3
	parts := make([]string, 0, showMax+1)
	for _, n := range ns[:min(len(ns), showMax)] {
		parts = append(parts, strconv.Itoa(n))
	}
	if len(ns) > showMax {
		parts = append(parts, "…")
	}
	return strings.Join(parts, ", ")
}

// interactiveRecallResolver returns a turn.RecallResolver that surfaces
// the §3.4 ASK-band recall offer and reads the user's decision. One
// question per turn, and the question states what accepting does: a
// single candidate is a yes/no, several are a pick-list.
//
// There is no follow-up decline-reason question under the banded default.
// A second prompt classifying a decline the user has already made is the
// same reflex-clearing surface the ruling struck down, and nothing reads
// the categorization yet (§4.3 feeds Phase-5 directive accrual, which is
// unbuilt). Under recall.ack-mode: always the superseded flow is restored
// verbatim, reason prompt included — that is what the escape hatch is
// for.
func interactiveRecallResolver(tm *term.Terminal, mode turn.RecallAckMode) turn.RecallResolver {
	out := tm.Out()
	return func(ctx context.Context, offer turn.RecallOffer) (turn.RecallResolution, error) {
		single := len(offer.Candidates) == 1
		preamble := make([]string, 0, 2*len(offer.Candidates)+1)
		if single {
			preamble = append(preamble, "related topic:")
			preamble = append(preamble, recallOfferLines(1, offer.Candidates[0])...)
		} else {
			preamble = append(preamble, "related topics:")
			for i, c := range offer.Candidates {
				preamble = append(preamble, recallOfferLines(i+1, c)...)
			}
		}
		prompt, keys := "accept which? [numbers / a=all / n=none]: ", "an"
		if single {
			prompt, keys = "pull into context? [y]es / [n]o: ", "yn"
		}
		// ActivityAsk, not Editor: this prompts from INSIDE turn close, over
		// the turn that is still the outer owner of the terminal.
		answer, err := tm.ReadLine(ctx, term.ActivityAsk, term.Question{
			Preamble: preamble,
			Prompt:   prompt,
			Keys:     keys,
		})
		if term.IsEndOrAbort(err) {
			// Session is ending / interrupted — decline all, no error.
			return turn.RecallResolution{}, nil
		}
		if err != nil {
			return turn.RecallResolution{}, err
		}

		var accept []int
		trimmed := strings.ToLower(strings.TrimSpace(answer.Text))
		switch {
		case trimmed == "" || trimmed == "n":
			// accept nothing
		case single:
			if trimmed == "y" {
				accept = []int{0}
			}
		case trimmed == "a":
			for i := range offer.Candidates {
				accept = append(accept, i)
			}
		default:
			for tok := range strings.FieldsSeq(trimmed) {
				n, perr := strconv.Atoi(tok)
				if perr != nil || n < 1 || n > len(offer.Candidates) {
					fmt.Fprintf(out, "ignoring %q\n", tok)
					continue
				}
				accept = append(accept, n-1)
			}
		}

		reason := turn.DeclineNotRelevant
		if mode == turn.RecallAckAlways && len(accept) < len(offer.Candidates) {
			// No Keys: the answers are words matched by prefix, so every
			// first rune is the start of a longer legal answer.
			rans, rerr := tm.ReadLine(ctx, term.ActivityAsk, term.Question{
				Prompt: "decline reason [not-relevant / wrong-project / already-known] (default not-relevant): ",
			})
			if rerr == nil {
				if r, ok := matchDeclineReason(rans.Text); ok {
					reason = r
				}
			}
		}
		return turn.RecallResolution{Accept: accept, Reason: reason}, nil
	}
}

// closureOfferLines renders the §3.5 closure offer's two context lines —
// the question's preamble, above the outcome prompt:
//
//	idle topic thr_N: <display name> — <gist>
//	  summary: <curator draft>
//
// The first line is the roster's topicLabel shape (an id, the name it
// resolves to, what it is about); the second is the draft the user is being
// asked to ack, which is a different thing from the gist and stays on its
// own line. The superseded form named only `topic thr_3`, which asked the
// user to close something they could not identify — the defect the recall
// offer's redesign fixed for its own prompt (user ruling 2026-08-06).
//
// The verb carries NO trailing colon: topicLabel's id-first shape supplies
// one, and `idle topic: thr_3: name` double-punctuates the same break.
func closureOfferLines(offer turn.ClosureOffer) []string {
	return []string{
		"idle topic " + topicLabel(offer.Display, offer.ThreadID, offer.Gist),
		fmt.Sprintf("  summary: %s", offer.Summary),
	}
}

// autoClosedLine renders the ONE committed line a §3.5 routine closure
// prints (SPEC §3.5). It carries the id because the documented revision
// path for a wrongly-summarized auto-closure is `/back-to <thr_id>`, and a
// line that omits the argument the fix needs is not actionable.
func autoClosedLine(n turn.ClosureNotice) string {
	return "closed " + topicLabel(n.Display, n.ThreadID, n.Summary)
}

// autoRecalledLine renders the ONE committed line a §3.4 auto-band recall
// prints (SPEC §3.4). Sibling of autoClosedLine, through the same
// topicLabel shape: a committed topic reference always renders as
// `thr_N: <display> — <gist>` (user ruling 2026-08-06), so the id the user
// needs to act on the fetch — /topics to see it, /back-to to steer it —
// leads the line, and the fetch the runtime made without asking is
// verifiable rather than merely announced. The hand-formatted form it
// replaces named the topic but not the id.
func autoRecalledLine(n turn.RecallNotice) string {
	return "recalled " + topicLabel(n.Display, n.ThreadID, n.Gist)
}

// interactiveClosureResolver returns a turn.ClosureResolver that surfaces
// the §3.5 closure offer at the prompt and reads the user's retire / wip /
// edit / defer decision. The [e]dit choice lets the user revise the
// curator's draft summary before acking (§3.5 ack-quality); a revised,
// non-identical summary flows back as ClosureResolution.EditedSummary so
// applyClosureResolution stores it and logs retire.ack edited=yes. On EOF
// or Ctrl-C it returns ClosureDefer so the thread is left untouched.
func interactiveClosureResolver(tm *term.Terminal) turn.ClosureResolver {
	// The offer's two context lines are the question's PREAMBLE, so they are
	// committed above the editor rather than printed by a separate call that
	// the editor could then repaint over — bug 5's exact shape. The re-ask
	// after an edit passes none: the context is already in scrollback.
	askOutcome := func(ctx context.Context, preamble []string) (string, error) {
		answer, err := tm.ReadLine(ctx, term.ActivityAsk, term.Question{
			Preamble: preamble,
			Prompt:   "close as? [r]esolved / [d]ecided / [a]bandoned / [w]ip / [e]dit summary / [s]kip: ",
			Keys:     "rdawes",
		})
		return strings.ToLower(strings.TrimSpace(answer.Text)), err
	}
	return func(ctx context.Context, offer turn.ClosureOffer) (turn.ClosureResolution, error) {
		choice, err := askOutcome(ctx, closureOfferLines(offer))
		if term.IsEndOrAbort(err) {
			return turn.ClosureResolution{Outcome: turn.ClosureDefer}, nil
		}
		if err != nil {
			return turn.ClosureResolution{}, err
		}

		edited := ""
		if choice == "e" {
			// Default, not Keys: the draft is a value to edit in place.
			answer, perr := tm.ReadLine(ctx, term.ActivityAsk, term.Question{
				Prompt:  "edit summary: ",
				Default: offer.Summary,
			})
			if term.IsEndOrAbort(perr) {
				return turn.ClosureResolution{Outcome: turn.ClosureDefer}, nil
			}
			if perr != nil {
				return turn.ClosureResolution{}, perr
			}
			revised := strings.TrimSpace(answer.Text)
			// Only a genuinely changed summary counts as an edit; resubmitting
			// the draft unchanged keeps edited=no (the ack-edit-rate canary
			// must not read a rubber-stamp as an edit).
			if revised != "" && revised != offer.Summary {
				edited = revised
			}
			// Re-ask for the outcome now that the summary is settled.
			choice, err = askOutcome(ctx, nil)
			if term.IsEndOrAbort(err) {
				return turn.ClosureResolution{Outcome: turn.ClosureDefer}, nil
			}
			if err != nil {
				return turn.ClosureResolution{}, err
			}
		}

		outcome, ok := closureOutcomeFor(choice)
		if !ok {
			// Empty / "s" / anything unrecognized → skip (defer).
			return turn.ClosureResolution{Outcome: turn.ClosureDefer}, nil
		}
		return turn.ClosureResolution{Outcome: outcome, EditedSummary: edited}, nil
	}
}

// closureOutcomeFor maps a single-letter closure choice to its outcome.
func closureOutcomeFor(choice string) (turn.ClosureOutcome, bool) {
	switch choice {
	case "r":
		return turn.ClosureResolved, true
	case "d":
		return turn.ClosureDecided, true
	case "a":
		return turn.ClosureAbandoned, true
	case "w":
		return turn.ClosureWIP, true
	default:
		return 0, false
	}
}

// matchDeclineReason resolves a user-typed token to a turn.DeclineReason
// by case-insensitive prefix match. Returns ok=false on empty input or
// no match (the caller defaults to not-relevant).
func matchDeclineReason(s string) (turn.DeclineReason, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", false
	}
	for _, r := range []turn.DeclineReason{
		turn.DeclineNotRelevant, turn.DeclineWrongProject, turn.DeclineAlreadyKnown,
	} {
		if strings.HasPrefix(string(r), s) {
			return r, true
		}
	}
	return "", false
}
