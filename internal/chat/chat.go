// Package chat implements the interactive REPL for `personant chat`
// (and the bare `personant` invocation, which delegates here).
//
// The REPL bootstraps the active project (spec §4.5.7), prints a
// banner, and loops on stdin: dispatches slash commands, stubs shell
// escapes, and otherwise hands input to the turn package
// (internal/turn) for the §3.0 chain.
package chat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"personant/internal/clock"
	"personant/internal/curator"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/measure"
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

	Stdin  io.Reader // default os.Stdin
	Stdout io.Writer // default os.Stdout
	Stderr io.Writer // default os.Stderr

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
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Ops == nil {
		return errors.New("chat: Options.Ops is required")
	}

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
	printRecoveryBanner(opts.Stdout, recoveryReport)

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
		fmt.Fprintf(opts.Stdout, "day barrier: sealed day(s) %v into permanent history\n", res.DaysSealed)
	}

	providers, faults, err := ops.LoadProviders(ctx)
	if err != nil {
		return fmt.Errorf("chat: load providers: %w", err)
	}
	// A broken pool entry is non-fatal: warn and carry on, so a session
	// that does not use the broken provider still starts.
	for _, fault := range faults {
		fmt.Fprintf(opts.Stderr, "warn: provider %q unavailable: %s\n", fault.Name, fault.Reason)
	}
	if len(providers) == 0 {
		return errors.New("chat: no providers configured in providers.toml; edit it to add one (see spec §8.2.1)")
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
	// config.toml [chat] > the conventional default / provider's own
	// defaultModel. config.toml's "<provider>/<model>" reference names
	// both; ValidateConfig has already verified the reference is
	// well-formed and names a provider in the pool.
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
	provider, ok := providers[providerName]
	if !ok {
		// An explicitly-requested provider (--provider) that doesn't resolve
		// is a HARD bootstrap error: silently retargeting to another endpoint
		// would send traffic to an unintended provider. Name the unknown value
		// and list the available provider names (names only — never key
		// material or apiKeyFile paths). config.toml [chat] pins are not
		// checked here: ValidateConfig has already verified they name a
		// provider in the pool, so a pinned name always resolves above.
		if explicitProvider {
			return fmt.Errorf("chat: unknown provider %q; available providers: %s",
				providerName, strings.Join(sortedProviderNames(providers), ", "))
		}
		// No explicit provider and the conventional default ("local") isn't in
		// the pool: keep the documented default-selection behavior and pick the
		// first provider by name. The empty pool is already rejected above, so
		// this always resolves.
		provider, providerName = firstProvider(providers)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("chat: getwd: %w", err)
	}

	// One buffered reader services the entire session: bootstrap prompts
	// and the REPL loop both read from it. Splitting the reader between
	// stages would risk dropping bytes already buffered by the first
	// stage when stdin is a pipe.
	in := bufio.NewReader(opts.Stdin)

	// The line reader is the input seam (§4.3.1): liner-backed editing +
	// history on a real terminal, buffered reads for pipes/tests. Closed on
	// every return path so terminal state is restored and history flushed.
	lr := newLineReader(opts, in)
	defer func() { _ = lr.close() }()

	// The phase-labeled wait indicator. Built before the signal handler is
	// installed so the forced-exit path can always clear it. Inert (zero
	// bytes) unless the session is driving a real terminal.
	pr := newProgress(opts.Stdout, interactiveTTY(opts))
	defer pr.stop()

	project, err := bootstrapProject(opts, lr, ops, cwd)
	if err != nil {
		return err
	}
	if project.ID == "" {
		// User picked cancel at every prompt; treat as clean exit.
		return nil
	}

	// Build the model client.
	client := opts.Client
	if client == nil {
		client = model.NewHTTPClient(provider)
	}

	// Resolve the chat model against the provider's /models endpoint.
	// config.toml's [chat] defaultModel (and --model) name a chat model
	// that is not statically validated — it is checked here, when the
	// provider is actually used. Embedding is validated separately by
	// ValidateConfig; this probe is chat-provider only.
	effectiveModel, err := resolveChatModel(ctx, client, opts.Stderr, providerName, chatModel, provider.DefaultModel)
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
		fmt.Fprintf(opts.Stderr, "warn: log session.started: %v\n", err)
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
		fmt.Fprintf(opts.Stderr, "warn: embedding recall unavailable: %v\n", err)
		state.Recaller = measure.NewService(ops, nil)
	}

	// Progress reporting: the turn pipeline announces the stage it is in,
	// the indicator renders it. Both interactive resolvers below write
	// through pr.writer so that taking the terminal to ask the user a
	// question retires any in-flight indicator first.
	state.OnPhase = pr.phase

	// §3.4 recall UI surface (Part B): install an interactive resolver
	// so recalled threads can be pulled into Layer B at turn close.
	state.RecallResolver = interactiveRecallResolver(lr, pr.writer(opts.Stdout))

	// §3.5 decay-triggered closure flow: a model-backed curator drafts
	// the closure summary, and an interactive resolver lets the user
	// pick the retire / wip / defer outcome. Both must be installed for
	// the per-turn decay scan to run; the curator reuses the session's
	// chat client and resolved chat model.
	closureModel := effectiveModel
	if closureModel == "" {
		closureModel = provider.DefaultModel
	}
	state.Curator = curator.NewHTTPCurator(client, closureModel)
	state.ClosureResolver = interactiveClosureResolver(lr, pr.writer(opts.Stdout))

	banner := opts.Banner
	if banner == "" {
		banner = Banner
	}
	fmt.Fprintln(opts.Stdout, banner)
	fmt.Fprintf(opts.Stdout, "active: %s (%s)\n", project.Name, project.ID)

	// SIGINT (§4 clean shutdown): the first interrupt cancels the session
	// context so an in-flight turn unwinds and the loop exits through the
	// clean-shutdown path below (recaller close → §3.11 session-close
	// checkpoint → session-end log). A second interrupt during shutdown
	// forces an immediate exit (onInterrupt). liner consumes Ctrl-C itself
	// in raw mode (returns errInputAborted, handled in loop); this handler
	// catches interrupts delivered while the terminal is in cooked mode
	// (streaming a turn, shutting down, or the piped-stdin path).
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var interrupts atomic.Int32
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt)
	// Stop delivery THEN close so the handler goroutine's range exits when
	// Run returns (signal.Stop guarantees no send races the close).
	defer func() {
		signal.Stop(sigCh)
		close(sigCh)
	}()
	go func() {
		for range sigCh {
			onInterrupt(&interrupts, cancel, lr, pr, opts.Stderr)
		}
	}()

	if err := loop(runCtx, opts, lr, pr, ops, state, &interrupts, cancel); err != nil {
		return err
	}

	// Stop the recaller's indexer goroutine (no-op on the symbolic-only
	// recaller). Done before the session-close checkpoint so no in-flight
	// index write races the final commit.
	if err := state.Recaller.Close(); err != nil {
		fmt.Fprintf(opts.Stderr, "warn: recaller close: %v\n", err)
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
			fmt.Fprintln(opts.Stdout, "unclean turn state preserved for recovery — will reconcile on next open")
		} else {
			fmt.Fprintf(opts.Stderr, "warn: session-close checkpoint: %v\n", err)
		}
	}

	if err := ops.Log(ctx, memops.LogCategorySession, "ended", "active="+project.ID); err != nil {
		fmt.Fprintf(opts.Stderr, "warn: log session.end: %v\n", err)
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

// resolveChatModel verifies the effective chat model against the
// provider's /models endpoint and returns the model the session should
// use.
//
// The effective model is configModel when non-empty, else providerDefault.
//
// Graceful degradation: if /models cannot be reached or reports no
// models, the check is skipped — a warning is printed and the effective
// model is returned unverified, so an offline provider does not block
// the session.
//
// When /models returns a non-empty list:
//   - effective model present → returned as-is.
//   - configModel set but absent → warn and fall back to providerDefault;
//     if providerDefault is present return it, otherwise fail.
//   - configModel empty and providerDefault absent → fail: the provider
//     does not offer its own default model.
func resolveChatModel(ctx context.Context, client model.Client, stderr io.Writer, providerName, configModel, providerDefault string) (string, error) {
	effective := configModel
	if effective == "" {
		effective = providerDefault
	}

	probeCtx, cancel := context.WithTimeout(ctx, modelResolveTimeout)
	defer cancel()
	models, err := client.ListModels(probeCtx)
	if err != nil {
		fmt.Fprintf(stderr, "warn: could not verify model against provider %q /models: %v\n", providerName, err)
		return effective, nil
	}
	if len(models) == 0 {
		fmt.Fprintf(stderr, "warn: could not verify model against provider %q /models: provider reported no models\n", providerName)
		return effective, nil
	}

	offered := make(map[string]struct{}, len(models))
	for _, m := range models {
		offered[m.ID] = struct{}{}
	}
	if _, ok := offered[effective]; ok {
		return effective, nil
	}

	if configModel != "" {
		fmt.Fprintf(stderr, "warn: model %q is not offered by provider %q; falling back to provider default %q\n",
			configModel, providerName, providerDefault)
		if providerDefault == "" {
			return "", fmt.Errorf("chat: configured model %q unavailable and provider %q has no default model", configModel, providerName)
		}
		if _, ok := offered[providerDefault]; !ok {
			return "", fmt.Errorf("chat: configured model %q unavailable and provider %q default %q is also not offered", configModel, providerName, providerDefault)
		}
		return providerDefault, nil
	}

	return "", fmt.Errorf("chat: provider %q does not offer its own default model %q", providerName, providerDefault)
}

// onInterrupt handles one SIGINT (or an at-prompt Ctrl-C routed here). The
// first interrupt cancels the session context so the REPL unwinds through
// the clean-shutdown path; a second forces an immediate exit, restoring the
// terminal first. count is shared between the signal handler and the loop's
// at-prompt handling so a Ctrl-C during shutdown always forces regardless of
// which path saw the first one.
func onInterrupt(count *atomic.Int32, cancel context.CancelFunc, lr lineReader, pr *progress, stderr io.Writer) {
	// Clear any in-flight indicator FIRST. On the forced-exit path below no
	// deferred cleanup runs, so a half-drawn frame would be the last thing
	// left on the terminal; on the clean path it keeps the interrupt notice
	// from being drawn over.
	pr.stop()
	if count.Add(1) >= 2 {
		_ = lr.close() // restore terminal before the forced exit
		os.Exit(130)
	}
	cancel()
	fmt.Fprintln(stderr, "\ninterrupt received — shutting down (Ctrl-C again to force quit)")
}

// loop is the core REPL. Returns nil on a clean exit; non-nil on an
// unrecoverable I/O error (e.g. the input reader failing, not a
// per-turn LLM error which is logged and tolerated).
func loop(ctx context.Context, opts Options, lr lineReader, pr *progress, ops memops.MemoryOps, state *turn.State, interrupts *atomic.Int32, cancel context.CancelFunc) error {
	for {
		if ctx.Err() != nil {
			// A SIGINT during a turn cancelled the session — unwind cleanly.
			return nil
		}
		line, err := lr.prompt("> ")
		switch {
		case errors.Is(err, errInputAborted):
			// Ctrl-C at the prompt (liner raw mode) — route through the same
			// interrupt path as a delivered SIGINT, then unwind.
			onInterrupt(interrupts, cancel, lr, pr, opts.Stderr)
			return nil
		case errors.Is(err, io.EOF):
			if line == "" {
				fmt.Fprintln(opts.Stdout) // newline after the dangling prompt
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
		lr.appendHistory(input)

		switch {
		case strings.HasPrefix(trimmed, "/"):
			done, derr := dispatchSlash(ctx, opts, ops, state, trimmed)
			if derr != nil {
				fmt.Fprintf(opts.Stderr, "command error: %v\n", derr)
				continue
			}
			if done {
				return nil
			}
		case strings.HasPrefix(trimmed, "$") || strings.HasPrefix(trimmed, "#"):
			fmt.Fprintln(opts.Stderr, "shell escape ($/#) is not yet implemented (Phase 2.f)")
		default:
			if err := runOneTurn(ctx, opts, pr, state, input); err != nil {
				// A bad turn does not kill the session — log the error and
				// loop. The user can usually retry; the one exception is a
				// marker-retained failure (a CommitTurn failure this turn, or
				// the JournalTurn refusal every later turn hits on the retained
				// scope): in-session retry CANNOT work, and the prompt just
				// typed was refused before it was journaled — it is not
				// captured anywhere. Say both, instead of implying retry.
				fmt.Fprintf(opts.Stderr, "turn error: %v\n", err)
				if errors.Is(err, turn.ErrMarkerRetained) || errors.Is(err, memops.ErrConflictingMarker) {
					fmt.Fprintln(opts.Stderr, "unrecoverable in this session: the failed turn's recovery scope is still open — restart personant to recover; prompts typed until then are NOT captured, so save this one if you still need it")
				}
				continue
			}
		}
	}
}

func runOneTurn(ctx context.Context, opts Options, pr *progress, state *turn.State, input string) error {
	turnCtx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	// The body streams through pr.writer: its first byte retires the
	// pre-token indicator, and the turn's closing phase restarts it for the
	// post-stream wait.
	_, err := turn.Run(turnCtx, state, input, pr.writer(opts.Stdout))
	// Retire the closing-phase indicator on EVERY exit — normal, per-turn
	// error, or a cancelled turn — before anything else touches the
	// terminal. It owns the current line until it is stopped.
	pr.stop()
	if err != nil {
		return err
	}
	// Ensure the next prompt lands on a fresh line. The progress knows the
	// cursor column (it and the body write through the same wrapper), which
	// the returned body — already tag-stripped and newline-trimmed — does not.
	if !pr.atLineStart() {
		fmt.Fprintln(opts.Stdout)
	}
	return nil
}

// dispatchSlash returns done=true to signal the loop should exit. A
// returned error is per-command and non-fatal — the loop prints it and
// continues. Stubs and unknown commands write to stderr and return nil.
func dispatchSlash(ctx context.Context, opts Options, ops memops.MemoryOps, state *turn.State, line string) (bool, error) {
	cmd, rest := splitCommand(line)
	switch cmd {
	case "/quit", "/exit":
		return true, nil
	case "/help":
		fmt.Fprintln(opts.Stdout, helpText())
	case "/stats":
		printStats(opts.Stdout, ops, state)
	case "/version":
		printVersion(ctx, opts.Stdout, ops)
	case "/topic":
		return false, cmdTopic(ctx, opts, state, rest)
	case "/done":
		return false, cmdDone(ctx, state, rest)
	case "/pause":
		return false, cmdPause(ctx, opts, state, rest)
	case "/resume":
		return false, cmdResume(ctx, opts, state, rest)
	case "/back-to":
		return false, cmdBackTo(ctx, opts, state, rest)
	case "/project":
		return false, cmdProject(ctx, opts, ops, state, rest)
	case "/no-revisit":
		fmt.Fprintln(opts.Stderr, "/no-revisit is not yet implemented (recall accrual loop, §3.4)")
	case "/cd-project", "/model":
		fmt.Fprintf(opts.Stderr, "%s is not yet implemented (later phase)\n", cmd)
	default:
		fmt.Fprintf(opts.Stderr, "unknown command: %s; type /help for available commands\n", cmd)
	}
	return false, nil
}

// cmdTopic implements /topic <name> — force a new thread engaged for the
// next turn (§4.2).
func cmdTopic(ctx context.Context, opts Options, state *turn.State, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("usage: /topic <name>")
	}
	id, err := turn.CreateTopic(ctx, state, name)
	if err != nil {
		return err
	}
	fmt.Fprintf(opts.Stdout, "started topic %q (%s)\n", name, id)
	return nil
}

// cmdDone implements /done [thr_id|name] — manual §3.5 closure on the
// current owner thread (or the named one). SPEC §4.2 defines only the
// no-arg form (close the active thread); the optional reference is a
// dogfooding convenience for closing a specific thread.
func cmdDone(ctx context.Context, state *turn.State, rest string) error {
	return turn.ManualClosure(ctx, state, rest)
}

// cmdPause implements /pause [thr_id|name] (§2.2.1).
func cmdPause(ctx context.Context, opts Options, state *turn.State, rest string) error {
	id, err := turn.PauseThread(ctx, state, rest)
	if err != nil {
		return err
	}
	fmt.Fprintf(opts.Stdout, "paused %s\n", id)
	return nil
}

// cmdResume implements /resume [thr_id|name] (§2.2.1).
func cmdResume(ctx context.Context, opts Options, state *turn.State, rest string) error {
	id, err := turn.ResumeThread(ctx, state, rest)
	if err != nil {
		return err
	}
	fmt.Fprintf(opts.Stdout, "resumed %s\n", id)
	return nil
}

// cmdBackTo implements /back-to <thr_id|name> — explicit re-engagement
// (§4.2), promoting the thread into Layer B.
func cmdBackTo(ctx context.Context, opts Options, state *turn.State, rest string) error {
	if strings.TrimSpace(rest) == "" {
		return errors.New("usage: /back-to <thr_id|name>")
	}
	id, err := turn.BackToThread(ctx, state, rest)
	if err != nil {
		return err
	}
	fmt.Fprintf(opts.Stdout, "re-engaged %s\n", id)
	return nil
}

// cmdProject dispatches the /project family: bare (print info), rename, and
// switch (§4.2 / §4.5.6).
func cmdProject(ctx context.Context, opts Options, ops memops.MemoryOps, state *turn.State, rest string) error {
	if rest == "" {
		printProjectInfo(opts.Stdout, state.ActiveProject)
		return nil
	}
	sub, arg := splitCommand(rest)
	switch sub {
	case "rename":
		return cmdProjectRename(ctx, opts, ops, state, arg)
	case "switch":
		return cmdProjectSwitch(ctx, opts, ops, state, arg)
	default:
		return fmt.Errorf("unknown /project subcommand %q; use rename or switch", sub)
	}
}

// cmdProjectRename implements /project rename <new-name> (§4.5.6): update
// the display name only — id, path, and remote URL are unchanged, and no
// spine entries are rewritten.
func cmdProjectRename(ctx context.Context, opts Options, ops memops.MemoryOps, state *turn.State, name string) error {
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
	fmt.Fprintf(opts.Stdout, "renamed project %s: %q → %q\n", meta.ID, old, name)
	return nil
}

// cmdProjectSwitch implements /project switch <name-or-id> (§4.2 / §4.5.6):
// change the active project to a known one. The Layer B/C working set is
// membership for the OUTGOING project (closure/recall are project-scoped),
// so turn.SwitchProject resets it — v0.1 keeps no per-project working-set
// snapshot, so the switched-to project cold-starts.
func cmdProjectSwitch(ctx context.Context, opts Options, ops memops.MemoryOps, state *turn.State, ref string) error {
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
		fmt.Fprintf(opts.Stdout, "already on project %s (%s)\n", target.Name, target.ID)
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
	fmt.Fprintf(opts.Stdout, "switched to project %s (%s)\n", target.Name, target.ID)
	return nil
}

func splitCommand(line string) (cmd, rest string) {
	line = strings.TrimSpace(line)
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		return line[:i], strings.TrimSpace(line[i+1:])
	}
	return line, ""
}

func helpText() string {
	return `available commands:
  /help                        show this help
  /quit, /exit                 exit the session
  /topic <name>                start a new thread and engage it
  /done [thr_id|name]          close the active (or named) thread (§3.5)
  /pause [thr_id|name]         pause the active (or named) thread
  /resume [thr_id|name]        resume a paused thread
  /back-to <thr_id|name>       re-engage a thread into the working set
  /project                     print active project info
  /project rename <new-name>   rename the active project
  /project switch <name-or-id> switch to a known project
  /stats                       print session and spine statistics
  /version                     print version identity and home format

stubbed (later phase):
  /no-revisit                  tighten recall threshold (recall accrual)
  /cd-project <path>           set active project root
  /model <id>                  switch active model

shell escape:
  $<cmd>                       fire-and-forget shell (not yet implemented)
  #<cmd>                       shell with capture (not yet implemented)`
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
func bootstrapProject(opts Options, lr lineReader, ops memops.MemoryOps, cwd string) (memops.ProjectMeta, error) {
	return bootstrapProjectWithExplicit(opts, lr, ops, cwd, opts.ExplicitProject)
}

func bootstrapProjectWithExplicit(opts Options, lr lineReader, ops memops.MemoryOps, cwd string, explicit string) (memops.ProjectMeta, error) {
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
		return promptConfirmation(opts, lr, ops, cwd, result.Candidate)

	case memops.StepNeedsFallback:
		return promptFallback(opts, lr, ops, cwd)

	default:
		return memops.ProjectMeta{}, fmt.Errorf("chat: bootstrap: unrecognized step %v", result.Step)
	}
}

// promptConfirmation surfaces the §4.5.7 last-active resume prompt.
func promptConfirmation(opts Options, lr lineReader, ops memops.MemoryOps, cwd string, candidate *memops.ProjectMeta) (memops.ProjectMeta, error) {
	if candidate == nil {
		return promptFallback(opts, lr, ops, cwd)
	}
	ctx := context.Background()
	if candidate.LastActive == "" {
		fmt.Fprintf(opts.Stdout, "Resume work on '%s'? [y]es / [n]o / <other-name-or-id>: ",
			candidate.Name)
	} else {
		fmt.Fprintf(opts.Stdout, "Resume work on '%s' (last active %s)? [y]es / [n]o / <other-name-or-id>: ",
			candidate.Name, candidate.LastActive)
	}
	ans, err := lr.prompt("")
	if err != nil {
		return memops.ProjectMeta{}, err
	}
	ans = strings.TrimSpace(ans)
	switch ans {
	case "", "y", "Y", "yes":
		if err := ops.SetLastActiveProject(ctx, candidate.ID); err != nil {
			return memops.ProjectMeta{}, fmt.Errorf("chat: write last-active: %w", err)
		}
		return *candidate, nil
	case "n", "N", "no":
		return promptFallback(opts, lr, ops, cwd)
	default:
		// Treat as <other-name-or-id> — re-resolve with explicit override.
		return bootstrapProjectWithExplicit(opts, lr, ops, cwd, ans)
	}
}

// promptFallback surfaces the §4.5.7 final fallback prompt.
func promptFallback(opts Options, lr lineReader, ops memops.MemoryOps, cwd string) (memops.ProjectMeta, error) {
	ctx := context.Background()
	for {
		fmt.Fprintln(opts.Stdout, "No active project resolved.")
		fmt.Fprint(opts.Stdout, "  [c] create a new project rooted at this directory\n")
		fmt.Fprint(opts.Stdout, "  [s] switch to a known project\n")
		fmt.Fprint(opts.Stdout, "  [n] no project (use prj_default)\n")
		fmt.Fprint(opts.Stdout, "choice: ")
		ans, err := lr.prompt("")
		if err != nil {
			return memops.ProjectMeta{}, err
		}
		switch strings.TrimSpace(strings.ToLower(ans)) {
		case "c":
			meta, err := createNewProject(opts, lr, ops, cwd)
			if err != nil {
				fmt.Fprintf(opts.Stderr, "create failed: %v\n", err)
				continue
			}
			return meta, nil
		case "s":
			meta, ok, err := pickExistingProject(opts, lr, ops)
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
			fmt.Fprintln(opts.Stderr, "invalid choice; enter c, s, or n")
		}
	}
}

func createNewProject(opts Options, lr lineReader, ops memops.MemoryOps, cwd string) (memops.ProjectMeta, error) {
	ctx := context.Background()
	fmt.Fprint(opts.Stdout, "display name: ")
	name, err := lr.prompt("")
	if err != nil {
		return memops.ProjectMeta{}, err
	}
	name = strings.TrimSpace(name)
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
		fmt.Fprintf(opts.Stderr, "warn: log project.created: %v\n", err)
	}
	// §3.11: a project creation is a structural change. This runs during
	// bootstrap (a NEW project chosen at session start), distinct from
	// `personant init` which commits the default project at scaffold time —
	// so it needs its own recovery point rather than riding init's commit.
	// Non-fatal: the session-close commit is the backstop. (last-active is a
	// substrate marker kept out of git, so it rides this commit only if the
	// adapter tracks it; either way the project meta is captured.)
	if err := ops.Checkpoint(ctx, "project-create "+id); err != nil {
		fmt.Fprintf(opts.Stderr, "warn: project-create checkpoint: %v\n", err)
	}
	return meta, nil
}

