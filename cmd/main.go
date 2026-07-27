package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"personant/internal/chat"
	"personant/internal/eventlog"
	"personant/internal/log"
	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/store"
	"personant/internal/version"
)

// flagHome backs the shared --home persistent flag declared on rootCmd.
// All subcommands inherit it; resolvePaths consults it before falling
// back to $PERSONANT_HOME / ~/.personant resolution.
var flagHome string

// flagAllowNewerHome backs --allow-newer-home: the explicit override that
// opens a home whose on-disk format exceeds what this binary writes
// (SPEC §9.1). Refusal is the default because this binary cannot know the
// newer layout; the override is a corruption risk taken knowingly, so
// every use leaves both a stderr warning and an event-log entry.
var flagAllowNewerHome bool

var rootCmd = &cobra.Command{
	Use:   "personant",
	Short: "a persistent-context ai agent",
	Long: `Personant is a single-user agent runtime with persistent working
memory across projects. With no subcommand it drops into an interactive
chat session (see 'personant chat'). Subcommands handle scaffolding,
indexing, verification, and provider-connectivity smoke tests.`,
	// Bare invocation drops into the chat REPL with default options.
	// '--help'/'-h' still prints help (cobra short-circuits before RunE).
	RunE: func(cmd *cobra.Command, args []string) error {
		ops, paths, err := openHome()
		if err != nil {
			return err
		}
		return chat.Run(chat.Options{
			Ops:         ops,
			HistoryFile: historyPath(paths),
		})
	},
	SilenceUsage: true,
}

// historyPath is the REPL line-edit history file (§2.1 <home>/history,
// §4.3.1). Operational state; the store gitignores it alongside last-active.
func historyPath(paths store.PersonantPaths) string {
	return filepath.Join(paths.Home, "history")
}

// resolvePaths honors --home for tests; otherwise uses the standard
// $PERSONANT_HOME / ~/.personant resolution.
func resolvePaths() (store.PersonantPaths, error) {
	if flagHome != "" {
		return store.PathsForHome(flagHome), nil
	}
	return store.ResolvePaths()
}

// checkHomeFormat reads the home's on-disk format revision and runs the
// SPEC §9.1 gate over it. READ-ONLY: it never stamps and never logs.
//
// It is the whole gate for `personant init` — which must refuse to
// scaffold over a home written by a newer personant, but must not stamp,
// because the home may not exist yet and store.Init already owns the
// write-if-missing of version.toml — and the read half of openHome.
func checkHomeFormat(ctx context.Context, ops memops.MemoryOps) (memops.HomeFormatDecision, error) {
	found, format, err := ops.HomeFormat(ctx)
	if err != nil {
		return memops.HomeFormatDecision{}, fmt.Errorf("read home format: %w", err)
	}
	return memops.GateHomeFormat(found, format, version.CurrentHomeFormat, flagAllowNewerHome)
}

// openHome resolves the home paths, builds the substrate adapter, and runs
// the §9.1 on-disk format gate before returning it. It is the single
// process-boundary entry point for opening the substrate: reconciledOps
// adds Reconcile on top of it for the CLI verbs, and the two chat entry
// points hand the adapter it returns to chat.Run. A gate only some callers
// run is not a gate, which is why chat.Run takes an already-opened adapter
// rather than resolving one itself.
//
// ORDERING INVARIANT (load-bearing): the gate runs BEFORE Reconcile.
// Reconcile is crash recovery — it resets the worktree to a recovery
// point. Running it against a layout this binary does not understand would
// rearrange bytes it cannot interpret.
func openHome() (*fileadapter.FileAdapter, store.PersonantPaths, error) {
	ctx := context.Background()
	paths, err := resolvePaths()
	if err != nil {
		return nil, store.PersonantPaths{}, err
	}
	ops := fileadapter.NewFileAdapter(paths)
	decision, err := checkHomeFormat(ctx, ops)
	if err != nil {
		return nil, store.PersonantPaths{}, err
	}

	// Greenfield: no home on disk yet. There is no layout to adopt forward
	// and no event log to append to — writing either here would scaffold,
	// and scaffolding belongs to `personant init` (and, on the chat path,
	// chat.Run's idempotent Init, which writes the canonical stamp itself).
	// The gate above still ran; an absent stamp cannot be newer than this
	// binary, so nothing is waved through.
	if _, serr := os.Stat(paths.Home); errors.Is(serr, os.ErrNotExist) {
		return ops, paths, nil
	}

	if decision.StampFormat != 0 {
		if err := ops.StampHomeFormat(ctx, decision.StampFormat); err != nil {
			return nil, store.PersonantPaths{}, fmt.Errorf("stamp home format: %w", err)
		}
	}
	// LogBootstrap FIRST among the event writes: it carries the log-tail
	// heal that every pre-Reconcile append needs (see its doc comment), so
	// anything logged after it appends onto a sound tail. The format it
	// records is the EFFECTIVE revision this session runs against, not the
	// constant — on an allow-newer open the two differ, and which one was
	// actually opened is the forensic value of the line.
	if err := eventlog.LogBootstrap(paths, decision.Format); err != nil {
		fmt.Fprintf(os.Stderr, "warn: log system.bootstrap: %v\n", err)
	}
	if decision.NewerAccepted {
		warnNewerHomeAccepted(paths, decision.Format)
	}
	return ops, paths, nil
}

