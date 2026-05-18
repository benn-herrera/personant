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
	"sort"
	"strconv"
	"strings"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/recall"
	"personant/internal/store"
	"personant/internal/turn"
	"personant/internal/workset"
)

// Options carries the knobs the cobra layer passes through.
type Options struct {
	ExplicitProject string // --project flag; "" → run the §4.5.7 waterfall
	HomeOverride    string // --home flag
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
}

const (
	// Banner is printed once on startup. Single line, terse.
	Banner = "personant — type /help for commands; Ctrl-D to exit"

	// defaultProviderName is the conventional name used in providers.toml
	// for the on-machine OpenAI-compatible endpoint (e.g. llama-server).
	defaultProviderName = "local"

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

	paths, err := resolvePaths(opts.HomeOverride)
	if err != nil {
		return fmt.Errorf("chat: resolve paths: %w", err)
	}

	ctx := context.Background()
	ops := fileadapter.NewFileAdapter(paths)

	// Idempotent home scaffold. If the home is already initialized this is
	// a near-noop; if it's a bare directory (e.g. user manually created
	// providers.toml without running `personant init`), this lands the
	// canonical layout (spine.jsonl, threads/, projects/, directives/, .git/,
	// etc.) without clobbering accrued state files (user.md, providers.toml).
	if err := ops.Init(ctx, memops.InitOptions{Quiet: true}); err != nil {
		return fmt.Errorf("chat: scaffold home: %w", err)
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
		return fmt.Errorf("chat: no providers configured in %s; edit it to add one (see spec §8.2.1)", paths.Providers)
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
		providerName = defaultProviderName
	}
	provider, ok := providers[providerName]
	if !ok {
		// If the conventional default isn't there, pick the first by name.
		provider, providerName = firstProvider(providers)
		if providerName == "" {
			return fmt.Errorf("chat: provider %q not found in %s", opts.ProviderName, paths.Providers)
		}
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

	project, err := bootstrapProject(opts, in, ops, cwd)
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

	if err := ops.Log(ctx, "system", "bootstrap",
		fmt.Sprintf("active=%s provider=%s", project.ID, providerName)); err != nil {
		fmt.Fprintf(opts.Stderr, "warn: log session.start: %v\n", err)
	}

	state := turn.NewState(ops, project, provider, client)
	if chatModel != "" {
		state.Model = chatModel
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
		state.Recaller = recall.NewService(ops, embedder)
	}
	// Index-build failure (e.g. the embedding endpoint unreachable)
	// degrades gracefully to symbolic-only recall — never blocks the
	// session. Prepare on the default symbolic-only Service is a no-op.
	if err := state.Recaller.Prepare(ctx); err != nil {
		fmt.Fprintf(opts.Stderr, "warn: embedding recall unavailable: %v\n", err)
		state.Recaller = recall.NewService(ops, nil)
	}

	// §3.4 recall UI surface (Part B): install an interactive resolver
	// so recalled threads can be pulled into Layer B at turn close.
	state.RecallResolver = interactiveRecallResolver(in, opts.Stdout)

	banner := opts.Banner
	if banner == "" {
		banner = Banner
	}
	fmt.Fprintln(opts.Stdout, banner)
	fmt.Fprintf(opts.Stdout, "active: %s (%s)\n", project.Name, project.ID)

	if err := loop(opts, in, ops, state); err != nil {
		return err
	}

	if err := ops.Log(ctx, "session", "ended", "active="+project.ID); err != nil {
		fmt.Fprintf(opts.Stderr, "warn: log session.end: %v\n", err)
	}
	return nil
}

func resolvePaths(homeOverride string) (store.PersonantPaths, error) {
	if homeOverride != "" {
		return store.PathsForHome(homeOverride), nil
	}
	return store.ResolvePaths()
}

func firstProvider(providers map[string]memops.Provider) (memops.Provider, string) {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return memops.Provider{}, ""
	}
	return providers[names[0]], names[0]
}

