package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"personant/internal/memops/fileadapter"
	"personant/internal/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "print version identity and the home's on-disk format",
	Long: `Print this binary's identity — substrate version, front-end version,
the on-disk format it writes, the build stamp, the Go toolchain — plus the
resolved $PERSONANT_HOME and the format stamp actually found there.

Deliberately ungated: this is the diagnostic you run precisely WHEN the
home is absent, unreadable, or written by a newer personant. It never runs
the SPEC §9.1 format gate, never reconciles, and never fails on a broken
home — a home it cannot read is reported as such and the command still
exits 0.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Print(version.Long())

		paths, err := resolvePaths()
		if err != nil {
			// Even unresolvable paths are reportable rather than fatal — the
			// binary identity above is the half that always prints.
			fmt.Print(version.Row("home", "unresolved: "+err.Error()))
			return nil
		}
		fmt.Print(version.Row("home", paths.Home))

		// The adapter is built DIRECTLY rather than through openHome: openHome
		// runs the gate, and a gated `version` would refuse exactly when it is
		// most needed. Nothing here writes, stamps, or reconciles.
		found, format, ferr := fileadapter.NewFileAdapter(paths).HomeFormat(context.Background())
		fmt.Print(version.Row("home stamp", version.HomeFormatLabel(found, format, ferr)))
		return nil
	},
	SilenceUsage: true,
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
