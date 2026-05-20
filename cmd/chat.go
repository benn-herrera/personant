package main

import (
	"github.com/spf13/cobra"

	"personant/internal/chat"
	"personant/internal/memops/fileadapter"
	"personant/internal/store"
)

var (
	chatFlagHome     string
	chatFlagProject  string
	chatFlagProvider string
	chatFlagModel    string
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
		paths, err := resolveChatPaths()
		if err != nil {
			return err
		}
		return chat.Run(chat.Options{
			Ops:             fileadapter.NewFileAdapter(paths),
			ExplicitProject: chatFlagProject,
			ProviderName:    chatFlagProvider,
			Model:           chatFlagModel,
		})
	},
	SilenceUsage: true,
}

func resolveChatPaths() (store.PersonantPaths, error) {
	if chatFlagHome != "" {
		return store.PathsForHome(chatFlagHome), nil
	}
	return store.ResolvePaths()
}

func init() {
	chatCmd.Flags().StringVar(&chatFlagHome, "home", "", "override $PERSONANT_HOME for this invocation")
	chatCmd.Flags().StringVar(&chatFlagProject, "project", "", "explicit active project (id or name); skips bootstrap heuristics")
	chatCmd.Flags().StringVar(&chatFlagProvider, "provider", "", "override the default provider name")
	chatCmd.Flags().StringVar(&chatFlagModel, "model", "", "override the provider's default model")
	rootCmd.AddCommand(chatCmd)
}
