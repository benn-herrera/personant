package main

import (
	"os"
	"time"

	"github.com/spf13/cobra"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/ping"
)

var (
	pingFlagProvider string
	pingFlagPrompt   string
	pingFlagModel    string
	pingFlagTimeout  time.Duration
)

const defaultPingPrompt = "Respond with a single sentence to confirm connectivity."

var pingCmd = &cobra.Command{
	Use:   "ping",
	Short: "send one chat-completion to a configured provider",
	Long: `Smoke-test the connectivity layer end-to-end. Loads providers
from $PERSONANT_HOME/providers.toml, sends a single user prompt to the
named provider, and prints the response body to stdout. A one-line
summary (provider, model, tokens, elapsed) is written to stderr.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, err := resolvePaths()
		if err != nil {
			return err
		}
		ops := fileadapter.NewFileAdapter(paths)
		return ping.Run(ops, ping.Options{
			Provider: pingFlagProvider,
			Prompt:   pingFlagPrompt,
			Model:    pingFlagModel,
			Timeout:  pingFlagTimeout,
			Stdout:   os.Stdout,
			Stderr:   os.Stderr,
		})
	},
	SilenceUsage: true,
}

func init() {
	pingCmd.Flags().StringVar(&pingFlagProvider, "provider", memops.LocalProviderName, "provider name from providers.toml")
	pingCmd.Flags().StringVar(&pingFlagPrompt, "prompt", defaultPingPrompt, "prompt to send")
	pingCmd.Flags().StringVar(&pingFlagModel, "model", "", "override the provider's default model")
	pingCmd.Flags().DurationVar(&pingFlagTimeout, "timeout", 30*time.Second, "request timeout")
	rootCmd.AddCommand(pingCmd)
}
