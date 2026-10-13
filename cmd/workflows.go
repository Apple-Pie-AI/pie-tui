package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Apple-Pie-AI/pie-tui/internal/browser"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/workflows"
)

// `pie workflows` opens the workflow editor: each stage's prompt, with the
// passages a team may rewrite editable and the contract locked.
func init() {
	var repo string
	var noOpen bool
	c := &cobra.Command{
		Use:   "workflows",
		Short: "Customize each stage's prompt (plan, implement, self-review, ...) in a local web editor",
		RunE: func(_ *cobra.Command, _ []string) error {
			repos, err := editorRepos(repo)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return workflows.New(repos).Serve(ctx, func(url string) {
				fmt.Printf("Workflow editor: %s\n(localhost only; closes after %s idle or on Ctrl-C)\n", url, workflows.IdleTimeout)
				if !noOpen {
					if err := browser.Open(url); err != nil {
						fmt.Printf("could not open a browser (%v) - open the link above\n", err)
					}
				}
			})
		},
	}
	c.Flags().StringVar(&repo, "repo", "", "repo to open first (default: the first configured repo)")
	c.Flags().BoolVar(&noOpen, "no-open", false, "print the editor's link instead of opening a browser")
	rootCmd.AddCommand(c)
}

// editorRepos is every configured repo, with --repo first (added when it
// isn't configured - naming it is consent to edit its workflow).
func editorRepos(first string) ([]string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	var out []string
	if first != "" {
		abs, err := filepath.Abs(config.Expand(first))
		if err != nil {
			return nil, err
		}
		out = append(out, abs)
	}
	for _, r := range cfg.Repos {
		if r.Path != "" && (len(out) == 0 || r.Path != out[0]) {
			out = append(out, r.Path)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no repo configured - run `pie init`, or pass --repo PATH")
	}
	return out, nil
}
