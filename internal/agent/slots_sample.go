package agent

// Sample renders a stage's prompt for a made-up ticket, through the same
// builder a real run uses - the workflow editor's preview. Only the ticket,
// plan and diff data differ from what an agent receives. "" for an unknown stage.
func (p Prompts) Sample(stage string) string {
	const key, summary = "PROJ-42", "Show a retry button when the feed fails to load"
	desc := "When the home feed request fails, users see a blank screen. Show an error state with a Retry button."
	plan := &Plan{Plan: "1. Add an Error state to FeedUiState.\n2. Render ErrorView with a Retry button in FeedScreen.\n3. Cover FeedViewModel's retry in a unit test."}
	switch stage {
	case "plan":
		return p.Plan(key, summary, desc, PlanTools)
	case "implement":
		return p.Impl(key, summary, desc, plan, "", "")
	case "self-review":
		return p.Review(key, summary, plan)
	case "verify":
		return p.Verify(key, summary, "/work/.pie/worktrees/PROJ-42", "emulator-5554", true, "", "", "")
	case "comments":
		return p.CommentFix(key, summary, "main", "", []ReviewComment{{ID: "2219", Author: "reviewer",
			Path: "app/src/main/java/feed/FeedScreen.kt", Line: 88, Body: "Could the retry button reuse our PrimaryButton?",
			DiffHunk: "@@ -85,3 +85,6 @@\n+    Button(onClick = onRetry) {\n+        Text(\"Retry\")\n+    }"}})
	case "review-fix":
		return BuildReviewFixPrompt([]string{"FeedViewModel.retry() is never covered by a test.", "ErrorView hardcodes \"Retry\" instead of using a string resource."})
	case "comment-revise":
		return BuildCommentRevisePrompt("Keep PrimaryButton, but pass the existing retry icon to it.")
	case "discuss":
		return BuildChangeDiscussPrompt("Why did you put the retry state in the ViewModel and not the repository?")
	case "rework":
		return p.Rework("Rename @app/src/main/java/feed/FeedScreen.kt:91 onRetry to onRetryClick.", 1)
	case "run":
		return p.RunRepair(RunRepairInput{ProjectDir: "/work/acme-android", Serial: "emulator-5554", Module: ":app",
			Variant: "debug", RecipeJSON: `{"module":":app","variant":"debug"}`, Failure: RunFailure{Step: "build",
				Argv: []string{"./gradlew", ":app:assembleDebug"}, ExitCode: 1, Tail: "Could not resolve com.acme:design-system:4.2.0"}})
	}
	return ""
}
