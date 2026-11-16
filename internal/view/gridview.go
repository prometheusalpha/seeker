package view

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/grid"
	"seeker/internal/theme"
)

// The face of the cells is the table font of the settings. Widths are
// computed for a monospace advance (UI.CharWidth), so it must be one.
const (
	// fallbackFamily is what a blank table family leaves the cells in.
	fallbackFamily = "Menlo"
	// valueBoxHeight is the box that shows a cell in full.
	valueBoxHeight = 96
)

// kindColor is the color a value wears by its column's type: numbers in the
// accent, instants in the success color, booleans and structured values in
// the warning color, identifiers and binary dimmed, and anything the type
// does not name in the text color: a wrong color would claim something
// about the data.
func kindColor(t *ui.Theme, typ string) ui.Color {
	switch grid.ResolveKind(typ) {
	case grid.KindInteger, grid.KindNumeric:
		return t.Accent
	case grid.KindTemporal:
		return t.Success
	case grid.KindBoolean, grid.KindStructured, grid.KindSpatial:
		return t.Warning
	case grid.KindIdentifier, grid.KindBinary:
		return t.TextMuted
	}
	return t.Text
}

// gridCtx is what a grid's callbacks need of the tab around it.
type gridCtx struct {
	tabID string
	// tv is the table tab's state, nil for a query's result: only a table
	// can be re-sorted or edited.
	tv *app.TableView
}

// resultGrid builds g on ui.Table: columns virtualised, the row numbers
// pinned, a block of cells chosen by click, drag or keys.
func (s *App) resultGrid(c *ui.Context, g *app.ResultGrid, gc gridCtx) {
	t := c.Theme()
	g.List.CellsStyle = ui.CellsStyle{
		Cell: t.Accent, CellText: t.AccentText,
		Row: t.Accent.Alpha(0.14), Stripe: t.Text.Alpha(0.03),
	}
	g.List.HeaderMenu = func(m *ui.Menu, col int) { s.headerMenu(c, m, g, gc, g.ResultCol(col)) }
	g.List.CellMenu = func(m *ui.Menu, row, col int) { s.cellMenu(c, m, g, gc, row, g.ResultCol(col)) }
	g.List.HeaderCell = func(cell ui.Element, col int) {
		if rc := g.ResultCol(col); rc >= 0 {
			cell.Tooltip(g.Columns[rc].Name + " · " + g.Columns[rc].Type)
		}
	}
	// The grid is denser than a list: ui.Table rows are Space(8) high.
	dense := *t
	dense.Spacing = 3
	var table ui.Element
	ui.Themed(c, &dense).Background(ui.Transparent).Grow(1).FillWidth().Children(func() {
		table = ui.Table(c, &g.List, g.TableColumns(), len(g.Rows), func(row, col int) {
			s.gridCell(c, g, row, col)
		}).Grow(1).FillWidth()
		if table.Submitted() {
			if rc, ok := g.CursorCol(); ok {
				g.BeginEdit(g.Row, rc)
			}
		}
	})
	if s.refocusGrid {
		s.refocusGrid = false
		table.Focus()
	}
	s.gridKeys(c, g, gc, table)
	s.valueBox(c, g)
}

// gridCell is one cell: the row number, the editor of the cell being
// edited, or the value with its type's color and NULL dimmed.
func (s *App) gridCell(c *ui.Context, g *app.ResultGrid, row, col int) {
	t := c.Theme()
	chosen := g.List.CellSelected(row, col)
	rc := g.ResultCol(col)
	if rc < 0 {
		color := t.TextMuted
		if chosen {
			color = t.AccentText
		}
		s.tableFont(ui.Text(c, strconv.Itoa(row+1)), -1).TextColor(color).SingleLine()
		return
	}
	if e := g.Editing; e != nil && e.Row == row && e.Col == rc {
		in := s.tableFont(ui.TextInput(c.Key("cell-edit"), &g.EditText), 0).FillWidth()
		if g.EditFocus {
			g.EditFocus = false
			in.Focus()
		}
		switch {
		case in.Submitted():
			g.FinishEdit(true)
			s.refocusGrid = true
		case c.Shortcut(0, ui.KeyEscape):
			g.FinishEdit(false)
			s.refocusGrid = true
		}
		return
	}
	value := g.Value(row, rc)
	var el ui.Element
	if value == nil {
		color := t.TextMuted.Alpha(0.6)
		if chosen {
			color = t.AccentText.Alpha(0.6)
		}
		el = s.tableFont(ui.Text(c, "NULL"), 0).TextColor(color).SingleLine()
	} else {
		text := g.Text(row, rc)
		color := kindColor(t, g.Columns[rc].Type)
		if chosen {
			color = t.AccentText
		}
		el = s.tableFont(ui.Text(c, text), 0).TextColor(color).SingleLine()
		if s.cut(text, g.Width(rc)) {
			el.Tooltip(oneLine(text, 2000))
		}
	}
	if g.Edited(row, rc) {
		el.Background(t.Warning.Alpha(0.25))
	}
}

