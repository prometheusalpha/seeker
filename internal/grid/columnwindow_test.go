package grid

import "testing"

func uniform(n, w int) []int {
	ws := make([]int, n)
	for i := range ws {
		ws[i] = w
	}
	return ws
}

// 50 columns of 100px behind a 56px sticky gutter, in an 800px viewport.
var offsets50 = ColumnOffsets(uniform(50, 100))

func base() ColumnWindowInput {
	return ColumnWindowInput{Offsets: offsets50, ViewportWidth: 800, Gutter: 56}
}

func TestWindowOf(t *testing.T) {
	cases := []struct {
		name string
		in   func() ColumnWindowInput
		want ColumnWindow
	}{
		// 744px of columns visible: 0..7 (the eighth is cut at 744).
		{"renders only the columns the viewport shows", base, ColumnWindow{0, 8, 0}},
		// scrollLeft 150: column 1 (100..200) is half hidden under the pin;
		// the last visible coordinate is 150 + 744 = 894, inside column 8.
		{"a column partly under the gutter still renders", func() ColumnWindowInput { i := base(); i.ScrollLeft = 150; return i }, ColumnWindow{1, 9, 100}},
		{"buffer widens both sides", func() ColumnWindowInput { i := base(); i.ScrollLeft = 1000; i.Buffer = 300; return i }, ColumnWindow{7, 21, 700}},
		{"buffer clamps at the right edge", func() ColumnWindowInput { i := base(); i.ScrollLeft = 4256; i.Buffer = 300; return i }, ColumnWindow{39, 50, 3900}},
		{"an unmeasured viewport still renders one column", func() ColumnWindowInput { i := base(); i.ViewportWidth = 0; return i }, ColumnWindow{0, 1, 0}},
		{"no columns renders nothing", func() ColumnWindowInput { i := base(); i.Offsets = []int{0}; return i }, ColumnWindow{0, 0, 0}},
		{"uneven widths are found by their offsets", func() ColumnWindowInput {
			i := base()
			i.Offsets = ColumnOffsets([]int{40, 900, 40, 40})
			i.ScrollLeft = 500
			return i
		}, ColumnWindow{1, 4, 40}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := WindowOf(c.in()); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestIncludeColumn(t *testing.T) {
	w := ColumnWindow{10, 18, 1000}
	cases := []struct {
		name string
		col  int
		want ColumnWindow
	}{
		{"leaves a window that already covers the column", 12, w},
		{"grows left to an editor scrolled off the left", 3, ColumnWindow{3, 18, 300}},
		{"grows right to an editor scrolled off the right", 30, ColumnWindow{10, 31, 1000}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IncludeColumn(w, c.col, offsets50); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}
