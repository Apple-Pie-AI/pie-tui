# Apple Pie demo, review story - for phones: 103 columns (28pt, 1.3 line height) in a 1898×1642
# frame. Wide enough for the state column, large enough to read on a phone.
# Render from the repo root with `make demo`; the keystrokes live in demo/steps/review.tape.

Output demo/apple-pie-review-mobile.gif
Output demo/apple-pie-review-mobile.mp4

Require vhs

Set Shell "bash"
Set Theme "Catppuccin Mocha"
Set FontFamily "Menlo"
Set FontSize 28
Set LineHeight 1.3
Set Width 1898
Set Height 1642
Set Padding 18
Set TypingSpeed 45ms
Set CursorBlink false
# At this frame size VHS cannot capture its default rate in real time, and a
# dropped frame shortens the video - so the demo plays back too fast.
Set Framerate 20

Source demo/steps/review.tape