// loop is the core REPL. Returns nil on a clean exit; non-nil on an
// unrecoverable I/O error (e.g. the input reader failing, not a
// per-turn LLM error which is logged and tolerated).
func loop(opts Options, in *bufio.Reader, ops memops.MemoryOps, state *turn.State) error {
	for {
		fmt.Fprint(opts.Stdout, "> ")
		line, err := in.ReadString('\n')
		if errors.Is(err, io.EOF) {
			if line == "" {
				fmt.Fprintln(opts.Stdout) // newline after the dangling prompt
				return nil
			}
			// fall through: process the final line, then EOF on next read.
		} else if err != nil {
			return fmt.Errorf("chat: read input: %w", err)
		}
		input := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimSpace(input)
		if trimmed == "" {
			continue
		}

		switch {
		case strings.HasPrefix(trimmed, "/"):
			done, err := dispatchSlash(opts, ops, state, trimmed)
			if err != nil {
				fmt.Fprintf(opts.Stderr, "command error: %v\n", err)
				continue
			}
			if done {
				return nil
			}
		case strings.HasPrefix(trimmed, "$") || strings.HasPrefix(trimmed, "#"):
			fmt.Fprintln(opts.Stderr, "shell escape ($/#) is not yet implemented (Phase 2.f)")
		default:
			if err := runOneTurn(opts, state, input); err != nil {
				// A bad turn does not kill the session — log the error and
				// loop. The user can retry.
				fmt.Fprintf(opts.Stderr, "turn error: %v\n", err)
				continue
			}
		}
	}
}

func runOneTurn(opts Options, state *turn.State, input string) error {
	ctx, cancel := context.WithTimeout(context.Background(), turnTimeout)
	defer cancel()
	body, err := turn.Run(ctx, state, input, opts.Stdout)
	if err != nil {
		return err
	}
	// turn.Run streams the body to opts.Stdout as it arrives. Ensure the
	// next prompt lands on a fresh line.
	if !strings.HasSuffix(body, "\n") {
		fmt.Fprintln(opts.Stdout)
	}
	return nil
}

// dispatchSlash returns done=true to signal the loop should exit.
// Errors are returned for the caller to print; non-fatal command-level
// problems (unknown command, stub) are written to stderr inside this
// function and do not return an error.
func dispatchSlash(opts Options, ops memops.MemoryOps, state *turn.State, line string) (bool, error) {
	cmd, rest := splitCommand(line)
	switch cmd {
	case "/quit", "/exit":
		return true, nil

	case "/help":
		fmt.Fprintln(opts.Stdout, helpText())
		return false, nil

	case "/project":
		if rest != "" {
			fmt.Fprintln(opts.Stderr, "project switch/rename are not yet implemented (Phase 2.c.3)")
			return false, nil
		}
		printProjectInfo(opts.Stdout, state.ActiveProject)
		return false, nil

	case "/stats":
		printStats(opts.Stdout, ops, state)
		return false, nil

	case "/topic", "/done", "/pause", "/resume", "/back-to", "/no-revisit",
		"/cd-project", "/model":
		fmt.Fprintf(opts.Stderr, "%s is not yet implemented (later phase)\n", cmd)
		return false, nil

	default:
		fmt.Fprintf(opts.Stderr, "unknown command: %s; type /help for available commands\n", cmd)
		return false, nil
	}
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
  /help                    show this help
  /quit, /exit             exit the session
  /project                 print active project info
  /stats                   print session and spine statistics

stubbed (later phase):
  /topic <name>            force a new thread (Phase 2.c.3+)
  /done, /pause, /resume   thread state transitions (Phase 4)
  /back-to <thr_id>        re-engage a retired thread (Phase 4)
  /no-revisit              tighten recall threshold (Phase 4)
  /cd-project <path>       set active project root (Phase 2.c.3)
  /model <id>              switch active model (Phase 2.c.3)

shell escape:
  $<cmd>                   fire-and-forget shell (not yet implemented)
  #<cmd>                   shell with capture (not yet implemented)`
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
func bootstrapProject(opts Options, in *bufio.Reader, ops memops.MemoryOps, cwd string) (memops.ProjectMeta, error) {
	return bootstrapProjectWithExplicit(opts, in, ops, cwd, opts.ExplicitProject)
}

func bootstrapProjectWithExplicit(opts Options, in *bufio.Reader, ops memops.MemoryOps, cwd string, explicit string) (memops.ProjectMeta, error) {
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
		return promptConfirmation(opts, in, ops, cwd, result.Candidate)

	case memops.StepNeedsFallback:
		return promptFallback(opts, in, ops, cwd)

	default:
		return memops.ProjectMeta{}, fmt.Errorf("chat: bootstrap: unrecognized step %v", result.Step)
	}
}

