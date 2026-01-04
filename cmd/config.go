package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/toyamarinyon/gh-ultrahope/internal/prcreate"
)

func init() {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Show loaded configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			cmd.SilenceUsage = true

			cfg, sources, err := prcreate.LoadFileConfig(context.Background())
			if err != nil {
				return &exitError{code: prcreate.ExitRuntimeErr, cause: err}
			}

			fmt.Fprintln(os.Stdout, "config files:")
			if len(sources) == 0 {
				fmt.Fprintln(os.Stdout, "- (none)")
			} else {
				for _, p := range sources {
					p = strings.TrimSpace(p)
					if p == "" {
						continue
					}
					fmt.Fprintf(os.Stdout, "- %s\n", p)
				}
			}

			b, err := yaml.Marshal(cfg)
			if err != nil {
				return &exitError{code: prcreate.ExitRuntimeErr, cause: err}
			}

			fmt.Fprintln(os.Stdout, "")
			fmt.Fprintln(os.Stdout, "config:")
			// yaml.Marshal always ends with a newline; keep output stable even if that changes.
			out := string(b)
			if !strings.HasSuffix(out, "\n") {
				out += "\n"
			}

			// Inline annotations for nil-able config fields, so users can see defaults at a glance.
			// Example:
			//   draft: null(default: false)
			//
			// Note: this is display-only; the underlying YAML remains standard if saved.
			sc := bufio.NewScanner(strings.NewReader(out))
			for sc.Scan() {
				line := sc.Text()
				trim := strings.TrimSpace(line)

				if cfg.Create.Draft == nil && trim == "draft: null" {
					line = strings.Replace(line, "draft: null", "draft: null(default: false)", 1)
				}
				if cfg.Create.SkipConfirm == nil && trim == "skip_confirm: null" {
					line = strings.Replace(line, "skip_confirm: null", "skip_confirm: null(default: false)", 1)
				}

				fmt.Fprintln(os.Stdout, line)
			}
			if err := sc.Err(); err != nil {
				return &exitError{code: prcreate.ExitRuntimeErr, cause: fmt.Errorf("failed to format config output: %w", err)}
			}
			return nil
		},
	}

	rootCmd.AddCommand(configCmd)
}
