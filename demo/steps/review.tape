# The review story's keystrokes: PR comments → the agent fixes them → FIXES READY.
# Sourced by demo/apple-pie-review.tape (desktop) and demo/apple-pie-review-mobile.tape.
#
# `pie --demo=review` runs the real hub on fixture tickets in a throwaway ~/.pie
# (internal/tui/demo_review.go). AND-207's PR opens with three review comments;
# choosing "Fix" runs a scripted fix that edits its worktree for real, and it
# parks at "fixes ready" 5.6s later. The sleeps below are tuned to that.

# Put the freshly built binary first on PATH, and skip the first-run splash.
Hide
Type "export PATH=$PWD:$PATH PIE_NO_SPLASH=1 PS1='$ ' && clear" Enter
Sleep 300ms
Show

Type "pie --demo=review" Sleep 500ms Enter

# AND-207's PR is back with three review comments: NEEDS YOU.
Sleep 2s
Down Sleep 250ms Down Sleep 250ms Down Sleep 250ms Down
Sleep 2s

# "Address PR feedback (3)" leads the menu.
Enter
Sleep 1.2s
Enter
Sleep 1.5s

# Read each comment: the code it points at shows beside it.
Down
Sleep 2s
Down
Sleep 2s
Down
Sleep 2s

# All three are checked: fix them. Back on the dashboard, AND-207 runs,
# then parks under NEEDS YOU as "fixes ready" (5.6s) - hold on that.
Down
Sleep 1.5s
Enter
Sleep 8.5s

# FIXES READY: open the fixes - the diff and the reply drafted for each comment.
Enter
Sleep 1.2s
Enter
Sleep 5s

Hide
Type "q"
Sleep 500ms
