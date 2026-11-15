package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"seeker/internal/driver"
	"seeker/internal/model"
)

// seedTable makes seeker_m4_t with n rows (id from 1, name 'n<id>', score
// id*2) in the profile's database, and drops it with the test.
func seedTable(t *testing.T, p model.Profile, password string, n int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := driver.Open(ctx, p, password, p.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var values []string
	for i := 1; i <= n; i++ {
		values = append(values, fmt.Sprintf("(%d, 'n%d', %d)", i, i, i*2))
	}
	for _, s := range []string{
		"DROP TABLE IF EXISTS seeker_m4_t",
		"CREATE TABLE seeker_m4_t (id integer PRIMARY KEY, name varchar(40), score integer)",
		"INSERT INTO seeker_m4_t (id, name, score) VALUES " + strings.Join(values, ","),
	} {
		if res := conn.Execute(ctx, s, 10, nil); len(res) != 1 || res[0].Err != nil {
			t.Fatalf("seed %q: %+v", s, res)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if c, err := driver.Open(ctx, p, password, p.Database); err == nil {
			c.Execute(ctx, "DROP TABLE IF EXISTS seeker_m4_t", 10, nil)
			c.Close()
		}
	})
}

func (a *App) waitTable(t *testing.T, p *pump, id string) *TableView {
	t.Helper()
	tv := a.TableOf(id)
	p.waitFor(t, "the table to load", func() bool { return !tv.Loading && tv.started })
	return tv
}

func openLiveTable(t *testing.T, a *App, p model.Profile) string {
	t.Helper()
	schema := "public"
	if p.DBType == model.MySQL {
		schema = p.Database
	}
	a.OpenTable(p.ID, p.Database, schema, "seeker_m4_t")
	tab, _ := a.ActiveTab()
	a.TableSync()
	return tab.ID
}

func colTexts(g *ResultGrid, col int) []string {
	var out []string
	for r := range g.Rows {
		out = append(out, g.Text(r, col))
	}
	return out
}

// A table tab reads its page with LIMIT/OFFSET, narrows by WHERE, orders by
// ORDER BY, counts on request, and keeps the grid it had when a clause
// fails. Both engines.
func testLiveTableTab(t *testing.T, profile model.Profile, password string) {
	seedTable(t, profile, password, 250)
	a, p := liveApp(t, profile, password)
	id := openLiveTable(t, a, profile)
	tv := a.waitTable(t, p, id)
	if tv.Err != "" || tv.Grid == nil {
		t.Fatalf("first load: %q", tv.Err)
	}
	g := tv.Grid
	if len(g.Rows) != 100 || !g.CanEdit() || !tv.HasNextPage() {
		t.Fatalf("page 1: %d rows, editable %v, next %v", len(g.Rows), g.CanEdit(), tv.HasNextPage())
	}
	if !equalStrings(g.Target.Keys, []string{"id"}) {
		t.Errorf("keys: %v", g.Target.Keys)
	}

	a.SetTablePage(id, 3)
	tv = a.waitTable(t, p, id)
	if len(tv.Grid.Rows) != 50 || tv.HasNextPage() || tv.Page != 3 {
		t.Fatalf("page 3: %d rows, next %v", len(tv.Grid.Rows), tv.HasNextPage())
	}
	a.SetTablePageSize(id, 500)
	a.waitTable(t, p, id)
	if tv.Page != 1 || len(tv.Grid.Rows) != 250 {
		t.Fatalf("500 per page: page %d, %d rows", tv.Page, len(tv.Grid.Rows))
	}

	// WHERE + ORDER BY, which drop back to page 1 and retire the count.
	a.CountTable(id)
	p.waitFor(t, "the count", func() bool { return tv.HasCount })
	if tv.Count != 250 {
		t.Errorf("count %d, want 250", tv.Count)
	}
	tv.WhereText, tv.OrderText = "id <= 20 AND score > 10", "id DESC"
	a.ApplyTableFilter(id)
	if tv.HasCount {
		t.Error("the count outlived the filter it counted")
	}
	a.waitTable(t, p, id)
	if got := colTexts(tv.Grid, 0); len(got) != 15 || got[0] != "20" || got[14] != "6" {
		t.Fatalf("filtered ids: %v", got)
	}
	a.CountTable(id)
	p.waitFor(t, "the filtered count", func() bool { return tv.HasCount })
	if tv.Count != 15 {
		t.Errorf("filtered count %d, want 15", tv.Count)
	}

	// Sorting by a column writes the ORDER BY field and reads again.
	a.SortTable(id, 2, false)
	a.waitTable(t, p, id)
	if got := colTexts(tv.Grid, 2); got[0] != "12" || !strings.Contains(tv.OrderText, "score") {
		t.Errorf("sorted by score: %v, %q", got[:3], tv.OrderText)
	}

	// A clause the server refuses leaves the rows from before and blames the
	// field that has text.
	before := tv.Grid
	tv.WhereText, tv.OrderText = "no_such_column = 1", ""
	a.ApplyTableFilter(id)
	a.waitTable(t, p, id)
	if tv.Err == "" || tv.ErrTarget != "where" || tv.Grid != before {
		t.Fatalf("bad clause: err %q, blame %q, grid kept %v", tv.Err, tv.ErrTarget, tv.Grid == before)
	}
}

func TestLiveTableTabPostgres(t *testing.T) {
	profile, password := livePG(t)
	testLiveTableTab(t, profile, password)
}

func TestLiveTableTabMySQL(t *testing.T) {
	profile, password := mysqlLive(t)
	testLiveTableTab(t, profile, password)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Staged edits are written by primary key in one transaction, the page is
// read again, and a rejected edit stays staged.
func testLiveCommitEdits(t *testing.T, profile model.Profile, password string) {
	seedTable(t, profile, password, 5)
	a, p := liveApp(t, profile, password)
	id := openLiveTable(t, a, profile)
	tv := a.waitTable(t, p, id)
	// Without an ORDER BY a server may return an updated row anywhere, as
	// Postgres does (it lands last), so the rows are read in key order.
	tv.OrderText = "id"
	a.ApplyTableFilter(id)
	a.waitTable(t, p, id)
	g := tv.Grid
	if g == nil || !g.CanEdit() {
		t.Fatalf("no editable grid: err %q", tv.Err)
	}
	g.StageEdit(1, 1, "renamed", false) // row id 2
	g.StageEdit(1, 2, "", true)
	g.StageEdit(3, 1, "also", false) // row id 4
	a.CommitEdits(id)
	p.waitFor(t, "the write and the reload", func() bool { return !tv.committing && !tv.Loading && tv.Grid != g })
	g = tv.Grid
	if g.Pending() != 0 || g.Text(1, 1) != "renamed" || g.Value(1, 2) != nil || g.Text(3, 1) != "also" || g.Text(0, 1) != "n1" {
		t.Fatalf("after commit: %v %v (pending %d)", colTexts(g, 1), g.Value(1, 2), g.Pending())
	}

	// A value the column refuses keeps every edit staged, and writes none.
	g.StageEdit(0, 2, "not a number", false)
	g.StageEdit(2, 1, "unchanged?", false)
	a.CommitEdits(id)
	p.waitFor(t, "the refusal", func() bool { return !tv.committing })
	if g.Pending() != 2 || tv.Grid != g {
		t.Fatalf("a refused write dropped the edits: pending %d", g.Pending())
	}
	a.ReloadTable(id)
	a.waitTable(t, p, id)
	if got := colTexts(tv.Grid, 1); got[2] != "n3" {
		t.Errorf("a refused write changed row 3: %v", got)
	}
}

func TestLiveCommitEditsPostgres(t *testing.T) {
	profile, password := livePG(t)
	testLiveCommitEdits(t, profile, password)
}

func TestLiveCommitEditsMySQL(t *testing.T) {
	profile, password := mysqlLive(t)
	testLiveCommitEdits(t, profile, password)
}