// cut reports a text that does not fit its column, so it shows ellipsised.
func (s *App) cut(text string, width float32) bool {
	return float32(grid.EstimateTextWidth(text, s.core.UI().CharWidth()*0.9))+16 > width
}

// tableFont sets e in the table font of the settings, at their size plus
// delta, so row numbers and the value box sit a point under the cells.
func (s *App) tableFont(e ui.Element, delta float32) ui.Element {
	f := s.core.UI().Font
	family := strings.TrimSpace(f.Table)
	if family == "" {
		family = fallbackFamily
	}
	e = e.Font(family).FontSize(float32(f.Size) + delta)
	if feats := theme.ParseFeatures(f.Features); feats != "" {
		e = e.FontFeatures(feats)
	}
	return e
}

// valueBox shows the cell the cursor is on in full when its column cuts
// it, or it holds line breaks.
func (s *App) valueBox(c *ui.Context, g *app.ResultGrid) {
	rc, ok := g.CursorCol()
	if !ok || g.Row < 0 || g.Row >= len(g.Rows) || g.Hidden(rc) || g.Editing != nil {
		return
	}
	value := g.Value(g.Row, rc)
	if value == nil {
		return
	}
	text := g.Text(g.Row, rc)
	if !strings.ContainsAny(text, "\n\r") && !s.cut(text, g.Width(rc)) {
		return
	}
	t := c.Theme()
	ui.Divider(c)
	ui.Scroll(c.Key("value-box")).Height(valueBoxHeight).FillWidth().Children(func() {
		s.tableFont(ui.Text(c, text), -1).TextColor(kindColor(t, g.Columns[rc].Type)).Selectable().Wrap()
	})
}

// gridKeys takes the grid's own keys while it has the focus: copy, sort.
func (s *App) gridKeys(c *ui.Context, g *app.ResultGrid, gc gridCtx, table ui.Element) {
	if !table.FocusWithin() || g.Editing != nil {
		return
	}
	if c.Shortcut(ui.Cmd, ui.KeyC) {
		s.copySelection(c, g)
	}
	if gc.tv == nil {
		return
	}
	rc, ok := g.CursorCol()
	if !ok {
		return
	}
	if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyUp) {
		s.core.SortTable(gc.tabID, rc, false)
	}
	if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyDown) {
		s.core.SortTable(gc.tabID, rc, true)
	}
}

func (s *App) copySelection(c *ui.Context, g *app.ResultGrid) {
	if len(g.SelectedRows()) == 0 {
		return
	}
	c.WriteClipboard(g.CopyText())
}

// headerMenu is the context menu of a column's header: sort by it (a
// table's), hide it, and the list of columns to show again. A click on the
// name sorts nothing, as recall's TODO asks.
func (s *App) headerMenu(c *ui.Context, m *ui.Menu, g *app.ResultGrid, gc gridCtx, rc int) {
	if rc >= 0 {
		s.sortItems(m, gc, rc)
		if m.Item("Copy column name").Chosen() {
			c.WriteClipboard(g.Columns[rc].Name)
		}
		if m.Item("Hide column").ID("hide").Disabled(len(g.Columns) <= 1).Chosen() {
			g.SetHidden(rc, true)
		}
		m.Separator()
	}
	s.columnsMenu(m, g)
}

