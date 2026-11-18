package view

import (
	"github.com/egoist/mygo/ui"
)

// marked is text with the byte ranges in hits painted over, as a search
// result shows what it matched. Ranges are ascending and may touch.
func marked(c *ui.Context, text string, hits [][2]int, ink, mark ui.Color) ui.Element {
	var spans []ui.Span
	at := 0
	for _, h := range hits {
		from, to := min(max(h[0], at), len(text)), min(max(h[1], at), len(text))
		if from == to {
			continue
		}
		if from > at {
			spans = append(spans, ui.Span{Text: text[at:from], Color: ink})
		}
		spans = append(spans, ui.Span{Text: text[from:to], Color: ink, Weight: 700, Background: mark, Radius: 2})
		at = to
	}
	if at < len(text) {
		spans = append(spans, ui.Span{Text: text[at:], Color: ink})
	}
	return ui.RichText(c, spans...)
}

// runeRanges turns matched rune offsets into the byte ranges of text,
// joining neighbours so a run of letters is one highlight.
func runeRanges(text string, runes []int) [][2]int {
	var out [][2]int
	want, n := 0, 0
	for b := range text {
		if want < len(runes) && runes[want] == n {
			if k := len(out); k > 0 && out[k-1][1] == b {
				out[k-1][1] = b + runeLen(text[b:])
			} else {
				out = append(out, [2]int{b, b + runeLen(text[b:])})
			}
			want++
		}
		n++
	}
	return out
}

func runeLen(s string) int {
	for i := range s {
		if i > 0 {
			return i
		}
	}
	return len(s)
}

// matchMark is the wash behind a match: the accent on a plain row, and the
// text's own colour on the chosen one, whose background is the accent.
func matchMark(t *ui.Theme, chosen bool) ui.Color {
	if chosen {
		return t.AccentText.Alpha(0.3)
	}
	return t.Accent.Alpha(0.28)
}
