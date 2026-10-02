# The pipeline story's keystrokes: ticket → NEEDS YOU → PR READY FOR REVIEW.
# Sourced by demo/apple-pie.tape (desktop) and demo/apple-pie-mobile.tape.
#
# `pie --demo` runs the real hub on fixture tickets in a throwaway ~/.pie, on a
# fixed timeline (internal/tui/demo_pipeline.go): AND-219 asks its question 3s
# in, AND-214 opens its PR at 16s, and AND-219 ships ~7s after it is answered.
# The sleeps below are tuned to that timeline - change one, re-check the other.

# Put the freshly built binary first on PATH, and skip the first-run splash.
Hide
Type "export PATH=$PWD:$PATH PIE_NO_SPLASH=1 PS1='$ ' && clear" Enter
Sleep 300ms
Show

Type "pie --demo" Sleep 500ms Enter

# Three agents at work, one PR already up. AND-219 hits a question at 3s.
Sleep 4s

# Walk past the four command rows to the NEEDS YOU ticket and open its menu.
Down Sleep 250ms Down Sleep 250ms Down Sleep 250ms Down
Sleep 1.2s
Enter
Sleep 1.2s

# "Answer the questions" leads the menu.
Enter
Sleep 600ms
Type "Expire after 24h, but keep serving the stale feed while offline."
Sleep 600ms
Enter

# AND-219 resumes: plan → implement → build → test on the emulator → PR.
# AND-214's PR lands along the way.
Sleep 8s

# Close on AND-219's row: the PR link and the emulator-verified log.
Down
Sleep 4s

Hide
Type "q"
Sleep 500ms
