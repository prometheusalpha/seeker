package grid

import "math"

// Auto-width arithmetic for the result grid columns. The grid renders in a
// monospace face, so widths come from one advance width and a character
// count, never per-cell measurement. Recall measured the advance with a
// canvas; here the caller passes it (CharWidth over its own measure
// function), or GridCharWidth stands in.

// GridCharWidth is the monospace advance in px when nothing was measured.
const GridCharWidth = 8.0

// GridCellPadding is the horizontal chrome around the text: 0.5rem of
// padding each side plus the 1px cell border, which is 17px, plus 13px of
// slack. The slack is not decoration: the width is arithmetic on a measured
// advance while the toolkit lays the same text out with its own sub-pixel
// advances and rounds down by up to 0.5px, so a column sized to the exact 17
// ellipsizes its last character. Carried over from the browser; recheck it
// against ui.Table's real cell padding.
const GridCellPadding = 40.0

const (
	GridMinAutoWidth = 40   // narrowest a column may auto-size to
	GridMaxAutoWidth = 900  // widest
	GridSampleRows   = 10   // rows sampled per column
	valueTextLimit   = 60   // runes past this are cut before measuring
	charProbeLength  = 100  // long enough that sub-pixel advances average out
	wideCodePoint    = 0x7e // code points above `~` (CJK, full-width, emoji) render about twice as wide
)

// valueTextLimit exists because one 10 000-character cell must not blow the
// column out to the max width.

// EstimateTextWidth is the estimated pixel width of text in a monospace
// face: full-width code points count as two units, everything else as one.
func EstimateTextWidth(text string, charWidth float64) float64 {
	units := 0
	for _, r := range text {
		if r > wideCodePoint {
			units += 2
		} else {
			units++
		}
	}
	return float64(units) * charWidth
}

// SampleColumnValues is the head-and-tail sample of one column, rendered to
// the strings the cells show. Sampling both ends lets a column grow when a
// later page holds wider values, and a short row reads as empty.
//
// columnType must be the one the grid paints with, or an instant measured as
// UTC gets a width sized for its +07 rendering.
func SampleColumnValues(rows [][]any, index int, columnType string, limit int) []string {
	text := func(row []any) string {
		var v any
		if index >= 0 && index < len(row) {
			v = row[index]
		}
		return FormatCell(v, columnType)
	}
	total := max(1, limit)
	out := make([]string, 0, min(len(rows), total))
	if len(rows) <= total {
		for _, r := range rows {
			out = append(out, text(r))
		}
		return out
	}
	head := (total + 1) / 2
	tail := total - head
	for i := 0; i < head; i++ {
		out = append(out, text(rows[i]))
	}
	for i := len(rows) - tail; i < len(rows); i++ {
		out = append(out, text(rows[i]))
	}
	return out
}

// CharWidth is the advance of the grid's monospace face, from a measure
// function that returns the width in px of a text. A nil function, or one
// returning a non-positive or non-finite width, gives GridCharWidth.
func CharWidth(measure func(string) float64) float64 {
	if measure == nil {
		return GridCharWidth
	}
	probe := make([]byte, charProbeLength)
	for i := range probe {
		probe[i] = '0'
	}
	w := measure(string(probe))
	if math.IsNaN(w) || math.IsInf(w, 0) || w <= 0 {
		return GridCharWidth
	}
	return w / charProbeLength
}

// ColumnWidthInput: Min and Max are zero for the defaults.
type ColumnWidthInput struct {
	Header    string
	Values    []string
	CharWidth float64
	Min, Max  int
}

// AutoColumnWidth is the final column width: the wider of the header and its
// sampled values, capped at Max and floored at Min so an empty column still
// stays clickable.
func AutoColumnWidth(in ColumnWidthInput) int {
	lo, hi := in.Min, in.Max
	if lo == 0 {
		lo = GridMinAutoWidth
	}
	if hi == 0 {
		hi = GridMaxAutoWidth
	}
	cw := in.CharWidth
	if cw <= 0 {
		cw = GridCharWidth
	}
	widest := EstimateTextWidth(in.Header, cw) + GridCellPadding
	valueWidth := GridCellPadding
	for _, v := range in.Values {
		if w := EstimateTextWidth(truncateRunes(v, valueTextLimit), cw) + GridCellPadding; w > valueWidth {
			valueWidth = w
		}
	}
	widest = max(widest, valueWidth)
	// Math.round: halves go up.
	return max(lo, min(hi, int(math.Floor(widest+0.5))))
}

func truncateRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
