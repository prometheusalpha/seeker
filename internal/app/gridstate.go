package app

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"seeker/internal/driver"
	"seeker/internal/grid"
	"seeker/internal/model"
	"seeker/internal/sqltext"
)

// TableTarget is the table a grid's rows came from, known for a table tab
// and nothing else: a query's result has no table behind it to UPDATE or to
// write an INSERT for.
type TableTarget struct {
	Type            model.DBType
	Dialect         sqltext.Dialect
	Database, Table string
	Schema          string
	// Keys are the primary key columns; a table without any cannot be
	// edited, as no row could be addressed.
	Keys []string
}

// CellRef is a cell by row and result column, not by the column's place in
// the table: hiding or moving a column must not move an edit.
type CellRef struct{ Row, Col int }

// ResultGrid is the grid over one result set: the state ui.Table keeps, the
// rows chosen, the columns hidden and the edits waiting to be written. It is
// created when a result arrives and dropped when another replaces it, so
// the selection and the columns of one never stick to the next.
type ResultGrid struct {
	// List is ui.Table's own state. Selection holds the rows chosen, by
	// their index, and Selected the row the keys move from.
	List ui.ListState
	Sel  ui.Selection[int]
	Row  int

	Columns []driver.Column
	Rows    [][]any
	Target  *TableTarget

	hidden map[int]bool
	widths []float32
	// charW is the advance the widths were sized for.
	charW  float64
	numW   float32
	cols   []ui.TableColumn
	colMap []int
	valid  bool

	Edits   map[string]grid.PendingEdit
	Editing *CellRef
	// EditText is what the open cell editor holds; EditFocus asks the view
	// to give it the keyboard once, as it opens.
	EditText  string
	EditFocus bool
}

// rowHeaderID names the column of row numbers.
const rowHeaderID = "#"

func colID(result int) string { return "c" + strconv.Itoa(result) }

func parseColID(id string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "c"))
	return n, err == nil && strings.HasPrefix(id, "c")
}

// NewResultGrid makes the grid of a result. Column widths come from the
// first and last rows and the headers, once, so the columns can be
// virtualised (they need a width each) and do not jump as rows scroll in.
func NewResultGrid(res driver.Result, target *TableTarget) *ResultGrid {
	return newResultGrid(res, target, grid.GridCharWidth)
}

// newResultGrid sizes the columns for a monospace advance of charW px.
func newResultGrid(res driver.Result, target *TableTarget, charW float64) *ResultGrid {
	g := &ResultGrid{
		Columns: res.Columns, Rows: res.Rows, Target: target,
		Row: -1, hidden: map[int]bool{}, Edits: map[string]grid.PendingEdit{},
	}
	g.List.CellSelect = true
	g.List.VirtualColumns = true
	g.List.Selection = &g.Sel
	g.List.Selected = &g.Row
	g.widths = make([]float32, len(res.Columns))
	g.Refit(charW)
	g.numW = float32(max(44, 16+8*len(strconv.Itoa(max(1, len(res.Rows))))))
	return g
}

// Refit sizes the columns again for a monospace advance of charW px, when
// the font changed. Widths the user dragged stay as they set them.
func (g *ResultGrid) Refit(charW float64) {
	if charW == g.charW {
		return
	}
	g.charW = charW
	for i, c := range g.Columns {
		g.widths[i] = float32(grid.AutoColumnWidth(grid.ColumnWidthInput{
			Header:    c.Name,
			Values:    grid.SampleColumnValues(g.Rows, i, c.Type, grid.GridSampleRows),
			CharWidth: charW,
		}))
	}
	g.valid = false
}

// Signature identifies the columns, so a layout is only carried over to a
// result that has the same ones.
func (g *ResultGrid) Signature() string {
	var b strings.Builder
	for _, c := range g.Columns {
		b.WriteString(c.Name + "\x00" + c.Type + "\x00")
	}
	return b.String()
}

// Inherit takes the order and the widths the user gave the columns of prev,
// and the columns it hid, when this result has the same columns: a table
// reloaded keeps the layout it had.
func (g *ResultGrid) Inherit(prev *ResultGrid) {
	if prev == nil || prev.Signature() != g.Signature() {
		return
	}
	g.List.Columns = ui.TableLayout{
		Order:  slices.Clone(prev.List.Columns.Order),
		Widths: cloneWidths(prev.List.Columns.Widths),
	}
	for i := range prev.hidden {
		g.hidden[i] = true
	}
	g.valid = false
}