// warnNewerHomeAccepted records a knowingly-unsafe open: --allow-newer-home
// waved through a home written by a newer personant. It leaves a trace in
// both places someone reads afterward — the terminal now, and the event
// log later — because a silent override is indistinguishable from a
// normal open when the corruption surfaces days later.
func warnNewerHomeAccepted(paths store.PersonantPaths, onDisk int) {
	fmt.Fprintf(os.Stderr,
		"WARNING: --allow-newer-home opened a home at format %d with a personant that writes %d.\n"+
			"         This binary does not understand the newer layout; writing to it can CORRUPT it.\n"+
			"         Upgrade personant instead unless you know exactly why you are doing this.\n",
		onDisk, version.CurrentHomeFormat)
	if err := eventlog.Log(paths, memops.LogCategorySystem, "home-format-override",
		fmt.Sprintf("on-disk=%d binary=%d", onDisk, version.CurrentHomeFormat)); err != nil {
		fmt.Fprintf(os.Stderr, "warn: log system.home-format-override: %v\n", err)
	}
}

// reconciledOps opens the home through openHome — which gates the §9.1
// on-disk format FIRST — and then runs the startup Reconcile before
// handing the adapter to a CLI verb (#94, SPEC §4.5.8 — the R2
// verbs-bypass-Reconcile carry). Every verb that READS or REBUILDS
// substrate state must open through here: operating on an unreconciled
// home would read torn canonical state back as truth (verify reporting
// phantom errors, index rebuild baking crash debris into derived files).
// Same refuse-on-error semantics as chat: a Reconcile failure refuses the
// verb; the recovery marker is left behind so a retry converges. On a
// home that was never initialized Reconcile fails to resolve HEAD — that
// refusal is honest too (nothing to operate on); verbs do NOT scaffold
// (`personant init` owns that).
//
// A non-routine reconcile prints one terse stderr note; the full detail
// is in the event log and, interactively, the chat banner.
func reconciledOps() (*fileadapter.FileAdapter, error) {
	ops, _, err := openHome()
	if err != nil {
		return nil, err
	}
	rep, err := ops.Reconcile(context.Background())
	if err != nil {
		return nil, fmt.Errorf("substrate reconcile failed — refusing to operate on an unreconciled home (rerun retries recovery): %w", err)
	}
	if !rep.Quiet() {
		fmt.Fprintf(os.Stderr, "recovery: unclean shutdown repaired before opening (cells: %s); see event log\n",
			strings.Join(rep.CellsHit, ", "))
	}
	return ops, nil
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagHome, "home", "", "override $PERSONANT_HOME for this invocation (testing)")
	rootCmd.PersistentFlags().BoolVar(&flagAllowNewerHome, "allow-newer-home", false,
		"open a home whose on-disk format is newer than this binary writes (SPEC §9.1); risks corrupting it")

	// `personant --version` prints exactly version.Short() and nothing
	// else. Cobra's default template wraps it in its own "personant version
	// X" line, which would render the string twice over.
	rootCmd.Version = version.Short()
	rootCmd.SetVersionTemplate("{{.Version}}\n")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Error("%v", err)
		os.Exit(1)
	}
}
