package cmd

import "github.com/spf13/cobra"

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Pull request helpers",
}

func init() {
	rootCmd.AddCommand(prCmd)
}
