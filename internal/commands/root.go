// Package commands holds the cobra CLI.
package commands

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:          "admin",
	Short:        "boson admin panel (web UI + backend-for-frontend)",
	SilenceUsage: true,
}

// Execute runs the root command and exits non-zero on error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
