package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version (VERSION env, set by the image)",
	Run: func(*cobra.Command, []string) {
		v := os.Getenv("VERSION")
		if v == "" {
			v = "dev"
		}
		fmt.Println(v)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