// promptConfirmation surfaces the §4.5.7 last-active resume prompt.
func promptConfirmation(opts Options, in *bufio.Reader, ops memops.MemoryOps, cwd string, candidate *memops.ProjectMeta) (memops.ProjectMeta, error) {
	if candidate == nil {
		return promptFallback(opts, in, ops, cwd)
	}
	ctx := context.Background()
	for {
		if candidate.LastActive == "" {
			fmt.Fprintf(opts.Stdout, "Resume work on '%s'? [y]es / [n]o / <other-name-or-id>: ",
				candidate.Name)
		} else {
			fmt.Fprintf(opts.Stdout, "Resume work on '%s' (last active %s)? [y]es / [n]o / <other-name-or-id>: ",
				candidate.Name, candidate.LastActive)
		}
		ans, err := readLine(in)
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
			return promptFallback(opts, in, ops, cwd)
		default:
			// Treat as <other-name-or-id> — re-resolve with explicit override.
			return bootstrapProjectWithExplicit(opts, in, ops, cwd, ans)
		}
	}
}

// promptFallback surfaces the §4.5.7 final fallback prompt.
func promptFallback(opts Options, in *bufio.Reader, ops memops.MemoryOps, cwd string) (memops.ProjectMeta, error) {
	ctx := context.Background()
	for {
		fmt.Fprintln(opts.Stdout, "No active project resolved.")
		fmt.Fprint(opts.Stdout, "  [c] create a new project rooted at this directory\n")
		fmt.Fprint(opts.Stdout, "  [s] switch to a known project\n")
		fmt.Fprint(opts.Stdout, "  [n] no project (use prj_default)\n")
		fmt.Fprint(opts.Stdout, "choice: ")
		ans, err := readLine(in)
		if err != nil {
			return memops.ProjectMeta{}, err
		}
		switch strings.TrimSpace(strings.ToLower(ans)) {
		case "c":
			meta, err := createNewProject(opts, in, ops, cwd)
			if err != nil {
				fmt.Fprintf(opts.Stderr, "create failed: %v\n", err)
				continue
			}
			return meta, nil
		case "s":
			meta, ok, err := pickExistingProject(opts, in, ops)
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
			meta, err := ops.LoadProject(ctx, store.DefaultProjectID)
			if err != nil {
				return memops.ProjectMeta{}, fmt.Errorf("chat: load default: %w", err)
			}
			if err := ops.SetLastActiveProject(ctx, store.DefaultProjectID); err != nil {
				return memops.ProjectMeta{}, fmt.Errorf("chat: write last-active: %w", err)
			}
			return meta, nil
		default:
			fmt.Fprintln(opts.Stderr, "invalid choice; enter c, s, or n")
		}
	}
}

func createNewProject(opts Options, in *bufio.Reader, ops memops.MemoryOps, cwd string) (memops.ProjectMeta, error) {
	ctx := context.Background()
	fmt.Fprint(opts.Stdout, "display name: ")
	name, err := readLine(in)
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
	now := time.Now().UTC().Format(time.RFC3339)
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
	if err := ops.Log(ctx, "project", "created", "id="+id+" name="+name); err != nil {
		fmt.Fprintf(opts.Stderr, "warn: log project.created: %v\n", err)
	}
	return meta, nil
}

func pickExistingProject(opts Options, in *bufio.Reader, ops memops.MemoryOps) (memops.ProjectMeta, bool, error) {
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
	ans, err := readLine(in)
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

// readLine reads a single line from r, stripping the trailing newline.
// io.EOF before any data is returned as ("", io.EOF); io.EOF after a
// non-empty partial line is returned as (line, nil).
func readLine(r *bufio.Reader) (string, error) {
	s, err := r.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			if s == "" {
				return "", io.EOF
			}
			return strings.TrimRight(s, "\r\n"), nil
		}
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

// interactiveRecallResolver returns a turn.RecallResolver that surfaces
// the §3.4 recall offer at the prompt and reads the user's accept /
// decline decision. Kept deliberately small — U/X polish is deferred.
func interactiveRecallResolver(in *bufio.Reader, out io.Writer) turn.RecallResolver {
	return func(_ context.Context, offer turn.RecallOffer) (turn.RecallResolution, error) {
		fmt.Fprintln(out, "recalled threads related to this turn:")
		for i, c := range offer.Candidates {
			fmt.Fprintf(out, "  [%d] %s  score=%.2f  (%s)\n",
				i+1, c.ThreadID, c.Score, strings.Join(c.Layers(), "+"))
		}
		fmt.Fprint(out, "accept which? [numbers / a=all / n=none]: ")
		ans, err := readLine(in)
		if errors.Is(err, io.EOF) {
			// Session is ending — decline all, no error.
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
			rans, rerr := readLine(in)
			if rerr == nil {
				if r, ok := matchDeclineReason(rans); ok {
					reason = r
				}
			}
		}
		return turn.RecallResolution{Accept: accept, Reason: reason}, nil
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
