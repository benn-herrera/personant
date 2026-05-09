package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"personant/internal/index"
	"personant/internal/store"
)

var (
	indexFlagHome    string
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
		paths, err := resolveIndexPaths()
		if err != nil {
			return err
		}
		opts := index.Options{
			Quiet:  indexFlagQuiet,
			Logger: stderrLogger(),
		}
		return index.Rebuild(paths, opts)
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
		paths, err := resolveIndexPaths()
		if err != nil {
			return err
		}
		opts := index.Options{
			Quiet:  indexFlagQuiet,
			Logger: stderrLogger(),
		}
		result, err := index.Check(paths, opts)
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
			fmt.Fprintln(os.Stderr, "index: check ok")
		}
		return nil
	},
	SilenceUsage:  true,
	SilenceErrors: false,
}

func resolveIndexPaths() (store.PersonantPaths, error) {
	if indexFlagHome != "" {
		return store.PathsForHome(indexFlagHome), nil
	}
	return store.ResolvePaths()
}

func stderrLogger() func(format string, args ...any) {
	return func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}

func init() {
	indexCmd.PersistentFlags().StringVar(&indexFlagHome, "home", "", "override $PERSONANT_HOME for this invocation (testing)")
	indexCmd.PersistentFlags().BoolVar(&indexFlagQuiet, "quiet", false, "suppress per-step output")
	indexCheckCmd.Flags().BoolVarP(&indexFlagVerbose, "verbose", "v", false, "print confirmation when no drift is detected")

	indexCmd.AddCommand(indexRebuildCmd)
	indexCmd.AddCommand(indexCheckCmd)
	rootCmd.AddCommand(indexCmd)
}
