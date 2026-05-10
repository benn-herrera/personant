package main

import (
	"github.com/spf13/cobra"

	"personant/internal/chat"
)

var (
	chatFlagHome     string
	chatFlagProject  string
	chatFlagProvider string
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "start an interactive chat session",
	Long: `Start an interactive chat session. The bare 'personant' invocation
delegates here. Bootstraps the active project (spec §4.5.7), prints a
banner, and loops on stdin: slash commands ('/help', '/quit',
'/project', '/stats', plus stubs for later phases), shell-escape stubs
('$', '#'), and otherwise drives one full §3.0 turn through the
turn-handler chain.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return chat.Run(chat.Options{
			ExplicitProject: chatFlagProject,
			HomeOverride:    chatFlagHome,
			ProviderName:    chatFlagProvider,
		})
	},
	SilenceUsage: true,
}

func init() {
	chatCmd.Flags().StringVar(&chatFlagHome, "home", "", "override $PERSONANT_HOME for this invocation")
	chatCmd.Flags().StringVar(&chatFlagProject, "project", "", "explicit active project (id or name); skips bootstrap heuristics")
	chatCmd.Flags().StringVar(&chatFlagProvider, "provider", "", "override the default provider name")
	rootCmd.AddCommand(chatCmd)
}
