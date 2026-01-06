package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/toyamarinyon/gh-ultrahope/internal/prcreate"
)

func replyArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		// For usage/validation errors, show usage text.
		cmd.SilenceUsage = false
		return cobra.MaximumNArgs(0)(cmd, args)
	}
	// For runtime errors, avoid printing usage (business logic prints its own errors).
	cmd.SilenceUsage = true
	return nil
}

func init() {
	var debug bool

	replyCmd := &cobra.Command{
		Use:   "reply",
		Short: "Reply to a pull request review comment with an AI-drafted response",
		Long: `Reply to a pull request comment by pasting the comment URL.

Ultrahope will fetch the pull request title/body/diff and the comment body, then ask for a draft reply.
It will generate a concise response (in the same language as the PR/comment) and ask for confirmation before posting.`,
		Args: replyArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			code := prcreate.RunReply(prcreate.Options{
				Debug: debug,
			}, os.Stdin, os.Stdout, os.Stderr)

			if code == prcreate.ExitOK {
				return nil
			}
			return &exitError{code: code, printed: true}
		},
	}

	replyCmd.Flags().BoolVarP(&debug, "debug", "d", false, "Print extra debug info (never prints secrets)")

	prCmd.AddCommand(replyCmd)
}
