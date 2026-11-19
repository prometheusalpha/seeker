package theme

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// flavor is a Catppuccin flavor, from github.com/catppuccin/palette
// v1.8.0 palette.json.
type flavor struct {
	dark                         bool
	accent                       map[string]string
	text, subtext1, subtext0     string
	overlay2, overlay1, overlay0 string
	surface2, surface1, surface0 string
	base, mantle, crust          string
}

// Accents are Catppuccin's, in the palette's order.
var Accents = []string{
	"Rosewater", "Flamingo", "Pink", "Mauve", "Red", "Maroon", "Peach",
	"Yellow", "Green", "Teal", "Sky", "Sapphire", "Blue", "Lavender",
}

var flavors = map[string]flavor{
	"Catppuccin Latte": {
		accent: map[string]string{
			"Rosewater": "#dc8a78", "Flamingo": "#dd7878", "Pink": "#ea76cb", "Mauve": "#8839ef",
			"Red": "#d20f39", "Maroon": "#e64553", "Peach": "#fe640b", "Yellow": "#df8e1d",
			"Green": "#40a02b", "Teal": "#179299", "Sky": "#04a5e5", "Sapphire": "#209fb5",
			"Blue": "#1e66f5", "Lavender": "#7287fd",
		},
		text: "#4c4f69", subtext1: "#5c5f77", subtext0: "#6c6f85",
		overlay2: "#7c7f93", overlay1: "#8c8fa1", overlay0: "#9ca0b0",
		surface2: "#acb0be", surface1: "#bcc0cc", surface0: "#ccd0da",
		base: "#eff1f5", mantle: "#e6e9ef", crust: "#dce0e8",
	},
	"Catppuccin Frappé": {
		dark: true,
		accent: map[string]string{
			"Rosewater": "#f2d5cf", "Flamingo": "#eebebe", "Pink": "#f4b8e4", "Mauve": "#ca9ee6",
			"Red": "#e78284", "Maroon": "#ea999c", "Peach": "#ef9f76", "Yellow": "#e5c890",
			"Green": "#a6d189", "Teal": "#81c8be", "Sky": "#99d1db", "Sapphire": "#85c1dc",
			"Blue": "#8caaee", "Lavender": "#babbf1",
		},
		text: "#c6d0f5", subtext1: "#b5bfe2", subtext0: "#a5adce",
		overlay2: "#949cbb", overlay1: "#838ba7", overlay0: "#737994",
		surface2: "#626880", surface1: "#51576d", surface0: "#414559",
		base: "#303446", mantle: "#292c3c", crust: "#232634",
	},
	"Catppuccin Macchiato": {
		dark: true,
		accent: map[string]string{
			"Rosewater": "#f4dbd6", "Flamingo": "#f0c6c6", "Pink": "#f5bde6", "Mauve": "#c6a0f6",
			"Red": "#ed8796", "Maroon": "#ee99a0", "Peach": "#f5a97f", "Yellow": "#eed49f",
			"Green": "#a6da95", "Teal": "#8bd5ca", "Sky": "#91d7e3", "Sapphire": "#7dc4e4",
			"Blue": "#8aadf4", "Lavender": "#b7bdf8",
		},
		text: "#cad3f5", subtext1: "#b8c0e0", subtext0: "#a5adcb",
		overlay2: "#939ab7", overlay1: "#8087a2", overlay0: "#6e738d",
		surface2: "#5b6078", surface1: "#494d64", surface0: "#363a4f",
		base: "#24273a", mantle: "#1e2030", crust: "#181926",
	},
	"Catppuccin Mocha": {
		dark: true,
		accent: map[string]string{
			"Rosewater": "#f5e0dc", "Flamingo": "#f2cdcd", "Pink": "#f5c2e7", "Mauve": "#cba6f7",
			"Red": "#f38ba8", "Maroon": "#eba0ac", "Peach": "#fab387", "Yellow": "#f9e2af",
			"Green": "#a6e3a1", "Teal": "#94e2d5", "Sky": "#89dceb", "Sapphire": "#74c7ec",
			"Blue": "#89b4fa", "Lavender": "#b4befe",
		},
		text: "#cdd6f4", subtext1: "#bac2de", subtext0: "#a6adc8",
		overlay2: "#9399b2", overlay1: "#7f849c", overlay0: "#6c7086",
		surface2: "#585b70", surface1: "#45475a", surface0: "#313244",
		base: "#1e1e2e", mantle: "#181825", crust: "#11111b",
	},
}

