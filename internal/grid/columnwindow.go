package grid

// Horizontal windowing for the result grid: which columns a row renders. Only
// the columns overlapping the viewport (plus a buffer) are rendered; one
// spacer stands in for the columns scrolled off to the left. The row keeps
// its full width, so the scroll range and the right side need no spacer.
// Widths and offsets are px.

// ColumnWindow: columns [Start, End) are rendered; Before is the spacer left
// of them.
type ColumnWindow struct {
	Start, End, Before int
}

// ColumnOffsets: entry i is column i's left edge; one extra entry holds the
// total.
func ColumnOffsets(widths []int) []int {
	offsets := make([]int, 1, len(widths)+1)
	total := 0
	for _, w := range widths {
		total += w
		offsets = append(offsets, total)
	}
	return offsets
}

type ColumnWindowInput struct {
	Offsets       []int
	ScrollLeft    int
	ViewportWidth int
	Gutter        int // the sticky gutter covers this much of the viewport's left edge
	Buffer        int // extra px rendered on each side, so a short scroll needs no re-render
}

// WindowOf: offsets are in column coordinates (0 = right edge of the
// gutter). The gutter is sticky, so the first visible column coordinate is
// ScrollLeft itself and the last is ScrollLeft + ViewportWidth - Gutter.
// Always at least one column unless there are none.
func WindowOf(in ColumnWindowInput) ColumnWindow {
	offsets := in.Offsets
	count := len(offsets) - 1
	if count <= 0 {
		return ColumnWindow{}
	}
	from := in.ScrollLeft - in.Buffer
	to := in.ScrollLeft + max(0, in.ViewportWidth-in.Gutter) + in.Buffer

	// First column whose right edge is past from.
	low, high := 0, count-1
	for low < high {
		mid := (low + high) >> 1
		if offsets[mid+1] <= from {
			low = mid + 1
		} else {
			high = mid
		}
	}
	end := low + 1
	for end < count && offsets[end] < to {
		end++
	}
	return ColumnWindow{Start: low, End: end, Before: offsets[low]}
}

// IncludeColumn grows a window to also cover col, e.g. an open editor that
// must stay mounted.
func IncludeColumn(w ColumnWindow, col int, offsets []int) ColumnWindow {
	if col >= w.Start && col < w.End {
		return w
	}
	start := min(w.Start, col)
	return ColumnWindow{Start: start, End: max(w.End, col+1), Before: offsets[start]}
}
