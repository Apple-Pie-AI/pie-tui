package workflows

import (
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
)

// Every agent prompt is reachable from some workflow, every agent node opens a
// real prompt, and every edge joins two nodes of its own workflow.
func TestWorkflowGraphs(t *testing.T) {
	stages := map[string]bool{}
	for _, s := range agent.Stages {
		stages[s.ID] = true
	}
	seen := map[string]bool{}
	for _, w := range Workflows {
		ids := map[string]bool{}
		cells := map[[2]int]string{}
		for _, n := range w.Nodes {
			if ids[n.ID] {
				t.Errorf("%s: duplicate node %s", w.ID, n.ID)
			}
			ids[n.ID] = true
			if c := cells[[2]int{n.Col, n.Row}]; c != "" {
				t.Errorf("%s: %s and %s share a cell", w.ID, c, n.ID)
			}
			cells[[2]int{n.Col, n.Row}] = n.ID
			switch n.Kind {
			case NodeAgent:
				if !stages[n.Stage] {
					t.Errorf("%s: agent node %s has unknown stage %q", w.ID, n.ID, n.Stage)
				}
				seen[n.Stage] = true
			case NodeStart, NodeYou, NodeAuto:
				if n.Stage != "" {
					t.Errorf("%s: %s is not an agent but has a prompt", w.ID, n.ID)
				}
			default:
				t.Errorf("%s: %s has kind %q", w.ID, n.ID, n.Kind)
			}
		}
		for _, e := range w.Edges {
			if !ids[e.From] || !ids[e.To] {
				t.Errorf("%s: edge %s→%s leaves the workflow", w.ID, e.From, e.To)
			}
		}
	}
	for id := range stages {
		if !seen[id] {
			t.Errorf("stage %s is in no workflow - the editor could never open it", id)
		}
	}
}
