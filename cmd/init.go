package main

import (
	"context"

	"github.com/spf13/cobra"

	"personant/internal/log"
	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
)

var initFlagQuiet bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "scaffold $PERSONANT_HOME (idempotent)",
	Long: `Initialize the personant home directory with the canonical layout
(spec §2.1): directory tree, empty spine.jsonl/symbols.jsonl, seed
directives, providers.toml stub, README, .gitignore, git init, and an
initial commit. Re-running over an existing home is a near no-op.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, err := resolvePaths()
		if err != nil {
			return err
		}
		ops := fileadapter.NewFileAdapter(paths)
		opts := memops.InitOptions{
			Quiet: initFlagQuiet,
			Logger: func(format string, args ...any) {
				log.Info(format, args...)
			},
		}
		return ops.Init(context.Background(), opts)
	},
}

func init() {
	initCmd.Flags().BoolVar(&initFlagQuiet, "quiet", false, "suppress per-step output")
	rootCmd.AddCommand(initCmd)
}
