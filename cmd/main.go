package main

import (
	"os"
	"path/filepath"

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

func init() {
	rootCmd.PersistentFlags().StringVar(&flagHome, "home", "", "override $PERSONANT_HOME for this invocation (testing)")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Error("%v", err)
		os.Exit(1)
	}
}
