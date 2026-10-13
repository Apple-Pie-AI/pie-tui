// Package workflows stores a repo's workflow customizations - the prompt
// slots a team rewrote in the workflow editor - and serves that editor.
// Customizations live on this machine, one file per repo under ~/.pie/workflows.
package workflows

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// file is the on-disk shape.
type file struct {
	Version   int               `json:"version"`
	Slots     map[string]string `json:"slots"`
	UpdatedAt string            `json:"updated_at,omitempty"`
}

// Load is repo's saved slot overrides (none when it has no file).
func Load(repo string) (map[string]string, error) {
	data, err := os.ReadFile(paths.WorkflowsFor(repo))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", paths.WorkflowsFor(repo), err)
	}
	return clean(f.Slots), nil
}

// For is repo's workflow: its overrides, or Apple Pie's built-in prompts when
// it has none or the file can't be read - a broken customization never stops
// a run.
func For(repo string) agent.Prompts {
	if repo == "" {
		return agent.Prompts{}
	}
	over, err := Load(repo)
	if err != nil {
		return agent.Prompts{}
	}
	return agent.NewPrompts(over)
}

// Save replaces repo's overrides. Text equal to a slot's default is dropped,
// so the file only ever holds real customizations; an unknown slot or text
// over agent.MaxSlotLen is an error.
func Save(repo string, slots map[string]string) error {
	for id, t := range slots {
		if !agent.KnownSlot(id) {
			return fmt.Errorf("unknown slot %q", id)
		}
		if len(t) > agent.MaxSlotLen {
			return fmt.Errorf("%s is %d bytes; the limit is %d", id, len(t), agent.MaxSlotLen)
		}
	}
	data, err := json.MarshalIndent(file{Version: 1, Slots: clean(slots),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	if err != nil {
		return err
	}
	p := paths.WorkflowsFor(repo)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".workflows-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// clean keeps known, non-blank, non-default slots, normalized.
func clean(in map[string]string) map[string]string {
	out := map[string]string{}
	for id, t := range in {
		t = agent.NormalizeSlotText(t)
		if agent.KnownSlot(id) && strings.TrimSpace(t) != "" && t != agent.SlotDefault(id) && len(t) <= agent.MaxSlotLen {
			out[id] = t
		}
	}
	return out
}
