package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/store"
)

var (
	initFlagHome  string
	initFlagQuiet bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "scaffold $PERSONANT_HOME (idempotent)",
	Long: `Initialize the personant home directory with the canonical layout
(spec §2.1): directory tree, empty spine.jsonl/symbols.jsonl, seed
directives, providers.toml stub, README, .gitignore, git init, and an
initial commit. Re-running over an existing home is a near no-op.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, err := resolveInitPaths()
		if err != nil {
			return err
		}
		ops := fileadapter.NewFileAdapter(paths)
		opts := memops.InitOptions{
			Quiet: initFlagQuiet,
			Logger: func(format string, args ...any) {
				fmt.Fprintf(os.Stderr, format+"\n", args...)
			},
		}
		return ops.Init(context.Background(), opts)
	},
}

// resolveInitPaths honors --home for tests; otherwise uses the standard
// $PERSONANT_HOME / ~/.personant resolution.
func resolveInitPaths() (store.PersonantPaths, error) {
	if initFlagHome != "" {
		return store.PathsForHome(initFlagHome), nil
	}
	return store.ResolvePaths()
}

func init() {
	initCmd.Flags().StringVar(&initFlagHome, "home", "", "override $PERSONANT_HOME for this invocation (testing)")
	initCmd.Flags().BoolVar(&initFlagQuiet, "quiet", false, "suppress per-step output")
	rootCmd.AddCommand(initCmd)
}
