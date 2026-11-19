package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"seeker/internal/driver"
	"seeker/internal/grid"
	"seeker/internal/model"
	"seeker/internal/sqltext"
)

func sampleResult() driver.Result {
	return driver.Result{
		Columns: []driver.Column{{Name: "id", Type: "integer"}, {Name: "email", Type: "text"}, {Name: "score", Type: "numeric"}},
		Rows: [][]any{
			{"1", "a@x.io", "10.5"},
			{"2", nil, "4"},
			{"3", "it's", nil},
		},
	}
}

func sampleTarget() *TableTarget {
	return &TableTarget{Type: model.Postgres, Dialect: sqltext.Postgres, Schema: "public", Table: "users", Keys: []string{"id"}}
}

// The columns ui.Table gets start with the row numbers, pinned and fixed,
// then every result column with a width, which virtualisation needs.
func TestGridColumns(t *testing.T) {
	g := NewResultGrid(sampleResult(), nil)
	cols := g.TableColumns()
	if len(cols) != 4 {
		t.Fatalf("%d table columns, want row numbers + 3", len(cols))
	}
	if h := cols[0]; !h.RowHeader || !h.Fixed || !h.Frozen || h.Width <= 0 {
		t.Errorf("row number column: %+v", h)
	}
	for i, c := range cols[1:] {
		if c.Width <= 0 || c.Title != g.Columns[i].Name {
			t.Errorf("column %d: %+v", i, c)
		}
		if c.Sortable {
			t.Errorf("column %q sorts on a header click, which the plan rules out", c.Title)
		}
	}
	if !g.List.VirtualColumns || !g.List.CellSelect {
		t.Error("the grid does not virtualise its columns or choose cells")
	}
	if g.ResultCol(0) != -1 || g.ResultCol(2) != 1 {
		t.Errorf("column mapping: %d %d", g.ResultCol(0), g.ResultCol(2))
	}
}

func TestGridHideColumns(t *testing.T) {
	g := NewResultGrid(sampleResult(), nil)
	g.SetHidden(1, true)
	cols := g.TableColumns()
	if len(cols) != 3 || cols[2].Title != "score" || g.ResultCol(2) != 2 {
		t.Fatalf("after hiding email: %+v", cols)
	}
	g.SetHidden(0, true)
	g.SetHidden(2, true)
	if g.Hidden(2) || len(g.TableColumns()) != 2 {
		t.Error("the last visible column was hidden")
	}
	g.ShowAll()
	if len(g.TableColumns()) != 4 {
		t.Error("Show all left columns hidden")
	}
}

// A table reloaded keeps the order and widths the user gave, and the
// columns hidden; another set of columns starts afresh.
func TestGridInheritsLayoutOnlyForSameColumns(t *testing.T) {
	prev := NewResultGrid(sampleResult(), sampleTarget())
	prev.List.Columns.Order = []string{"c2", "c0", "c1"}
	prev.List.Columns.Widths = map[string]float32{"c0": 300}
	prev.SetHidden(1, true)

	same := NewResultGrid(sampleResult(), sampleTarget())
	same.Inherit(prev)
	if !slices.Equal(same.List.Columns.Order, prev.List.Columns.Order) || same.List.Columns.Widths["c0"] != 300 || !same.Hidden(1) {
		t.Errorf("same columns: %+v hidden %v", same.List.Columns, same.Hidden(1))
	}
	// A copy, not shared: the new grid's drags must not write into the old.
	same.List.Columns.Widths["c0"] = 1
	if prev.List.Columns.Widths["c0"] != 300 {
		t.Error("the layout is shared with the grid it came from")
	}

	other := sampleResult()
	other.Columns = other.Columns[:2]
	g := NewResultGrid(other, nil)
	g.Inherit(prev)
	if len(g.List.Columns.Order) != 0 || g.Hidden(1) {
		t.Errorf("a different result took the layout: %+v", g.List.Columns)
	}
}

// A header drag writes the order of the columns shown; a column hidden
// through it comes back where it was, and the Columns list reads the same
// order.
func TestGridHeaderDragKeepsHiddenPlace(t *testing.T) {
	g := NewResultGrid(sampleResult(), nil)
	g.SetHidden(1, true)
	// What ui.Table writes as score is dragged before id.
	g.List.Columns.Order = []string{"#", "c2", "c0"}
	if got := g.ColumnOrder(); !slices.Equal(got, []int{2, 1, 0}) {
		t.Errorf("order after the drag: %v, want [2 1 0]", got)
	}
	g.SetHidden(1, false)
	if got := g.ColumnOrder(); !slices.Equal(got, []int{2, 1, 0}) {
		t.Errorf("order after showing email: %v, want it back in its place", got)
	}
	if !slices.Equal(g.List.Columns.Order, []string{"c2", "c1", "c0"}) {
		t.Errorf("the table's order: %v", g.List.Columns.Order)
	}
}

