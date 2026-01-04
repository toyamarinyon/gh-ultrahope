package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/toyamarinyon/gh-ultrahope/internal/prcreate"
)

func createArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		// For usage/validation errors, show usage text.
		cmd.SilenceUsage = false
		return cobra.MaximumNArgs(1)(cmd, args)
	}
	// For runtime errors, avoid printing usage (business logic prints its own errors).
	cmd.SilenceUsage = true
	return nil
}

func init() {
	var edit bool
	var dryRun bool
	var debug bool

	createCmd := &cobra.Command{
		Use:   "create [base-branch]",
		Short: "Create a pull request with an AI-generated title and body",
		Long: `Create a pull request with an AI-generated title and body from git commits and diffs.

If base-branch is omitted, ultrahope will try to auto-detect a stacked base from origin/* refs,
and if that fails it will fall back to the repository default branch.
If detection fails, pass a base branch explicitly (e.g. "main") or set GH_REPO.`,
		Args: createArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var base string
			baseGiven := false
			if len(args) == 1 {
				base = args[0]
				baseGiven = true
			}

			code := prcreate.Run(prcreate.Options{
				BaseBranch: base,
				BaseGiven:  baseGiven,
				Edit:       edit,
				DryRun:     dryRun,
				Debug:      debug,
			}, os.Stdin, os.Stdout, os.Stderr)

			if code == prcreate.ExitOK {
				return nil
			}
			return &exitError{code: code, printed: true}
		},
	}

	createCmd.Flags().BoolVarP(&edit, "edit", "e", false, "Edit the generated output in $EDITOR")
	createCmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "Print generated title/body only (do not create PR)")
	createCmd.Flags().BoolVarP(&debug, "debug", "d", false, "Print extra debug info (never prints secrets)")

	prCmd.AddCommand(createCmd)
}
