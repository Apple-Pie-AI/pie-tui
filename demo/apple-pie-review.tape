# Apple Pie demo, review story - for README and landscape video (1800×900, 16pt).
# Render from the repo root with `make demo`; the keystrokes live in demo/steps/review.tape.

Output demo/apple-pie-review.gif
Output demo/apple-pie-review.mp4

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

Source demo/steps/review.tape
