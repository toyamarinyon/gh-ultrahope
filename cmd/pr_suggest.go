package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/toyamarinyon/gh-ultrahope/internal/prsuggest"
)

func suggestArgs(cmd *cobra.Command, args []string) error {
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
	var create bool
	var debug bool

	suggestCmd := &cobra.Command{
		Use:   "suggest [base-branch]",
		Short: "Suggest a pull request title and body from git commits and diffs",
		Long: `Suggest a pull request title and body from git commits and diffs.

If base-branch is omitted, ultrahope will auto-detect the repository default branch.
If detection fails, pass a base branch explicitly (e.g. "main") or set GH_REPO.`,
		Args: suggestArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var base string
			baseGiven := false
			if len(args) == 1 {
				base = args[0]
				baseGiven = true
			}

			code := prsuggest.Run(prsuggest.Options{
				BaseBranch: base,
				BaseGiven:  baseGiven,
				Edit:       edit,
				Create:     create,
				Debug:      debug,
			}, os.Stdin, os.Stdout, os.Stderr)

			if code == prsuggest.ExitOK {
				return nil
			}
			return &exitError{code: code, printed: true}
		},
	}

	suggestCmd.Flags().BoolVarP(&edit, "edit", "e", false, "Edit the suggested output in $EDITOR")
	suggestCmd.Flags().BoolVarP(&create, "create", "c", false, "Create PR with suggested title/body (with confirmation)")
	suggestCmd.Flags().BoolVarP(&debug, "debug", "d", false, "Print extra debug info (never prints secrets)")

	prCmd.AddCommand(suggestCmd)
}
