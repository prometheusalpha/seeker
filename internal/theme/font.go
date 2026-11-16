package theme

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// Font is the typeface of the two places that show code: the SQL editor and
// the result grid. Size and Features are shared, as one setting of a
// terminal's would be.
type Font struct {
	Editor string
	Table  string
	Size   float64
	// Features is ghostty's font-feature value: tags separated by commas,
	// "-tag" to switch one off, "tag=n" to set it.
	Features string
}

// DefaultFamily is the family a fresh install asks the system for; text
// falls back to a system font where it is not installed.
const DefaultFamily = "JetBrainsMono Nerd Font"

func DefaultFont() Font {
	return Font{Editor: DefaultFamily, Table: DefaultFamily, Size: 14}
}

// EditorFont is what the editor's State takes; a blank family leaves the
// system's monospaced font.
func (f Font) EditorFont() ui.Font {
	return ui.Font{Family: strings.TrimSpace(f.Editor), Size: float32(f.Size), Features: ParseFeatures(f.Features)}
}

// ParseFeatures turns what ghostty's font-feature takes ("ss01, -liga,
// cv09=2") into what the text engine does ("ss01,liga=0,cv09=2"). Blank
// entries drop, and so do the quotes ghostty allows around a tag.
func ParseFeatures(s string) string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		f = strings.Trim(f, `"'`)
		switch {
		case f == "":
		case strings.HasPrefix(f, "-"):
			if tag := strings.TrimPrefix(f, "-"); tag != "" {
				out = append(out, tag+"=0")
			}
		default:
			out = append(out, f)
		}
	}
	return strings.Join(out, ",")
}
