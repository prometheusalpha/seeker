package grid

import (
	"slices"
	"strconv"
)

// Cell edits the grid holds until the user commits them, keyed by row index
// and result column index. Result columns rather than visible ones: hiding
// or reordering a column must not move an edit onto a different column.

type PendingEdit struct {
	Row, ResultCol int
	Value          any // nil sets NULL
}

func EditKey(row, resultCol int) string {
	return strconv.Itoa(row) + ":" + strconv.Itoa(resultCol)
}

type RowEdits struct {
	Row   int
	Edits []PendingEdit
}

// EditsByRow groups edits per row, rows ascending so the UPDATEs run top to
// bottom, and each row's edits by column.
func EditsByRow(edits []PendingEdit) []RowEdits {
	byRow := map[int][]PendingEdit{}
	for _, e := range edits {
		byRow[e.Row] = append(byRow[e.Row], e)
	}
	out := make([]RowEdits, 0, len(byRow))
	for row, list := range byRow {
		slices.SortStableFunc(list, func(a, b PendingEdit) int { return a.ResultCol - b.ResultCol })
		out = append(out, RowEdits{row, list})
	}
	slices.SortFunc(out, func(a, b RowEdits) int { return a.Row - b.Row })
	return out
}
