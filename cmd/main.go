package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"personant/internal/chat"
	"personant/internal/log"
	"personant/internal/memops/fileadapter"
	"personant/internal/store"
)

// flagHome backs the shared --home persistent flag declared on rootCmd.
// All subcommands inherit it; resolvePaths consults it before falling
// back to $PERSONANT_HOME / ~/.personant resolution.
var flagHome string

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
		paths, err := resolvePaths()
		if err != nil {
			return err
		}
		return chat.Run(chat.Options{
			Ops:         fileadapter.NewFileAdapter(paths),
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

// reconciledOps builds the substrate adapter and runs the startup
// Reconcile before handing it to a CLI verb (#94, SPEC §4.5.8 — the R2
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
	paths, err := resolvePaths()
	if err != nil {
		return nil, err
	}
	ops := fileadapter.NewFileAdapter(paths)
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
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Error("%v", err)
		os.Exit(1)
	}
}