// catppuccin maps a flavor onto the window's theme following Catppuccin's
// style guide: base for panes, crust behind them, text on an accent in
// crust, selections in overlay2 at a quarter.
func catppuccin(f flavor, accentName string) Resolved {
	hex := ui.Hex
	acc, ok := f.accent[accentName]
	if !ok {
		acc = f.accent["Blue"]
	}
	ac := func(name string) ui.Color { return hex(f.accent[name]) }

	t := *ui.LightTheme()
	if f.dark {
		t = *ui.DarkTheme()
	}
	t = *t.WithAccent(hex(acc))
	t.Background = hex(f.base)
	t.Surface = hex(f.surface0)
	t.SurfaceHover = hex(f.surface1)
	t.SurfacePressed = hex(f.surface2)
	t.Border = hex(f.surface1)
	t.Text = hex(f.text)
	t.TextMuted = hex(f.subtext0)
	t.AccentText = hex(f.crust)
	t.Danger = ac("Red")
	t.Warning = ac("Yellow")
	t.Success = ac("Green")
	t.Selection = hex(f.overlay2).Alpha(0.25)
	t.Scrollbar = hex(f.overlay0).Alpha(0.6)

	isl := Islands{
		Canvas: hex(f.crust),
		Island: hex(f.base),
		Edge:   hex(f.surface0).Alpha(0.7),
		Active: hex(f.surface0),
	}

	code := &editor.Theme{
		Background:        hex(f.base),
		Text:              hex(f.text),
		LineNumber:        hex(f.overlay1),
		ActiveLineNumber:  ac("Lavender"),
		ActiveLine:        hex(f.text).Alpha(0.06),
		Selection:         hex(f.overlay2).Alpha(0.3),
		InactiveSelection: hex(f.overlay2).Alpha(0.18),
		Cursor:            ac("Rosewater"),
		MatchingBracket:   hex(f.overlay2).Alpha(0.5),
		Match:             ac("Yellow").Alpha(0.25),
		CurrentMatch:      ac("Peach").Alpha(0.45),
		Occurrence:        hex(f.overlay2).Alpha(0.18),
		IndentGuide:       hex(f.surface1).Alpha(0.6),
		ActiveIndentGuide: hex(f.surface2),
		WrapGuide:         hex(f.surface1).Alpha(0.6),
		Whitespace:        hex(f.overlay0).Alpha(0.5),
		Error:             ac("Red"),
		Warning:           ac("Yellow"),
		Info:              ac("Sky"),
		Hint:              ac("Teal"),
		Added:             ac("Green"),
		Deleted:           ac("Red"),
		Modified:          ac("Blue"),
	}
	tok := func(k editor.TokenKind, c ui.Color) { code.Tokens[k].Color = c }
	tok(editor.TokenKeyword, ac("Mauve"))
	tok(editor.TokenOperator, ac("Sky"))
	tok(editor.TokenType, ac("Yellow"))
	tok(editor.TokenFunction, ac("Blue"))
	tok(editor.TokenMeta, ac("Pink"))
	tok(editor.TokenProperty, ac("Lavender"))
	tok(editor.TokenConstant, ac("Peach"))
	tok(editor.TokenNumber, ac("Peach"))
	tok(editor.TokenAttribute, ac("Yellow"))
	tok(editor.TokenString, ac("Green"))
	tok(editor.TokenEscape, ac("Pink"))
	tok(editor.TokenRegexp, ac("Pink"))
	tok(editor.TokenTag, ac("Blue"))
	tok(editor.TokenComment, hex(f.overlay2))
	tok(editor.TokenHeading, ac("Red"))
	tok(editor.TokenLink, ac("Blue"))
	tok(editor.TokenCode, ac("Green"))
	tok(editor.TokenInserted, ac("Green"))
	tok(editor.TokenDeleted, ac("Red"))
	tok(editor.TokenInvalid, ac("Red"))
	code.Tokens[editor.TokenComment].Italic = true
	code.Tokens[editor.TokenHeading].Bold = true
	code.Tokens[editor.TokenStrong].Bold = true
	code.Tokens[editor.TokenEmphasis].Italic = true

	types := Types{
		Integer: ac("Blue"), Numeric: ac("Sapphire"), Boolean: ac("Peach"), Temporal: ac("Mauve"),
		Structured: ac("Pink"), Identifier: ac("Yellow"), Binary: ac("Red"), Spatial: ac("Teal"),
	}

	return Resolved{UI: t, Islands: isl, Types: types, Code: code}
}
