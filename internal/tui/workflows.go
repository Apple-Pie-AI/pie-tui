package tui

import (
	"context"
	"fmt"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/browser"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/workflows"
)

// workflowEditorMsg reports the workflow editor's link (or why it didn't open).
type workflowEditorMsg struct {
	url string
	err error
}

// The editor runs inside this process - it closes with the hub, or after
// workflows.IdleTimeout of disuse - and a second "Customize workflows" reopens
// the same one instead of starting another.
var editor struct {
	sync.Mutex
	url string
}

// openWorkflowEditor serves the workflow editor for the configured repos and
// opens it in the browser.
func openWorkflowEditor() tea.Cmd {
	return func() tea.Msg {
		editor.Lock()
		url := editor.url
		editor.Unlock()
		if url == "" {
			cfg, err := config.Load()
			if err != nil {
				return workflowEditorMsg{err: err}
			}
			var repos []string
			for _, r := range cfg.Repos {
				if r.Path != "" {
					repos = append(repos, r.Path)
				}
			}
			if len(repos) == 0 {
				return workflowEditorMsg{err: fmt.Errorf("no repo configured - Edit config first")}
			}
			s := workflows.New(repos)
			s.Logf = func(string, ...interface{}) {} // never write over the hub's screen
			ready := make(chan string, 1)
			errc := make(chan error, 1)
			go func() {
				err := s.Serve(context.Background(), func(u string) {
					editor.Lock()
					editor.url = u
					editor.Unlock()
					ready <- u
				})
				editor.Lock()
				editor.url = ""
				editor.Unlock()
				errc <- err
			}()
			select {
			case url = <-ready:
			case err := <-errc:
				return workflowEditorMsg{err: err}
			}
		}
		if err := browser.Open(url); err != nil {
			return workflowEditorMsg{url: url, err: err}
		}
		return workflowEditorMsg{url: url}
	}
}

// workflowNotice is the hub's line for a workflowEditorMsg. The link carries
// the editor's token, so it is shown only when the browser could not be opened.
func workflowNotice(msg workflowEditorMsg) string {
	switch {
	case msg.err != nil && msg.url != "":
		return "open this link in a browser: " + msg.url
	case msg.err != nil:
		return "workflow editor: " + msg.err.Error()
	}
	return "workflow editor opened in your browser"
}
