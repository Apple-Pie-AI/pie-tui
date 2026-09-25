# Visual Explanation Templates for PRs

Use these templates to create visual guides for PRs. Structure depends on PR type: **Bug Fix** vs **Feature**.

---

## Template 1: BUG FIX PR

**Use this for**: Production issues, blockers, fixes to broken functionality.

**Focus**: Root cause analysis, problem explanation, how the fix restores expected behavior.

**Sections**:

### 🔍 Root Cause
- **"How the system works"** – Brief architecture/flow explanation
- **"What's the problem"** – Concrete description of the bug
- **"Two-column breakdown"** – Side-by-side visuals showing:
  - Left: What the code does (the bug)
  - Right: The result (what breaks)
- If **multiple root causes**, create separate Issue 1, Issue 2 subsections

Example: "The runner treats the worktree root as the project directory, and `git worktree add` only copies tracked files."

### The Blockers
- List each blocker that resulted from the root cause
- Example: "Blocker 1: Permissions too restrictive — agent couldn't run gradle, adb, java"
- Example: "Blocker 2: Fresh worktrees miss config files — local.properties never arrived"

### Fix 1: [Name] — [What it restores]
1. **Before/After Diagram** – Show broken state vs fixed state
2. **What changed** – Plain English explanation
3. **Code Implementation** – Actual code (8-12 lines) with numbered step comments
4. **File Context** – What the file does, when it's called, what it orchestrates
5. **Impact** – One-line outcome

*(Repeat for Fix 2, Fix 3, etc.)*

### 🎯 Summary
- 3 cards showing main outcomes of each fix

---

## Template 2: FEATURE PR

**Use this for**: New functionality, API additions, behavioral enhancements.

**Focus**: What the feature enables, how it works, architecture decisions.

**Sections**:

### 🎯 Overview
- **"What this enables"** – The capability or benefit
- **"Why it matters"** – User value or problem it solves
- **"High-level flow"** – How the feature fits into the system

### Architecture & Design
- **"How it works"** – The mechanism (not code, but the concept)
- **"Design decisions"** – Why this approach (trade-offs considered)
- **"Data flow"** – Before/after diagram showing the new flow

### Implementation Details

#### Component 1: [Name]
1. **Purpose** – What does this piece do?
2. **Code** – Key logic (8-12 lines) with numbered steps
3. **File Context** – File role, when called, dependencies
4. **Integration Points** – How it connects to other parts

*(Repeat for Components 2, 3, etc.)*

### 🎯 Summary
- User-facing benefits
- Performance characteristics (if relevant)
- Extensibility (how teams can customize/extend)

---

## When to Use Each

| PR Type | Template | Root Cause | Code Focus |
|---------|----------|-----------|-----------|
| Bug fix, blocker, production issue | **BUG FIX** | YES (detailed) | "What was broken, how the fix restores it" |
| New feature, API, enhancement | **FEATURE** | NO (skip root cause) | "How the new component works" |
| Refactor/cleanup | **BUG FIX** variant | Minimal (why refactor) | "How the new structure is cleaner" |
| Performance improvement | **BUG FIX** variant | YES (what was slow) | "What optimization technique" |

---

## HTML Structure (Both Templates)

Use this structure in your artifact:

```html
<header>
  <h1>🚀 [Feature] or 🐛 [Bug Fix Title]</h1>
  <p class="subtitle">[One-line summary]</p>
</header>

<!-- ROOT CAUSE (BUG FIX ONLY) -->
<section>
  <h2>🔍 The Root Cause</h2>
  [How system works, what's the problem, two-column issue breakdown]
</section>

<!-- THE BLOCKERS (BUG FIX ONLY) -->
<div class="problem-statement">
  <h2>The Blockers</h2>
  [List each blocker that resulted]
</div>

<!-- OVERVIEW (FEATURE ONLY) -->
<section>
  <h2>🎯 Overview</h2>
  [What it enables, why it matters, high-level flow]
</section>

<!-- ARCHITECTURE & DESIGN (FEATURE ONLY) -->
<section>
  <h2>🏗️ Architecture & Design</h2>
  [How it works, design decisions, data flow diagram]
</section>

<!-- IMPLEMENTATION DETAILS (BOTH) -->
<section>
  <h2><span class="section-number">1</span>[Fix/Component Name]</h2>
  <div class="fix-content">
    <div class="diagram">[Before/After or Data Flow]</div>
    <div class="description">
      <h3>What changed / Purpose</h3>
      <p>[Explanation]</p>
      <div class="config-example">
        <h4>Key implementation</h4>
        <div class="code-block">[8-12 lines with numbered steps]</div>
      </div>
      <div class="file-context">
        <h4>File Context</h4>
        <p>[What file does, when called, what it orchestrates]</p>
      </div>
      <div class="impact">
        <strong>⚡ Impact:</strong> [One-line outcome]
      </div>
    </div>
  </div>
</section>

<!-- SUMMARY (BOTH) -->
<section>
  <h2>🎯 Summary</h2>
  [3 cards with key outcomes]
</section>
```