// A column moved in the Columns list moves in the table, hidden ones too.
func TestGridMoveColumns(t *testing.T) {
	g := NewResultGrid(sampleResult(), nil)
	g.SetHidden(2, true)
	g.MoveColumns([]int{2}, 0)
	if got := g.ColumnOrder(); !slices.Equal(got, []int{2, 0, 1}) {
		t.Errorf("score moved first: %v", got)
	}
	g.MoveColumns([]int{0}, 3)
	if got := g.ColumnOrder(); !slices.Equal(got, []int{0, 1, 2}) {
		t.Errorf("score moved to the end: %v", got)
	}
	if !slices.Equal(g.List.Columns.Order, []string{"c0", "c1", "c2"}) {
		t.Errorf("the table's order: %v", g.List.Columns.Order)
	}
}

func TestGridEditsStageAndChange(t *testing.T) {
	g := NewResultGrid(sampleResult(), sampleTarget())
	if !g.CanEdit() {
		t.Fatal("a table with a primary key in the result is not editable")
	}
	g.StageEdit(0, 1, "new@x.io", false)
	g.StageEdit(0, 2, "", true)
	g.StageEdit(1, 1, "", true)      // NULL over NULL: nothing changes
	g.StageEdit(2, 1, "it's", false) // the value it has: not an edit
	g.StageEdit(2, 2, "7", false)
	if g.Pending() != 3 {
		t.Fatalf("%d edits staged, want 3", g.Pending())
	}
	if g.Text(0, 1) != "new@x.io" || !g.Edited(0, 1) || g.Value(0, 2) != nil {
		t.Errorf("staged values: %q, edited %v, %v", g.Text(0, 1), g.Edited(0, 1), g.Value(0, 2))
	}
	changes := g.Changes()
	if len(changes) != 2 {
		t.Fatalf("changes: %+v", changes)
	}
	// One change per row, rows from the top, columns in order, keyed by the
	// values read.
	c0 := changes[0]
	if c0.KeyValues[0] != "1" || len(c0.Changes) != 2 || c0.Changes[0].Column != "email" ||
		c0.Changes[0].Value != "new@x.io" || c0.Changes[1].Column != "score" || c0.Changes[1].Value != nil {
		t.Errorf("first change: %+v", c0)
	}
	if c1 := changes[1]; c1.KeyValues[0] != "3" || len(c1.Changes) != 1 || c1.Changes[0].Column != "score" || c1.Changes[0].Value != "7" {
		t.Errorf("second change: %+v", c1)
	}
	// Putting the original back takes the edit out.
	g.StageEdit(0, 1, "a@x.io", false)
	if g.Pending() != 2 {
		t.Errorf("the original value left %d edits", g.Pending())
	}
	g.Discard()
	if g.Pending() != 0 || g.Text(0, 1) != "a@x.io" {
		t.Error("Discard kept an edit")
	}
}

func TestGridNotEditable(t *testing.T) {
	if NewResultGrid(sampleResult(), nil).CanEdit() {
		t.Error("a query result is editable")
	}
	noKey := sampleTarget()
	noKey.Keys = nil
	if g := NewResultGrid(sampleResult(), noKey); g.CanEdit() || g.BeginEdit(0, 1) {
		t.Error("a table without a primary key is editable")
	}
	missing := sampleTarget()
	missing.Keys = []string{"tenant_id"}
	if NewResultGrid(sampleResult(), missing).CanEdit() {
		t.Error("a key missing from the result is editable")
	}
	g := NewResultGrid(sampleResult(), nil)
	g.StageEdit(0, 1, "x", false)
	if g.Pending() != 0 {
		t.Error("an edit was staged on a query result")
	}
}

func TestGridCellEditor(t *testing.T) {
	g := NewResultGrid(sampleResult(), sampleTarget())
	if !g.BeginEdit(0, 1) || g.EditText != "a@x.io" || !g.EditFocus {
		t.Fatalf("editor: %+v %q", g.Editing, g.EditText)
	}
	g.EditText = "b@x.io"
	g.FinishEdit(false)
	if g.Pending() != 0 || g.Editing != nil {
		t.Error("Escape staged the edit")
	}
	g.BeginEdit(0, 1)
	g.EditText = "b@x.io"
	g.FinishEdit(true)
	if g.Text(0, 1) != "b@x.io" {
		t.Errorf("Enter did not stage: %q", g.Text(0, 1))
	}
}

