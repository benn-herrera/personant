package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"personant/internal/log"
	"personant/internal/memops"
)

var (
	indexFlagQuiet   bool
	indexFlagVerbose bool
)

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "manage derived files (symbols.jsonl, project digests)",
	Long: `Operate on personant's derived files (spec §2.1): symbols.jsonl
and projects/prj_<n>/digest.json. Subcommands rebuild from canonical
sources or check for drift.`,
}

var indexRebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "regenerate derived files from canonical sources",
	Long: `Read spine.jsonl and rewrite symbols.jsonl plus per-project
digest.json files atomically. Idempotent: a rebuild after a clean
rebuild produces no changes.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Reconcile-first (#94): rebuilding derived files from a torn
		// canonical tree would bake crash debris into symbols.jsonl.
		ops, err := reconciledOps()
		if err != nil {
			return err
		}
		opts := memops.IndexBuildOptions{
			Quiet:  indexFlagQuiet,
			Logger: stderrLogger(),
		}
		return ops.RegenerateDerivedState(context.Background(), opts)
	},
}

var indexCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "report drift between derived files and what rebuild would produce",
	Long: `Compute what rebuild would produce and compare to what is on
disk. Prints any drift to stdout. Exits 0 when all derived files match
the rebuild output; exits 1 when drift is detected. Never writes
anything.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Reconcile-first (#94): drift measured against torn canonical
		// state is noise, not signal.
		ops, err := reconciledOps()
		if err != nil {
			return err
		}
		opts := memops.IndexBuildOptions{
			Quiet:  indexFlagQuiet,
			Logger: stderrLogger(),
		}
		result, err := ops.CheckDerivedState(context.Background(), opts)
		if err != nil {
			return err
		}
		// Print drift to stdout regardless of --quiet (it's the
		// command's primary output, not informational chatter).
		for _, d := range result.Drifts {
			if d.Detail != "" {
				fmt.Printf("%s: %s (%s)\n", d.Path, d.Status, d.Detail)
			} else {
				fmt.Printf("%s: %s\n", d.Path, d.Status)
			}
		}
		if !result.OK() {
			// Exit non-zero without printing the cobra usage line —
			// drift is a normal outcome, not an invocation error.
			os.Exit(1)
		}
		if indexFlagVerbose && !indexFlagQuiet {
			log.Info("index: check ok")
		}
		return nil
	},
	SilenceUsage:  true,
	SilenceErrors: false,
}

func stderrLogger() func(format string, args ...any) {
	return func(format string, args ...any) {
		log.Info(format, args...)
	}
}

func init() {
	indexCmd.PersistentFlags().BoolVar(&indexFlagQuiet, "quiet", false, "suppress per-step output")
	indexCheckCmd.Flags().BoolVarP(&indexFlagVerbose, "verbose", "v", false, "print confirmation when no drift is detected")

	indexCmd.AddCommand(indexRebuildCmd)
	indexCmd.AddCommand(indexCheckCmd)
	rootCmd.AddCommand(indexCmd)
}