func pickExistingProject(opts Options, lr lineReader, ops memops.MemoryOps) (memops.ProjectMeta, bool, error) {
	metas, err := ops.ListProjects(context.Background())
	if err != nil {
		return memops.ProjectMeta{}, false, err
	}
	if len(metas) == 0 {
		fmt.Fprintln(opts.Stderr, "no known projects to switch to")
		return memops.ProjectMeta{}, false, nil
	}
	fmt.Fprintln(opts.Stdout, "known projects:")
	for _, m := range metas {
		fmt.Fprintf(opts.Stdout, "  %s  %s\n", m.ID, m.Name)
	}
	fmt.Fprint(opts.Stdout, "id or name: ")
	ans, err := lr.prompt("")
	if err != nil {
		return memops.ProjectMeta{}, false, err
	}
	ans = strings.TrimSpace(ans)
	for _, m := range metas {
		if m.ID == ans || m.Name == ans {
			return m, true, nil
		}
	}
	fmt.Fprintf(opts.Stderr, "no project matched %q\n", ans)
	return memops.ProjectMeta{}, false, nil
}

// interactiveRecallResolver returns a turn.RecallResolver that surfaces
// the §3.4 recall offer at the prompt and reads the user's accept /
// decline decision. Kept deliberately small — U/X polish is deferred.
func interactiveRecallResolver(lr lineReader, out io.Writer) turn.RecallResolver {
	return func(_ context.Context, offer turn.RecallOffer) (turn.RecallResolution, error) {
		fmt.Fprintln(out, "recalled threads related to this turn:")
		for i, c := range offer.Candidates {
			fmt.Fprintf(out, "  [%d] %s  score=%.2f  (%s)\n",
				i+1, c.ThreadID, c.Score, strings.Join(c.Layers(), "+"))
		}
		fmt.Fprint(out, "accept which? [numbers / a=all / n=none]: ")
		ans, err := lr.prompt("")
		if isEndOrAbort(err) {
			// Session is ending / interrupted — decline all, no error.
			return turn.RecallResolution{}, nil
		}
		if err != nil {
			return turn.RecallResolution{}, err
		}

		var accept []int
		switch trimmed := strings.ToLower(strings.TrimSpace(ans)); trimmed {
		case "", "n":
			// accept nothing
		case "a":
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
		if len(accept) < len(offer.Candidates) {
			fmt.Fprint(out, "decline reason [not-relevant / wrong-project / already-known] (default not-relevant): ")
			rans, rerr := lr.prompt("")
			if rerr == nil {
				if r, ok := matchDeclineReason(rans); ok {
					reason = r
				}
			}
		}
		return turn.RecallResolution{Accept: accept, Reason: reason}, nil
	}
}

// interactiveClosureResolver returns a turn.ClosureResolver that surfaces
// the §3.5 closure offer at the prompt and reads the user's retire / wip /
// edit / defer decision. The [e]dit choice lets the user revise the
// curator's draft summary before acking (§3.5 ack-quality); a revised,
// non-identical summary flows back as ClosureResolution.EditedSummary so
// applyClosureResolution stores it and logs retire.ack edited=yes. On EOF
// or Ctrl-C it returns ClosureDefer so the thread is left untouched.
func interactiveClosureResolver(lr lineReader, out io.Writer) turn.ClosureResolver {
	askOutcome := func() (string, error) {
		fmt.Fprint(out, "close as? [r]esolved / [d]ecided / [a]bandoned / [w]ip / [e]dit summary / [s]kip: ")
		ans, err := lr.prompt("")
		return strings.ToLower(strings.TrimSpace(ans)), err
	}
	return func(_ context.Context, offer turn.ClosureOffer) (turn.ClosureResolution, error) {
		fmt.Fprintf(out, "thread %s has gone idle — closure suggested.\n", offer.ThreadID)
		fmt.Fprintf(out, "  summary: %s\n", offer.Summary)

		choice, err := askOutcome()
		if isEndOrAbort(err) {
			return turn.ClosureResolution{Outcome: turn.ClosureDefer}, nil
		}
		if err != nil {
			return turn.ClosureResolution{}, err
		}

		edited := ""
		if choice == "e" {
			revised, perr := lr.promptWithDefault("edit summary: ", offer.Summary)
			if isEndOrAbort(perr) {
				return turn.ClosureResolution{Outcome: turn.ClosureDefer}, nil
			}
			if perr != nil {
				return turn.ClosureResolution{}, perr
			}
			revised = strings.TrimSpace(revised)
			// Only a genuinely changed summary counts as an edit; resubmitting
			// the draft unchanged keeps edited=no (the ack-edit-rate canary
			// must not read a rubber-stamp as an edit).
			if revised != "" && revised != offer.Summary {
				edited = revised
			}
			// Re-ask for the outcome now that the summary is settled.
			choice, err = askOutcome()
			if isEndOrAbort(err) {
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
