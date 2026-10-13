package workflows

// The workflows Apple Pie runs, as the editor draws them: what happens, in
// order, and who does each step. Only agent nodes have a prompt; the rest are
// there so a team sees where its words land. They describe the pipeline in
// internal/runner and docs/workflows.md - change them together.

// Node kinds.
const (
	NodeStart = "start" // where the workflow begins
	NodeAgent = "agent" // an agent session - opens its stage prompt
	NodeYou   = "you"   // a decision that waits for you
	NodeAuto  = "auto"  // Apple Pie does it itself, no agent
)

// Node is one step. Col/Row place it on the graph's grid.
type Node struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Note  string `json:"note,omitempty"`
	Stage string `json:"stage,omitempty"` // agent nodes: the agent.Stages id
	Col   int    `json:"col"`
	Row   int    `json:"row"`
}

// Edge is a transition; Back marks a loop to an earlier step.
type Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
	Back  bool   `json:"back,omitempty"`
}

// Workflow is one end-to-end flow.
type Workflow struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Blurb string `json:"blurb"`
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Workflows are the flows the editor offers, in the order the docs give them.
var Workflows = []Workflow{
	{
		ID: "ticket-to-pr", Title: "Ticket → pull request",
		Blurb: "From a Jira key or a markdown ticket to an open pull request, with your review before anything is pushed.",
		Nodes: []Node{
			{ID: "ticket", Kind: NodeStart, Title: "Ticket", Note: "Jira key or .md file", Col: 0},
			{ID: "plan", Kind: NodeAgent, Title: "Plan", Stage: "plan", Col: 1},
			{ID: "plan-gate", Kind: NodeYou, Title: "You review the plan", Note: "optional; answer its questions", Col: 2},
			{ID: "implement", Kind: NodeAgent, Title: "Implement", Stage: "implement", Col: 3},
			{ID: "self-review", Kind: NodeAgent, Title: "Self-review", Stage: "self-review", Col: 4},
			{ID: "review-fix", Kind: NodeAgent, Title: "Fix round", Stage: "review-fix", Col: 4, Row: 1},
			{ID: "verify", Kind: NodeAgent, Title: "Verify", Stage: "verify", Col: 5},
			{ID: "change-gate", Kind: NodeYou, Title: "You review the change", Note: "every file and diff", Col: 6},
			{ID: "needs-you", Kind: NodeYou, Title: "NEEDS YOU", Note: "not green: you decide", Col: 6, Row: 1},
			{ID: "pr", Kind: NodeAuto, Title: "Commit, push, PR", Note: "Apple Pie, never the agent", Col: 7},
		},
		Edges: []Edge{
			{From: "ticket", To: "plan"},
			{From: "plan", To: "plan-gate"},
			{From: "plan-gate", To: "plan", Label: "feedback", Back: true},
			{From: "plan-gate", To: "implement", Label: "approve"},
			{From: "implement", To: "self-review"},
			{From: "self-review", To: "review-fix", Label: "fix"},
			{From: "self-review", To: "verify", Label: "pass"},
			{From: "review-fix", To: "verify"},
			{From: "verify", To: "change-gate", Label: "green"},
			{From: "verify", To: "needs-you", Label: "not green"},
			{From: "change-gate", To: "pr", Label: "approve"},
		},
	},
	{
		ID: "review-comments", Title: "Review comments",
		Blurb: "Reviewers commented on a PR: you pick the comments, the agent fixes them locally, you preview every fix and reply, then Apple Pie pushes and answers.",
		Nodes: []Node{
			{ID: "comments-in", Kind: NodeStart, Title: "PR comments", Note: "from GitHub, bots too", Col: 0},
			{ID: "pick", Kind: NodeYou, Title: "You pick comments", Note: "fix, skip, or instruct", Col: 1},
			{ID: "fix", Kind: NodeAgent, Title: "Fix comments", Stage: "comments", Col: 2},
			{ID: "preview", Kind: NodeYou, Title: "You preview fixes", Note: "each fix's diff and draft reply", Col: 3},
			{ID: "revise", Kind: NodeAgent, Title: "Revise fixes", Stage: "comment-revise", Col: 3, Row: 1},
			{ID: "verify", Kind: NodeAgent, Title: "Verify", Stage: "verify", Note: "same prompt as Ticket → PR", Col: 4},
			{ID: "push", Kind: NodeAuto, Title: "Push & reply", Note: "same PR; threads resolved", Col: 5},
		},
		Edges: []Edge{
			{From: "comments-in", To: "pick"},
			{From: "pick", To: "fix"},
			{From: "fix", To: "preview"},
			{From: "preview", To: "revise", Label: "request changes"},
			{From: "revise", To: "preview", Back: true},
			{From: "preview", To: "verify", Label: "approve"},
			{From: "verify", To: "push", Label: "green"},
		},
	},
	{
		ID: "request-changes", Title: "Request changes",
		Blurb: "Review any change before it ships - a ticket's or any branch you check out. Talk it through, send it back with feedback, then create or update the PR.",
		Nodes: []Node{
			{ID: "change", Kind: NodeStart, Title: "A change", Note: "a ticket, or any checked-out branch", Col: 0},
			{ID: "review", Kind: NodeYou, Title: "You review the change", Note: "files and diffs; @ to point", Col: 1},
			{ID: "discuss", Kind: NodeAgent, Title: "Discuss", Stage: "discuss", Note: "plan mode: answers, never edits", Col: 1, Row: 1},
			{ID: "rework", Kind: NodeAgent, Title: "Rework", Stage: "rework", Col: 2},
			{ID: "verify", Kind: NodeAgent, Title: "Verify", Stage: "verify", Note: "same prompt as Ticket → PR", Col: 3},
			{ID: "pr", Kind: NodeAuto, Title: "Create or push PR", Col: 2, Row: 1},
		},
		Edges: []Edge{
			{From: "change", To: "review"},
			{From: "review", To: "discuss", Label: "ask"},
			{From: "discuss", To: "review", Back: true},
			{From: "review", To: "rework", Label: "feedback"},
			{From: "rework", To: "verify"},
			{From: "verify", To: "review", Label: "see what moved", Back: true},
			{From: "review", To: "pr", Label: "approve"},
		},
	},
	{
		ID: "run-app", Title: "Run app",
		Blurb: "Build, install and launch the main checkout or a ticket on the device, the way Android Studio's ▶ does. An agent steps in only when a step fails.",
		Nodes: []Node{
			{ID: "target", Kind: NodeStart, Title: "Pick a target", Note: "main or a ticket", Col: 0},
			{ID: "build", Kind: NodeAuto, Title: "Build", Note: "assemble<Variant>", Col: 1},
			{ID: "install", Kind: NodeAuto, Title: "Install", Note: "old app removed first", Col: 2},
			{ID: "launch", Kind: NodeAuto, Title: "Launch", Note: "the LAUNCHER activity", Col: 3},
			{ID: "repair", Kind: NodeAgent, Title: "Repair", Stage: "run", Note: "once; what it learns is remembered", Col: 2, Row: 1},
		},
		Edges: []Edge{
			{From: "target", To: "build"},
			{From: "build", To: "install"},
			{From: "install", To: "launch"},
			{From: "build", To: "repair", Label: "a step fails"},
			{From: "repair", To: "build", Label: "retry", Back: true},
		},
	},
}
