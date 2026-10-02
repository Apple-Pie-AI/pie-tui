# Apple Pie demo, review story - for phones: 4:5 portrait at 28pt, so the text reads on a small screen.
# Render from the repo root with `make demo`; the keystrokes live in demo/steps/review.tape.

Output demo/apple-pie-review-mobile.gif
Output demo/apple-pie-review-mobile.mp4

Require vhs

Set Shell "bash"
Set Theme "Catppuccin Mocha"
Set FontFamily "Menlo"
Set FontSize 28
Set Width 1080
Set Height 1350
Set Padding 32
Set TypingSpeed 45ms
Set CursorBlink false

Source demo/steps/review.tape