---

## Code Block Format

**Key Rule**: Show 8-12 lines of actual code with **numbered step comments**.

```html
<div class="code-block">
<span class="keyword">func</span> FunctionName(...) {
    <span class="comment">// 1. First key step — explain what happens</span>
    ...code...

    <span class="comment">// 2. Second key step — explain why this matters</span>
    ...code...

    <span class="comment">// 3. Safety check or important detail</span>
    ...code...
}
</div>
```

**Span classes**:
- `<span class="keyword">` – Go keywords, function names
- `<span class="string">` – String literals
- `<span class="comment">` – Explanation comments (numbered 1, 2, 3...)

---

## Diagrams

### For Bug Fix: Before/After
```
❌ Before                          ✅ After
worktree/                          worktree/
├── build.gradle                   ├── build.gradle
├── src/                           ├── src/
└── ❌ local.properties            └── ✅ local.properties
    (never copied)                     (seeded from main)
```

### For Feature: Data Flow
```
Old Flow                           New Flow
Agent                              Agent
  ↓                                  ↓
Run command                        Run command (with validation)
  ↓                                  ↓
Execute                            Execute
  ↓                                  ↓
Report result                      Report result
                                   + Metrics collected
```

---

## File Context Template

**For Bug Fix** (explaining what was broken):
> **`internal/git/seed.go`** is called by the runner before the agent starts. It copies machine-local config (like `local.properties`) from the main checkout into fresh worktrees. The logic is scoped: ignored files only (not untracked WIP), with build-dir exclusions for performance, and idempotent safety (never overwrites).

**For Feature** (explaining what's new):
> **`internal/config/permissions.go`** (new file) manages the three-layer permission model. Layer 1 is the user's own Claude Code settings, Layer 2 is Apple Pie's baseline allowlist, Layer 3 is team-specific tools via `extra_allowed_tools`. It's called at agent spawn time to compute the effective allowlist.

---

## Writing Tips

### Bug Fix PRs
- ✅ Start with "Two production blockers with one shared root cause"
- ✅ Make the root cause **crystal clear** with examples
- ✅ Show what was silently broken (invisible failures)
- ✅ Explain each fix as "restores X behavior" not "adds X feature"
- ❌ Don't skip the root cause analysis
- ❌ Don't make it sound like a feature

### Feature PRs
- ✅ Start with "This enables..." or "Now you can..."
- ✅ Explain the capability first, implementation second
- ✅ Show data flow / architecture before code
- ✅ Explain trade-offs and why this approach
- ❌ Don't lead with root cause (unless a limitation)
- ❌ Don't make it sound like a bug fix

### Both
- ✅ Be specific: "copies git-ignored files from main checkout" not "syncs config"
- ✅ Number code comments: "// 1. Step name" then "// 2. Next step"
- ✅ Keep code blocks to 8-12 lines max
- ✅ Explain what each file does in the system
- ✅ Always explain when/where the code is called
- ❌ Don't use analogies to other systems
- ❌ Don't assume Go knowledge
- ❌ Don't show every line of code

---

## Checklist Before Publishing

### Bug Fix
- [ ] Root cause section clearly explains what broke and why
- [ ] All blockers are listed and explained
- [ ] Each fix has a before/after diagram
- [ ] Code shows actual implementation (not pseudocode)
- [ ] File context explains the file's role and when it's called
- [ ] No analogies to other systems
- [ ] Artifact link added to top of PR description

### Feature
- [ ] Overview section explains what it enables (user value first)
- [ ] Architecture/design section covers the approach
- [ ] Data flow diagram shows the new mechanism
- [ ] Each component has code + file context + integration points
- [ ] Code shows actual implementation
- [ ] No unnecessary root cause details
- [ ] Artifact link added to top of PR description

### Both
- [ ] Supports light and dark themes (uses CSS variables)
- [ ] Responsive on mobile (diagrams stack vertically)
- [ ] All colors use CSS var() tokens
- [ ] Code blocks have numbered step comments
- [ ] File paths and technical terms are accurate
- [ ] Tested in both light and dark mode

---

## Template Repository

**For future PRs:**
1. Note whether it's a BUG FIX or FEATURE
2. Follow the appropriate template structure above
3. Use the HTML structure as a guide
4. Reference the artifact link format
5. Add link to top of PR description with: `## 📊 Visual Explanation [LINK]`

**Reference PR #1** (bug fix example): https://github.com/Apple-Pie-AI/pie-tui/pull/1
