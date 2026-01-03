package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/toyamarinyon/gh-ultrahope/internal/prsuggest"
)

var rootCmd = &cobra.Command{
	Use:           "ultrahope",
	Short:         "AI-powered GitHub workflow assistant",
	SilenceErrors: true,  // we print errors ourselves in Execute()
	SilenceUsage:  false, // subcommands may toggle for usage errors
}

func init() {
	// Keep UX GitHub-CLI-like; avoid exposing Cobra's default completion command.
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}

type exitError struct {
	code    int
	printed bool
	cause   error
}

func (e *exitError) Error() string {
	if e == nil {
		return ""
	}
	if e.cause != nil {
		return e.cause.Error()
	}
	return "command failed"
}

func Execute() int {
	err := rootCmd.Execute()
	if err == nil {
		return prsuggest.ExitOK
	}

	var ee *exitError
	if errors.As(err, &ee) && ee != nil {
		if !ee.printed {
			msg := ee.Error()
			if msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
		}
		if ee.code != 0 {
			return ee.code
		}
		return prsuggest.ExitRuntimeErr
	}

	// Cobra usage/validation errors (unknown command, too many args, etc).
	fmt.Fprintln(os.Stderr, err.Error())
	return prsuggest.ExitInvalidArgs
}
