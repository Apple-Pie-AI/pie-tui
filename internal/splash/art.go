package splash

// The title is drawn in the wordmark's own hand: the chunky, round, heavy
// lowercase of the Apple Pie logo, built out of block runes rather than the
// slash-and-pipe of an old figlet banner. Glyphs carry their own natural width
// (plus a trailing column of air for letter spacing) and every glyph is the same
// height, which is all the renderer needs to reveal the word letter by letter and
// sweep the gloss across it.
//
// The grid is seven rows deep: two for the ascender of the l and the cap P, four
// for the x-height where the round lowercase lives, and one below the baseline
// for the descenders of the two p's.
const glyphH = 7

var glyphs = map[rune][]string{
	'a': {
		"        ",
		"        ",
		" ▄████▄ ",
		" ▄▄▄▄██ ",
		"██   ██ ",
		" ▀███▀▀ ",
		"        ",
	},
	'p': {
		"        ",
		"        ",
		"█▀███▄  ",
		"██   ██ ",
		"██   ██ ",
		"█▄███▀  ",
		"██      ",
	},
	'l': {
		"██  ",
		"██  ",
		"██  ",
		"██  ",
		"██  ",
		"██  ",
		"    ",
	},
	'e': {
		"        ",
		"        ",
		" ▄███▄  ",
		"██▄▄▄██ ",
		"██▀▀▀▀▀ ",
		" ▀███▀  ",
		"        ",
	},
	'P': {
		"█████▄  ",
		"██   ██ ",
		"██   ██ ",
		"█████▀  ",
		"██      ",
		"██      ",
		"        ",
	},
	'i': {
		"██  ",
		"    ",
		"██  ",
		"██  ",
		"██  ",
		"██  ",
		"    ",
	},
	' ': {
		"    ",
		"    ",
		"    ",
		"    ",
		"    ",
		"    ",
		"    ",
	},
}

// title is the word drawn at the top of the screen, set the way the logo sets
// it: one word, lowercase but for the P. Every rune needs a glyph.
const title = "applePie"

// titleSplit is where the logo changes ink - the rune index of the P that opens
// the orange-red half.
const titleSplit = 5 // len("apple")

// The wordmark's two inks, straight off the logo, each with a lit tint. The lit
// tint is what the gloss band paints as it travels across the word, so the
// lettering catches the light without ever leaving its own color.
const (
	appleInk   = "#6B7A3C" // olive green - "apple"
	appleGloss = "#9CAE5C"
	pieInk     = "#D4562A" // orange-red - "Pie"
	pieGloss   = "#F08040"
)

const (
	crustColor   = "#D9A05B" // baked lattice crust
	fillingColor = "#E0913A" // apple filling
	chunkColor   = "#A85A22" // apple chunks in the filling
	steamColor   = "#7E7A70"
	promptColor  = "#F0A32E"
	hintColor    = "#6C6459"
	dimColor     = "#4A463F"
)
