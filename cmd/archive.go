package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"personant/internal/memops"
)

// archiveSummaryWidth caps the spine-summary column in `archive list` so a
// long gist does not blow out the table. Truncated summaries get an ellipsis.
const archiveSummaryWidth = 48

var archiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "inspect and recover deep-cold archived topics",
	Long: `Operate on personant's recoverable git-based archive (§3.8).
Retired topics drained from the active spine are preserved as git
deletion commits with a lookup entry in archive/index.jsonl. 'list'
shows the index; 'recover' restores a topic to the spine as wip.`,
}

var archiveListCmd = &cobra.Command{
	Use:   "list",
	Short: "list archived topics from the index",
	Long: `Print every archived topic's lookup entry, sorted by thr_id.
Recovered topics RETAIN their entry and are marked RECOVERED.
Read-only — never writes a file.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Reconcile-first (#94): the index may hold an unstamped entry a
		// crash left behind; Reconcile's stamp-repair pass fixes it before
		// the listing reads it.
		ops, err := reconciledOps()
		if err != nil {
			return err
		}
		entries, err := ops.ListArchivedThreads(context.Background())
		if err != nil {
			return err
		}
		printArchiveList(entries)
		return nil
	},
	SilenceUsage:  true,
	SilenceErrors: false,
}

var archiveRecoverCmd = &cobra.Command{
	Use:   "recover <thr_id>",
	Short: "restore an archived topic to the spine",
	Long: `Restore an archived topic from its git deletion commit, verify
it against the stored tree hash, and re-add it to the spine as wip.
The archive-index entry is retained as a breadcrumb (RecoveredAt
stamped). Fails without recovering if the integrity check does not
match.`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: false,
	RunE: func(cmd *cobra.Command, args []string) error {
		thrID := args[0]
		// Reconcile-first (#94): recovery from the archive commits to an
		// unreconciled worktree otherwise, and an unstamped entry must be
		// stamp-repaired before RecoverThread can resolve it.
		ops, err := reconciledOps()
		if err != nil {
			return err
		}
		rec, err := ops.RecoverThread(context.Background(), thrID)
		if err != nil {
			switch {
			case errors.Is(err, memops.ErrArchiveEntryNotFound):
				return fmt.Errorf("no archived topic %s", thrID)
			case errors.Is(err, memops.ErrArchiveIntegrity):
				return fmt.Errorf("integrity check failed, topic %s NOT recovered", thrID)
			default:
				return fmt.Errorf("recover %s failed", thrID)
			}
		}
		fmt.Printf("recovered %s (state=%s) — back on the spine\n", rec.ID, rec.State)
		return nil
	},
}

// printArchiveList writes the archive index as a readable table. An empty
// index prints a friendly line, not an error.
func printArchiveList(entries []memops.ArchiveEntry) {
	if len(entries) == 0 {
		fmt.Println("no archived topics")
		return
	}
	fmt.Printf("%-10s  %-8s  %-20s  %-9s  %s\n", "THR_ID", "PROJECT", "ARCHIVED_AT", "STATE", "SUMMARY")
	for _, e := range entries {
		state := "archived"
		if e.RecoveredAt != "" {
			state = "RECOVERED"
		}
		fmt.Printf("%-10s  %-8s  %-20s  %-9s  %s\n",
			e.ThrID, e.Project, e.ArchivedAt, state, truncateSummary(e.SpineSummary))
	}
}

// truncateSummary clips s to archiveSummaryWidth runes, appending an ellipsis
// when clipped. Rune-aware so multibyte summaries are not split mid-codepoint.
func truncateSummary(s string) string {
	r := []rune(s)
	if len(r) <= archiveSummaryWidth {
		return s
	}
	return string(r[:archiveSummaryWidth-1]) + "…"
}

func init() {
	archiveCmd.AddCommand(archiveListCmd)
	archiveCmd.AddCommand(archiveRecoverCmd)
	rootCmd.AddCommand(archiveCmd)
}