// INSERT writes every column of the row as the server returned it, hidden
// ones too, and a query result (no table) has none.
func TestGridInsertSQL(t *testing.T) {
	g := NewResultGrid(sampleResult(), sampleTarget())
	g.SetHidden(1, true)
	got := g.InsertSQL([]int{0, 2})
	want := "INSERT INTO \"public\".\"users\" (\"id\", \"email\", \"score\") VALUES (1, 'a@x.io', 10.5);\n" +
		"INSERT INTO \"public\".\"users\" (\"id\", \"email\", \"score\") VALUES (3, 'it''s', NULL);"
	if got != want {
		t.Errorf("InsertSQL =\n%s\nwant\n%s", got, want)
	}
	if NewResultGrid(sampleResult(), nil).CanInsert() {
		t.Error("a query result can write INSERT")
	}
}

func TestGridExportIsWholeResult(t *testing.T) {
	g := NewResultGrid(sampleResult(), nil)
	g.SetHidden(1, true)
	if csv := g.CSV(); !strings.HasPrefix(csv, "id,email,score\n1,a@x.io,10.5\n2,,4\n") {
		t.Errorf("CSV (hidden columns are still exported): %q", csv)
	}
	if js := g.JSON(); !strings.Contains(js, `"email": null`) {
		t.Errorf("JSON: %s", js)
	}
}

// Export writes the whole result to the file the user picked, adding the
// extension, and says so.
func TestExportGridWritesFile(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	old := pickExport
	t.Cleanup(func() { pickExport = old })
	pickExport = func(name, ext string) (string, error) { return filepath.Join(dir, "out"), nil }
	g := NewResultGrid(sampleResult(), nil)
	a.ExportGrid(g, "users", "csv")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "out.csv")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "id,email,score\n1,a@x.io,10.5") {
		t.Errorf("exported %q", data)
	}
	// A cancelled panel writes nothing and says nothing.
	pickExport = func(name, ext string) (string, error) { return "", nil }
	a.ExportGrid(g, "users", "json")
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "out.json")); err == nil {
		t.Error("a cancelled export wrote a file")
	}
}

func TestRefitScalesWidthsButKeepsDraggedOnes(t *testing.T) {
	g := NewResultGrid(sampleResult(), nil)
	before := g.Width(1)
	g.List.Columns.Widths = map[string]float32{colID(0): 123}
	g.Refit(grid.GridCharWidth * 2)
	if g.Width(1) <= before {
		t.Errorf("width after a larger font = %v, want more than %v", g.Width(1), before)
	}
	if g.Width(0) != 123 {
		t.Errorf("dragged width = %v, want 123", g.Width(0))
	}
}

func TestGridSortMarks(t *testing.T) {
	g := NewResultGrid(sampleResult(), sampleTarget())
	plain := g.Width(0)
	g.SetSort(`"email" DESC`)
	cols := g.TableColumns()
	if cols[2].Title != "email ▼" || cols[1].Title != "id" {
		t.Errorf("titles: %q, %q", cols[1].Title, cols[2].Title)
	}
	if g.Width(0) != plain {
		t.Errorf("an unsorted column changed width: %v -> %v", plain, g.Width(0))
	}
	g.SetSort(`"score", "id" DESC`)
	cols = g.TableColumns()
	if cols[3].Title != "score ▲1" || cols[1].Title != "id ▼2" || cols[2].Title != "email" {
		t.Errorf("titles: %q, %q, %q", cols[1].Title, cols[2].Title, cols[3].Title)
	}
	g.SetSort("")
	if g.TableColumns()[1].Title != "id" {
		t.Error("an empty ORDER BY left an arrow")
	}
}

func TestReadOnlyReason(t *testing.T) {
	if why := NewResultGrid(sampleResult(), nil).ReadOnlyReason(); why != "" {
		t.Errorf("a query's result explained itself: %q", why)
	}
	if why := NewResultGrid(sampleResult(), sampleTarget()).ReadOnlyReason(); why != "" {
		t.Errorf("an editable grid said %q", why)
	}
	noKey := sampleTarget()
	noKey.Keys = nil
	if why := NewResultGrid(sampleResult(), noKey).ReadOnlyReason(); !strings.Contains(why, "no primary key") {
		t.Errorf("no key: %q", why)
	}
	missing := sampleTarget()
	missing.Keys = []string{"uuid"}
	if why := NewResultGrid(sampleResult(), missing).ReadOnlyReason(); !strings.Contains(why, "not in the result") {
		t.Errorf("key not in result: %q", why)
	}
}