// columnsMenu lists every column with a check by those shown.
func (s *App) columnsMenu(m *ui.Menu, g *app.ResultGrid) {
	m.Submenu("Columns", func(sub *ui.Menu) {
		if sub.Item("Show all").ID("show-all").Chosen() {
			g.ShowAll()
		}
		sub.Separator()
		for i, col := range g.Columns {
			if sub.Item(col.Name).ID("col-" + strconv.Itoa(i)).Checked(!g.Hidden(i)).Chosen() {
				g.SetHidden(i, !g.Hidden(i))
			}
		}
	})
}

// sortItems are the sort entries of a table's menus; a query's result has no
// table behind it to sort.
func (s *App) sortItems(m *ui.Menu, gc gridCtx, rc int) {
	if gc.tv == nil {
		return
	}
	if m.Item("Sort ascending").ID("asc").Shortcut(ui.Ctrl|ui.Shift, ui.KeyUp).Chosen() {
		s.core.SortTable(gc.tabID, rc, false)
	}
	if m.Item("Sort descending").ID("desc").Shortcut(ui.Ctrl|ui.Shift, ui.KeyDown).Chosen() {
		s.core.SortTable(gc.tabID, rc, true)
	}
	if gc.tv.OrderBy != "" && m.Item("Clear sort").ID("clear").Chosen() {
		s.core.ClearSort(gc.tabID)
	}
}

// cellMenu is the context menu of a cell or a row number: copy the block,
// copy rows as INSERT and edit (a table's), sort by the column, export.
func (s *App) cellMenu(c *ui.Context, m *ui.Menu, g *app.ResultGrid, gc gridCtx, row, rc int) {
	if m.Item("Copy").ID("copy").Shortcut(ui.Cmd, ui.KeyC).Disabled(len(g.SelectedRows()) == 0).Chosen() {
		s.copySelection(c, g)
	}
	if g.CanInsert() {
		rows := g.SelectedRows()
		if len(rows) == 0 {
			rows = []int{row}
		}
		label := "Copy row as INSERT"
		if len(rows) > 1 {
			label = fmt.Sprintf("Copy %d rows as INSERT", len(rows))
		}
		if m.Item(label).ID("insert").Chosen() {
			c.WriteClipboard(g.InsertSQL(rows))
			s.core.Toast("INSERT copied")
		}
	}
	if rc >= 0 {
		if g.CanEdit() {
			m.Separator()
			if m.Item("Edit cell").ID("edit").Chosen() {
				g.BeginEdit(row, rc)
			}
			if m.Item("Set NULL").ID("null").Chosen() {
				g.StageEdit(row, rc, "", true)
			}
		}
		if gc.tv != nil {
			m.Separator()
			s.sortItems(m, gc, rc)
		}
	}
	m.Separator()
	s.columnsMenu(m, g)
}

// exportMenu is the Export button: the whole result as CSV or JSON.
func (s *App) exportMenu(c *ui.Context, g *app.ResultGrid, title string) {
	ui.PopoverMenuButton(c.Key("export"), "Export", func(m *ui.Menu) {
		if m.Item("CSV").ID("csv").Chosen() {
			s.core.ExportGrid(g, title, "csv")
		}
		if m.Item("JSON").ID("json").Chosen() {
			s.core.ExportGrid(g, title, "json")
		}
	}).Height(26).Disabled(len(g.Rows) == 0).Tooltip("Write the whole result to a file")
}

// columnsButton is the menu of columns to show or hide.
func (s *App) columnsButton(c *ui.Context, g *app.ResultGrid) {
	ui.PopoverMenuButton(c.Key("columns"), "Columns", func(m *ui.Menu) {
		if m.Item("Show all").ID("show-all").Chosen() {
			g.ShowAll()
		}
		m.Separator()
		for i, col := range g.Columns {
			if m.Item(col.Name).ID("col-" + strconv.Itoa(i)).Checked(!g.Hidden(i)).Chosen() {
				g.SetHidden(i, !g.Hidden(i))
			}
		}
	}).Height(26).Tooltip("Show or hide columns")
}

// sumText is the total of the cells chosen, as the status bar shows it.
func sumText(r grid.CellSumResult) string {
	if r.Count == 0 {
		return ""
	}
	sum := strconv.FormatFloat(math.Round(r.Sum*1e6)/1e6, 'f', -1, 64)
	return fmt.Sprintf("Sum %s · %d cells", sum, r.Count)
}