func cloneWidths(m map[string]float32) map[string]float32 {
	if m == nil {
		return nil
	}
	out := make(map[string]float32, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// TableColumns are the columns ui.Table is given: the pinned row numbers,
// then the visible result columns. The cell builder maps its column index
// back with ResultCol.
func (g *ResultGrid) TableColumns() []ui.TableColumn {
	if g.valid {
		return g.cols
	}
	g.cols = []ui.TableColumn{{ID: rowHeaderID, Width: g.numW, MinWidth: g.numW, RowHeader: true, Fixed: true, Frozen: true}}
	g.colMap = []int{-1}
	for i, c := range g.Columns {
		if g.hidden[i] {
			continue
		}
		g.cols = append(g.cols, ui.TableColumn{ID: colID(i), Title: c.Name, Width: g.widths[i], MinWidth: 40})
		g.colMap = append(g.colMap, i)
	}
	g.valid = true
	return g.cols
}

// ResultCol is the result column behind a column of the table, -1 for the
// row numbers.
func (g *ResultGrid) ResultCol(col int) int {
	g.TableColumns()
	if col < 0 || col >= len(g.colMap) {
		return -1
	}
	return g.colMap[col]
}

// Width is the width of a result column as the table lays it out, which
// the user may have changed.
func (g *ResultGrid) Width(result int) float32 {
	if w, ok := g.List.Columns.Widths[colID(result)]; ok {
		return w
	}
	return g.widths[result]
}

// SetHidden shows or hides a result column. The last visible column stays:
// a grid of nothing has no header to show the menu from.
func (g *ResultGrid) SetHidden(result int, hide bool) {
	if hide == g.hidden[result] || result < 0 || result >= len(g.Columns) {
		return
	}
	if hide {
		if len(g.Columns)-len(g.hidden) <= 1 {
			return
		}
		g.hidden[result] = true
	} else {
		delete(g.hidden, result)
	}
	g.valid = false
}

func (g *ResultGrid) Hidden(result int) bool { return g.hidden[result] }

// ShowAll brings every hidden column back.
func (g *ResultGrid) ShowAll() {
	clear(g.hidden)
	g.valid = false
}

// SelectedRows are the rows chosen, top to bottom.
func (g *ResultGrid) SelectedRows() []int {
	rows := slices.Collect(g.Sel.All())
	slices.Sort(rows)
	return rows
}

// SelectedCols are the result columns of the block chosen, in the order
// shown.
func (g *ResultGrid) SelectedCols() []int {
	var out []int
	for _, id := range g.List.SelectedColumns() {
		if n, ok := parseColID(id); ok {
			out = append(out, n)
		}
	}
	return out
}

func (g *ResultGrid) cell(row, col int) any {
	if row < 0 || row >= len(g.Rows) || col < 0 || col >= len(g.Rows[row]) {
		return nil
	}
	return g.Rows[row][col]
}

// Value is what a cell shows: the edit staged for it, else the row's own.
func (g *ResultGrid) Value(row, col int) any {
	if e, ok := g.Edits[grid.EditKey(row, col)]; ok {
		return e.Value
	}
	return g.cell(row, col)
}

// Text is a cell as the grid paints it.
func (g *ResultGrid) Text(row, col int) string {
	return grid.FormatCell(g.Value(row, col), g.Columns[col].Type)
}

// Sum adds up the cells chosen that are numbers.
func (g *ResultGrid) Sum() grid.CellSumResult {
	var cells []grid.SumCell
	cols := g.SelectedCols()
	for _, r := range g.SelectedRows() {
		for _, c := range cols {
			cells = append(cells, grid.SumCell{Value: g.Value(r, c), ColumnType: g.Columns[c].Type})
		}
	}
	return grid.SumCells(cells)
}

// CopyText is the block chosen as TSV, read from the result and not from
// what was built, so a block taller or wider than the window copies whole.
func (g *ResultGrid) CopyText() string {
	cols := g.SelectedCols()
	var rows [][]string
	for _, r := range g.SelectedRows() {
		line := make([]string, len(cols))
		for i, c := range cols {
			line[i] = g.Text(r, c)
		}
		rows = append(rows, line)
	}
	return grid.TSV(rows)
}

// CanInsert reports that a row can be written as an INSERT: the grid is a
// table's, which names the target.
func (g *ResultGrid) CanInsert() bool { return g.Target != nil }

// InsertSQL is one INSERT per row, over every column (a hidden column is
// still written, or the statement would write another row) and the values
// as the server returned them.
func (g *ResultGrid) InsertSQL(rows []int) string {
	if g.Target == nil {
		return ""
	}
	names := make([]string, len(g.Columns))
	types := make([]string, len(g.Columns))
	for i, c := range g.Columns {
		names[i], types[i] = c.Name, c.Type
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		if r < 0 || r >= len(g.Rows) {
			continue
		}
		lines = append(lines, grid.BuildInsert(grid.InsertInput{
			Dialect: g.Target.Dialect, Database: g.Target.Database, Schema: g.Target.Schema, Table: g.Target.Table,
			Columns: names, ColumnTypes: types, Row: g.Rows[r],
		}))
	}
	return strings.Join(lines, "\n")
}

func (g *ResultGrid) names() []string {
	names := make([]string, len(g.Columns))
	for i, c := range g.Columns {
		names[i] = c.Name
	}
	return names
}

// CSV and JSON are the whole result, as the server returned it.
func (g *ResultGrid) CSV() string  { return grid.CSV(g.names(), g.Rows) }
func (g *ResultGrid) JSON() string { return grid.JSON(g.names(), g.Rows) }

// keyColumns are the result columns of the primary key, in key order; nil
// when one is missing from the result.
func (g *ResultGrid) keyColumns() []int {
	if g.Target == nil {
		return nil
	}
	var out []int
	for _, k := range g.Target.Keys {
		i := slices.IndexFunc(g.Columns, func(c driver.Column) bool { return c.Name == k })
		if i < 0 {
			return nil
		}
		out = append(out, i)
	}
	return out
}

// CanEdit reports a grid whose rows can be addressed: a table with a
// primary key, every part of it in the result.
func (g *ResultGrid) CanEdit() bool {
	return g.Target != nil && len(g.Target.Keys) > 0 && g.keyColumns() != nil
}

// StageEdit holds a new value for a cell until the edits are committed;
// the value the row already has takes the edit back out. null stages NULL.
func (g *ResultGrid) StageEdit(row, col int, text string, null bool) {
	if !g.CanEdit() || row < 0 || row >= len(g.Rows) || col < 0 || col >= len(g.Columns) {
		return
	}
	key := grid.EditKey(row, col)
	orig := g.cell(row, col)
	if null == (orig == nil) && (null || grid.RawText(orig) == text) {
		delete(g.Edits, key)
		return
	}
	var v any
	if !null {
		v = text
	}
	g.Edits[key] = grid.PendingEdit{Row: row, ResultCol: col, Value: v}
}

// Edited reports a cell with an edit staged.
func (g *ResultGrid) Edited(row, col int) bool {
	_, ok := g.Edits[grid.EditKey(row, col)]
	return ok
}

// Pending is how many cells have an edit staged.
func (g *ResultGrid) Pending() int { return len(g.Edits) }

// Discard drops the staged edits.
func (g *ResultGrid) Discard() {
	clear(g.Edits)
	g.Editing = nil
}

// Changes are the staged edits as one change per row, the key values being
// those the grid last read.
func (g *ResultGrid) Changes() []driver.RowChange {
	keys := g.keyColumns()
	if keys == nil {
		return nil
	}
	edits := make([]grid.PendingEdit, 0, len(g.Edits))
	for _, e := range g.Edits {
		edits = append(edits, e)
	}
	var out []driver.RowChange
	for _, re := range grid.EditsByRow(edits) {
		ch := driver.RowChange{}
		for _, k := range keys {
			ch.KeyValues = append(ch.KeyValues, g.cell(re.Row, k))
		}
		for _, e := range re.Edits {
			ch.Changes = append(ch.Changes, driver.CellChange{Column: g.Columns[e.ResultCol].Name, Value: e.Value})
		}
		out = append(out, ch)
	}
	return out
}

// BeginEdit opens the editor of a cell, which starts from the raw value
// (what an UPDATE would replace), not from what the grid paints.
func (g *ResultGrid) BeginEdit(row, col int) bool {
	if !g.CanEdit() || row < 0 || row >= len(g.Rows) || col < 0 || col >= len(g.Columns) || g.hidden[col] {
		return false
	}
	g.Editing = &CellRef{row, col}
	g.EditText = grid.RawText(g.Value(row, col))
	g.EditFocus = true
	return true
}

// FinishEdit closes the editor, staging what it holds when save is set.
func (g *ResultGrid) FinishEdit(save bool) {
	if g.Editing == nil {
		return
	}
	if save {
		g.StageEdit(g.Editing.Row, g.Editing.Col, g.EditText, false)
	}
	g.Editing = nil
}

// CursorCol is the result column the cursor is on, false before a cell is
// chosen.
func (g *ResultGrid) CursorCol() (int, bool) { return parseColID(g.List.Cells.Cursor) }
