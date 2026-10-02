# Apple Pie launch demo: ticket → NEEDS YOU → PR READY FOR REVIEW.
#
# Render from the repo root:   make demo
# `pie --demo` runs the real hub on fixture tickets in a throwaway ~/.pie, on a
# fixed timeline (internal/tui/demo.go): AND-219 asks its question 3s in,
# AND-214 opens its PR at 16s, and AND-219 ships ~7s after it is answered.
# The sleeps below are tuned to that timeline - change one, re-check the other.

Output demo/apple-pie.gif
Output demo/apple-pie.mp4

Require vhs

Set Shell "bash"
Set Theme "Catppuccin Mocha"
Set FontFamily "Menlo"
Set FontSize 16
Set Width 1800
Set Height 900
Set Padding 24
Set TypingSpeed 45ms
Set CursorBlink false

# Put the freshly built binary first on PATH, and skip the first-run splash.
Hide
Type "export PATH=$PWD:$PATH PIE_NO_SPLASH=1 PS1='$ ' && clear" Enter
Sleep 300ms
Show

Type "pie --demo" Sleep 500ms Enter

# Three agents at work, one PR already up. AND-219 hits a question at 3s.
Sleep 4s
Screenshot demo/frames/01-needs-you.png

# Walk past the four command rows to the NEEDS YOU ticket and open its menu.
Down Sleep 250ms Down Sleep 250ms Down Sleep 250ms Down
Sleep 1.2s
Enter
Sleep 1.2s
Screenshot demo/frames/02-menu.png

# "Answer the questions" leads the menu.
Enter
Sleep 600ms
Type "Expire after 24h, but keep serving the stale feed while offline."
Sleep 600ms
Screenshot demo/frames/03-answer.png
Enter

# AND-219 resumes: plan → implement → build → test on the emulator → PR.
# AND-214's PR lands along the way.
Sleep 4s
Screenshot demo/frames/04-resumed.png
Sleep 4s

# Close on AND-219's row: the PR link and the emulator-verified log.
Down
Sleep 2.5s
Screenshot demo/frames/05-pr-ready.png
Sleep 1.5s

Hide
Type "q"
Sleep 500ms
