package view

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/driver"
	"seeker/internal/model"
)

func livePGWindow(t *testing.T, sql string) (model.Profile, string, *app.App, *pump, *ui.Tester) {
	t.Helper()
	raw := os.Getenv("SEEKER_TEST_PG_URL")
	if raw == "" {
		t.Skip("SEEKER_TEST_PG_URL is not set; start recall/compose.yaml")
	}
	profile, password := livePostgres(t, raw)
	core, p, tt := editorWindow(t, profile, password, sql)
	return profile, password, core, p, tt
}

func runQuery(t *testing.T, core *app.App, p *pump, tt *ui.Tester, wantText string) {
	t.Helper()
	id := activeID(core)
	clickEditor(t, tt, 100, 15)
	tt.Key(ui.Cmd, ui.KeyEnter)
	p.waitUntil(t, tt, "the result", func() bool { return tt.HasText(wantText) && !core.Running(id) })
}

// A result shows on ui.Table: its rows, the row numbers, a block of cells
// chosen with the keys, summed in the status bar, and copied as TSV.
func TestLiveQueryGrid(t *testing.T) {
	_, _, core, p, tt := livePGWindow(t, "select 100*g as n, 'x'||g as s from generate_series(1,5) g")
	runQuery(t, core, p, tt, "x3")
	if !hasAny(tt, "5 rows") {
		t.Errorf("no row count: %v", tt.Texts())
	}
	id := activeID(core)
	g := core.GridOf(id, 0)
	if g == nil || len(g.Rows) != 5 {
		t.Fatalf("grid: %+v", g)
	}

	// A click chooses a cell and its row; Cmd+A every cell.
	if err := tt.Click("300"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	p.frame(tt)
	if got := g.SelectedRows(); len(got) != 5 {
		t.Fatalf("rows chosen: %v", got)
	}
	if sum := g.Sum(); sum.Sum != 1500 || sum.Count != 5 {
		t.Errorf("sum %+v", sum)
	}
	if !hasAny(tt, "sum 1500") {
		t.Errorf("the status bar does not show the sum: %v", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); got != "100\tx1\n200\tx2\n300\tx3\n400\tx4\n500\tx5" {
		t.Errorf("copied %q", got)
	}

	// The cell menu copies too, and offers no INSERT or sort: a query's
	// result has no table behind it.
	tt.SetClipboard("")
	if err := tt.RightClick("300"); err != nil {
		t.Fatal(err)
	}
	menu := strings.Join(tt.Menu(), "|")
	if strings.Contains(menu, "INSERT") || strings.Contains(menu, "Sort") || strings.Contains(menu, "Edit cell") {
		t.Errorf("a query result's menu: %s", menu)
	}
	if err := tt.ChooseMenuItem("Copy"); err != nil {
		t.Fatalf("copy: %v (menu %v)", err, tt.Menu())
	}
	if tt.Clipboard() == "" {
		t.Error("the menu's Copy copied nothing")
	}

	// A header menu hides a column; a click on a header sorts nothing.
	if err := tt.RightClick("s"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Hide column"); err != nil {
		t.Fatalf("hide: %v (menu %v)", err, tt.Menu())
	}
	p.frame(tt)
	if tt.HasText("x3") || !g.Hidden(1) {
		t.Errorf("column s still shows: hidden %v", g.Hidden(1))
	}
}

// Past the window the columns are not built, and the row numbers stay.
func TestLiveWideResultVirtualises(t *testing.T) {
	var cols []string
	for i := 1; i <= 150; i++ {
		cols = append(cols, fmt.Sprintf("'v%d' as c%d", i, i))
	}
	_, _, core, p, tt := livePGWindow(t, "select "+strings.Join(cols, ", "))
	runQuery(t, core, p, tt, "v1")
	// The first frame of a list has no width to virtualise by.
	p.frame(tt)
	p.frame(tt)
	if hasExact(tt, "v150") {
		t.Fatal("all 150 columns were built")
	}
	num, ok := tt.Find("1")
	if !ok {
		t.Fatalf("no row number: %v", tt.Texts())
	}
	cell, _ := tt.Find("v1")
	tt.Scroll(cell.X+20, cell.Y+5, 30000, 0)
	p.frame(tt)
	if !hasExact(tt, "v150") || hasExact(tt, "v1") {
		t.Errorf("after scrolling right: v150 %v, v1 %v", hasExact(tt, "v150"), hasExact(tt, "v1"))
	}
	if after, ok := tt.Find("1"); !ok || after.X != num.X || !hasExact(tt, "1") {
		t.Errorf("the row number moved or went: %v -> %v (%v)", num, after, ok)
	}
}

// hasExact reports a text that is s and nothing longer: HasText matches
// parts, and v1 is part of v150.
func hasExact(tt *ui.Tester, s string) bool {
	for _, text := range tt.Texts() {
		if text == s {
			return true
		}
	}
	return false
}

func seedViewTable(t *testing.T, p model.Profile, password string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := driver.Open(ctx, p, password, p.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, s := range []string{
		"DROP TABLE IF EXISTS seeker_m4_v",
		"CREATE TABLE seeker_m4_v (id integer PRIMARY KEY, name text, score integer)",
		"INSERT INTO seeker_m4_v VALUES (1,'n1',10),(2,'n2',20),(3,'n3',30),(4,'n4',40),(5,'n5',50)",
	} {
		if res := conn.Execute(ctx, s, 10, nil); len(res) != 1 || res[0].Err != nil {
			t.Fatalf("seed %q: %+v", s, res)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if c, err := driver.Open(ctx, p, password, p.Database); err == nil {
			c.Execute(ctx, "DROP TABLE IF EXISTS seeker_m4_v", 10, nil)
			c.Close()
		}
	})
}

// A table tab in the window: filter by Enter, ⌘L, count, sort by key, edit
// a cell and write it, and the INSERT of a row.
func TestLiveTableTabWindow(t *testing.T) {
	profile, password, core, p, tt := livePGWindow(t, "select 1")
	seedViewTable(t, profile, password)
	core.OpenTable(profile.ID, profile.Database, "public", "seeker_m4_v")
	p.waitUntil(t, tt, "the rows", func() bool { return tt.HasText("n5") })
	tab, _ := core.ActiveTab()
	tv := core.TableOf(tab.ID)
	if !hasAny(tt, "5 rows") || !tt.HasText("Count") || !tt.HasText("100 rows") {
		t.Fatalf("table tab: %v", tt.Texts())
	}

	// ⌘L goes to WHERE, Enter applies it.
	tt.Key(ui.Cmd, ui.KeyL)
	p.frame(tt)
	tt.Type("id <= 3")
	tt.Key(0, ui.KeyEnter)
	p.waitUntil(t, tt, "the filtered rows", func() bool { return tv.Where == "id <= 3" && !tv.Loading && !tt.HasText("n4") })
	if !tt.HasText("n3") {
		t.Fatalf("filtered: %v", tt.Texts())
	}
	if err := tt.Click("Count"); err != nil {
		t.Fatal(err)
	}
	p.waitUntil(t, tt, "the count", func() bool { return hasAny(tt, "3 in total") })

	// ⌃⇧↓ sorts by the column the cursor is on, writing the ORDER BY.
	if err := tt.Click("n2"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyDown)
	p.waitUntil(t, tt, "the sort", func() bool { return strings.Contains(tv.OrderBy, "DESC") && !tv.Loading })
	if !strings.Contains(tv.OrderBy, "name") || tv.Grid.Text(0, 1) != "n3" {
		t.Errorf("sorted by name: %q, first %q", tv.OrderBy, tv.Grid.Text(0, 1))
	}
	// Two clicks in a row on one place are a double click, which edits.
	tt.Advance(time.Second)

	// Enter on a cell edits it; Enter stages, Save writes.
	if err := tt.Click("n2"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnter)
	p.frame(tt)
	if tv.Grid.Editing == nil {
		t.Fatalf("Enter did not open the editor: %v", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("zzz")
	tt.Key(0, ui.KeyEnter)
	p.frame(tt)
	if tv.Grid.Pending() != 1 || !tt.HasText("zzz") || !hasAny(tt, "1 edits") {
		t.Fatalf("staged: pending %d, %v", tv.Grid.Pending(), tt.Texts())
	}
	old := tv.Grid
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	p.waitUntil(t, tt, "the write", func() bool { return tv.Grid != old && !tv.Loading && !tv.Committing() })
	found := false
	for r := range tv.Grid.Rows {
		found = found || tv.Grid.Text(r, 1) == "zzz"
	}
	if !found || tv.Grid.Pending() != 0 {
		t.Errorf("after save: %v", tt.Texts())
	}

	// A row as INSERT.
	if err := tt.RightClick("n3"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Copy row as INSERT"); err != nil {
		t.Fatalf("insert: %v (menu %v)", err, tt.Menu())
	}
	if got := tt.Clipboard(); !strings.Contains(got, `INSERT INTO "public"."seeker_m4_v"`) || !strings.Contains(got, "'n3'") {
		t.Errorf("INSERT: %q", got)
	}
}

// A value the column cuts shows in full in the box under the grid when its
// cell has the cursor; a short one needs no box.
func TestLiveValueBox(t *testing.T) {
	long := strings.Repeat("abcdefghij", 30)
	_, _, core, p, tt := livePGWindow(t, "select 'short' as a, '"+long+"' as b")
	runQuery(t, core, p, tt, "short")
	count := func(s string) int {
		n := 0
		for _, text := range tt.Texts() {
			if text == s {
				n++
			}
		}
		return n
	}
	if err := tt.Click("short"); err != nil {
		t.Fatal(err)
	}
	if count("short") != 1 {
		t.Errorf("a short value has a box: %d", count("short"))
	}
	if err := tt.Click(long); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	if count(long) != 2 {
		t.Errorf("the cut value is %d times on screen, want the cell and the box", count(long))
	}
}

// A header dragged to another place moves its column, and the layout is the
// one a reload keeps.
func TestLiveHeaderDragMovesColumn(t *testing.T) {
	_, _, core, p, tt := livePGWindow(t, "select 'one' as first_col, 'two' as second_col")
	runQuery(t, core, p, tt, "two")
	g := core.GridOf(activeID(core), 0)
	first, ok1 := tt.Find("first_col")
	second, ok2 := tt.Find("second_col")
	if !ok1 || !ok2 {
		t.Fatalf("no headers: %v", tt.Texts())
	}
	tt.Press(second.X+second.W/2, second.Y+second.H/2)
	tt.Move(first.X+2, first.Y+first.H/2)
	tt.Release(first.X+2, first.Y+first.H/2)
	p.frame(tt)
	got := g.List.Columns.Order
	if i, j := slices.Index(got, "c1"), slices.Index(got, "c0"); i < 0 || j < 0 || i > j {
		t.Errorf("order after the drag: %v, want c1 before c0", got)
	}
}

// Table tab odds: ⌘R reads again, the cell menu sorts, Export is on offer.
func TestLiveTableTabMenusAndReload(t *testing.T) {
	profile, password, core, p, tt := livePGWindow(t, "select 1")
	seedViewTable(t, profile, password)
	core.OpenTable(profile.ID, profile.Database, "public", "seeker_m4_v")
	p.waitUntil(t, tt, "the rows", func() bool { return tt.HasText("n5") })
	tab, _ := core.ActiveTab()
	tv := core.TableOf(tab.ID)

	tt.Key(ui.Cmd, ui.KeyR)
	if !tv.Loading {
		t.Error("⌘R did not read the page again")
	}
	p.waitUntil(t, tt, "the reload", func() bool { return !tv.Loading && tt.HasText("n5") })

	if err := tt.RightClick("n4"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Sort ascending"); err != nil {
		t.Fatalf("sort: %v (menu %v)", err, tt.Menu())
	}
	p.waitUntil(t, tt, "the sort", func() bool { return strings.Contains(tv.OrderBy, "ASC") && !tv.Loading })
	if !strings.Contains(tv.OrderBy, `"name"`) {
		t.Errorf("sorted by %q, want the column under the cursor", tv.OrderBy)
	}
	if !tt.HasText("Export") || !tt.HasText("Columns") {
		t.Errorf("no Export or Columns: %v", tt.Texts())
	}
}
