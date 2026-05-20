package main

import (
	"os"
	"time"

	"github.com/spf13/cobra"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/modellist"
)

var (
	modelsFlagProvider string
	modelsFlagTimeout  time.Duration
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "list models available from a configured provider",
	Long: `Query the named provider's /models endpoint and print one model
identifier per line, sorted ascending. Loads providers from
$PERSONANT_HOME/providers.toml. A one-line summary (provider, count,
elapsed) is written to stderr.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, err := resolvePaths()
		if err != nil {
			return err
		}
		ops := fileadapter.NewFileAdapter(paths)
		return modellist.Run(ops, modellist.Options{
			Provider: modelsFlagProvider,
			Timeout:  modelsFlagTimeout,
			Stdout:   os.Stdout,
			Stderr:   os.Stderr,
		})
	},
	SilenceUsage: true,
}

func init() {
	modelsCmd.Flags().StringVar(&modelsFlagProvider, "provider", memops.LocalProviderName, "provider name from providers.toml")
	modelsCmd.Flags().DurationVar(&modelsFlagTimeout, "timeout", 30*time.Second, "request timeout")
	rootCmd.AddCommand(modelsCmd)
}
