package main

import (
	"os"
	"time"

	"github.com/spf13/cobra"

	"personant/internal/modellist"
	"personant/internal/store"
)

var (
	modelsFlagHome     string
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
		paths, err := resolveModelsPaths()
		if err != nil {
			return err
		}
		return modellist.Run(paths, modellist.Options{
			Provider: modelsFlagProvider,
			Timeout:  modelsFlagTimeout,
			Stdout:   os.Stdout,
			Stderr:   os.Stderr,
		})
	},
	SilenceUsage: true,
}

func resolveModelsPaths() (store.PersonantPaths, error) {
	if modelsFlagHome != "" {
		return store.PathsForHome(modelsFlagHome), nil
	}
	return store.ResolvePaths()
}

func init() {
	modelsCmd.Flags().StringVar(&modelsFlagHome, "home", "", "override $PERSONANT_HOME for this invocation")
	modelsCmd.Flags().StringVar(&modelsFlagProvider, "provider", "local", "provider name from providers.toml")
	modelsCmd.Flags().DurationVar(&modelsFlagTimeout, "timeout", 30*time.Second, "request timeout")
	rootCmd.AddCommand(modelsCmd)
}
