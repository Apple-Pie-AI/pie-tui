---
name: pull-request-creation
description: Create a visual HTML explanation artifact for a GitHub PR (bug fix or feature) with before/after diagrams, numbered code snippets, and file context, then link it from the PR description. Use when asked to explain a PR visually, e.g. /pull-request-creation 7 feature.
---

# pull-request-creation


Create visual explanations for PRs (bug fixes or features) with proper structure, code snippets, and architecture context.

## Usage

```
/pull-request-creation [PR_NUMBER] [TYPE] [DESCRIPTION]
```

- `PR_NUMBER`: GitHub PR number (e.g., `1`, `7`, `15`)
- `TYPE`: `bug-fix` or `feature`
- `DESCRIPTION`: Brief description of what to focus on (optional)

## Examples

```
/pull-request-creation 1 bug-fix
/pull-request-creation 5 feature This enables custom permission allowlists
/pull-request-creation 8 bug-fix Production blocker in monorepo builds
```

## What This Skill Does

1. **Fetches the PR** from GitHub
2. **Reads the implementation files** mentioned in the PR
3. **Creates a visual explanation** with:
   - Proper structure based on bug fix or feature type
   - Before/after diagrams or data flow diagrams
   - Actual code snippets (8-12 lines) with numbered step comments
   - File context explaining each file's role
   - Dark mode support and responsive design
4. **Publishes as an artifact** and adds link to PR description

## Template Structures

### Bug Fix PRs
Focus on root cause analysis and problem explanation.

**Sections**:
1. 🔍 Root Cause
   - How the system works
   - What's the problem
   - Two-column issue breakdown
2. The Blockers (each issue that resulted)
3. Fix 1, Fix 2, Fix 3 (etc.)
   - Before/After diagram
   - What changed
   - Code (8-12 lines, numbered steps)
   - File context
   - Impact
4. 🎯 Summary (3 cards with outcomes)

### Feature PRs
Focus on capability and architecture.

**Sections**:
1. 🎯 Overview
   - What this enables
   - Why it matters
   - High-level flow
2. 🏗️ Architecture & Design
   - How it works
   - Design decisions
   - Data flow diagram
3. Implementation Details
   - Component 1, 2, 3 (etc.)
   - Purpose
   - Code (8-12 lines, numbered steps)
   - File context
   - Integration points
4. 🎯 Summary

## Code Snippet Guidelines

**Extract the most important 8-12 lines** with numbered comments:

```go
// 1. First key step — explain what happens
args := []string{"ls-files", "--others", "--ignored"}

// 2. Second key step — why this matters
for _, d := range seedExcludedDirs {
    args = append(args, ":!**/"+d+"/**")
}

// 3. Safety check
if fi.Size() > seedMaxFileBytes {
    st.SkippedLarge++
    continue
}
```

**Do**:
- ✅ Show actual code from the PR (not pseudocode)
- ✅ Number each major step with comments
- ✅ Include safety checks and important details
- ✅ Use syntax highlighting: `<span class="keyword">`, `<span class="comment">`, `<span class="string">`

**Don't**:
- ❌ Show every line
- ❌ Use generic code examples
- ❌ Skip the numbered step comments

## File Context Requirements

For each file mentioned, explain:

1. **What it does** – Purpose in the system
2. **When it's called** – In which pipeline stage or code path
3. **What it orchestrates** – What other files it depends on or calls
4. **Why it was added/changed** – Context for this PR

**Example**:
> `internal/git/seed.go` is called by the runner before the agent starts. It copies machine-local config (like `local.properties`) from the main checkout into fresh worktrees. The logic is scoped: ignored files only (not untracked WIP), with build-dir exclusions for performance, and idempotent safety (never overwrites).

## Output

1. **Visual Artifact** – Self-contained HTML with:
   - Proper color scheme (dark mode support)
   - Responsive design (mobile-friendly)
   - Before/after diagrams or data flow
   - Code with numbered comments
   - File context boxes
   
2. **PR Description Update** – Adds link at top:
   ```markdown
   ## 📊 Visual Explanation
   
   See a comprehensive visual guide to these changes: [ARTIFACT_URL]
   ```

## Remember

- **No analogies** to other systems (Android Studio, IDE, etc.)
- **Be specific**: "copies git-ignored files from main checkout" not "syncs config"
- **Assume no Go knowledge** but do assume software development knowledge
- **All colors use CSS variables** for automatic light/dark mode support
- **Responsive**: Test on mobile (diagrams should stack vertically)

## Template Reference

Full templates documented at:
`docs/VISUAL_EXPLANATION_TEMPLATES.md`

## After Running This Skill

- [ ] Visual explanation created and published
- [ ] Artifact link added to PR description
- [ ] Both bug-fix and feature structures supported
- [ ] Code snippets use numbered step comments
- [ ] File context explains the role and pipeline
- [ ] No analogies to other systems used
- [ ] Tested in light and dark mode
- [ ] Responsive on mobile

## Related Files

- **Template guide**: `docs/VISUAL_EXPLANATION_TEMPLATES.md`
- **Example PR #1**: https://github.com/Apple-Pie-AI/pie-tui/pull/1
- **Example artifact**: https://claude.ai/code/artifact/cf4f2599-b344-43f4-81fd-1bf1b938bad1
