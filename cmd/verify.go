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
	verifyFlagHome  string
	verifyFlagQuiet bool
)

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "validate canonical state (schema, cross-refs, index drift)",
	Long: `Walk $PERSONANT_HOME and report schema violations
(spec §2.2 spine record limits, §2.5.1 project meta), missing
cross-references, and derived-file drift (delegated to
` + "`personant index check`" + `). Read-only — never writes a file.

Output ordering is stable: errors first, warnings second, drift third,
followed by a one-line summary. Exits 1 if any error or drift entry is
reported, 0 otherwise.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, err := resolveVerifyPaths()
		if err != nil {
			return err
		}
		// NOTE: verifyFlagQuiet and the stderrLogger are not yet plumbed
		// through the port (memops.MemoryOps.Verify takes no options).
		// If/when verbose verify output is needed, add VerifyOptions to
		// the port and the fileadapter.
		_ = verifyFlagQuiet
		ops := fileadapter.NewFileAdapter(paths)
		report, err := ops.Verify(context.Background())
		if err != nil {
			return err
		}
		printReport(report)
		if report.HasErrors() {
			os.Exit(1)
		}
		return nil
	},
	SilenceUsage:  true,
	SilenceErrors: false,
}

func resolveVerifyPaths() (store.PersonantPaths, error) {
	if verifyFlagHome != "" {
		return store.PathsForHome(verifyFlagHome), nil
	}
	return store.ResolvePaths()
}

// printReport emits the report to stdout in the spec's stable order.
// Errors and drift go to stdout (primary command output); the summary
// is also stdout. Findings printed with a leading "- " bullet.
func printReport(r memops.VerifyReport) {
	if len(r.Errors) > 0 {
		fmt.Println("errors:")
		for _, f := range r.Errors {
			fmt.Println("  - " + formatFinding(f))
		}
	}
	if len(r.Warnings) > 0 {
		fmt.Println("warnings:")
		for _, f := range r.Warnings {
			fmt.Println("  - " + formatFinding(f))
		}
	}
	if len(r.Drift) > 0 {
		fmt.Println("drift:")
		for _, d := range r.Drift {
			fmt.Println("  - " + d)
		}
	}
	fmt.Printf("verify: %d errors, %d warnings, %d drift entries\n",
		len(r.Errors), len(r.Warnings), len(r.Drift))
	if r.HasErrors() {
		fmt.Println("verify: FAILED")
	} else {
		fmt.Println("verify: ok")
	}
}

func formatFinding(f memops.VerifyFinding) string {
	switch {
	case f.Field == "" && f.Path == "":
		return f.Message
	case f.Field == "":
		return fmt.Sprintf("%s: %s", f.Path, f.Message)
	case f.Path == "":
		return fmt.Sprintf("[%s] %s", f.Field, f.Message)
	}
	return fmt.Sprintf("%s [%s]: %s", f.Path, f.Field, f.Message)
}

func init() {
	verifyCmd.Flags().StringVar(&verifyFlagHome, "home", "", "override $PERSONANT_HOME for this invocation (testing)")
	verifyCmd.Flags().BoolVar(&verifyFlagQuiet, "quiet", false, "suppress per-step logging (does not affect findings output)")
	rootCmd.AddCommand(verifyCmd)
}
