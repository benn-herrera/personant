package main

import (
	"os"

	"github.com/spf13/cobra"

	"personant/internal/chat"
	"personant/internal/log"
)

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
		return chat.Run(chat.Options{})
	},
	SilenceUsage: true,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Error("%v", err)
		os.Exit(1)
	}
}
