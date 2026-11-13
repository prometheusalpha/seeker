package app

import (
	"testing"

	"seeker/internal/driver"
	"seeker/internal/model"
	"seeker/internal/pool"
)

func TestTreeTerm(t *testing.T) {
	cases := []struct{ q, want string }{
		{"", ""},
		{"  users  ", "users"},
		{"app_db.users", "users"},
		{".users", ".users"},
		{"a.b.c", "b.c"},
	}
	for _, c := range cases {
		if got := treeTerm(c.q); got != c.want {
			t.Errorf("treeTerm(%q) = %q, want %q", c.q, got, c.want)
		}
	}
}

func TestDatabaseNodesKeepOwnDatabase(t *testing.T) {
	p := model.Profile{ID: "c", Database: "app", HiddenDatabases: []string{"app", "template1"}}
	nodes := databaseNodes(p, []driver.DatabaseInfo{{Name: "app"}, {Name: "template1"}, {Name: "audit"}})
	var names []string
	for _, n := range nodes {
		names = append(names, n.DB)
	}
	want := []string{"app", "audit"}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Errorf("got %v, want %v", names, want)
	}
}

// The pin order wins over the server's, and unpinned tables keep theirs.
func TestOrderTables(t *testing.T) {
	a := &App{pl: pool.New()}
	a.pins = []model.PinnedTable{{ConnectionID: "c", Database: "d", Schema: "s", Table: "z"}, {ConnectionID: "c", Database: "d", Schema: "s", Table: "y"}}
	a.indexPins()
	kids := []Node{
		{Kind: KindTable, Conn: "c", DB: "d", Schema: "s", Table: "a"},
		{Kind: KindTable, Conn: "c", DB: "d", Schema: "s", Table: "b"},
		{Kind: KindTable, Conn: "c", DB: "d", Schema: "s", Table: "z"},
		{Kind: KindTable, Conn: "c", DB: "d", Schema: "s", Table: "y"},
	}
	a.orderTables(kids)
	var got []string
	for _, k := range kids {
		got = append(got, k.Table)
	}
	want := []string{"z", "y", "a", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestFindTabIsPathFirst(t *testing.T) {
	a := &App{pl: pool.New()}
	a.tabs.Items = []Tab{
		{ID: "scratch", Conn: "c", Database: "d", Schema: "s", Mode: ModeQuery},
		{ID: "file", Conn: "c", Database: "d", Schema: "s", Mode: ModeQuery, Path: "/a.sql"},
	}
	target := Tab{Conn: "c", Database: "d", Schema: "s", Mode: ModeQuery}
	if i := a.findTab(target, ""); i != 0 {
		t.Errorf("scratch tab: got %d, want 0", i)
	}
	if i := a.findTab(target, "/a.sql"); i != 1 {
		t.Errorf("file tab: got %d, want 1", i)
	}
	// A file never lands in a scratch tab, even with the same target.
	if i := a.findTab(target, "/b.sql"); i != -1 {
		t.Errorf("unknown file: got %d, want -1", i)
	}
}

func TestMoveVisibleAcrossPinned(t *testing.T) {
	a := &App{pl: pool.New()}
	a.tabs.Items = []Tab{
		{ID: "a"}, {ID: "b", Pinned: true}, {ID: "c"},
	}
	a.tabs.Active = 2
	// Visible order is b, a, c. Drag a (1) onto b (0).
	a.MoveVisible(1, 0)
	var got []string
	for _, tab := range a.tabs.Items {
		got = append(got, tab.ID)
	}
	// Dropping a on b's place puts it where b was; the raw order is what
	// persists, and the strip partitions pinned first when it draws.
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if a.tabs.Items[a.tabs.Active].ID != "c" {
		t.Errorf("the active tab moved: %q", a.tabs.Items[a.tabs.Active].ID)
	}
}

func TestCloseKeepsNeighbourActive(t *testing.T) {
	a := &App{pl: pool.New()}
	a.tabs.Items = []Tab{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	a.tabs.Active = 1
	a.CloseTab("b")
	if len(a.tabs.Items) != 2 || a.tabs.Items[a.tabs.Active].ID != "c" {
		t.Fatalf("after closing b: %+v, active %d", a.tabs.Items, a.tabs.Active)
	}
	// Closing the last tab falls back to the previous one.
	a.Activate("c")
	a.CloseTab("c")
	if a.tabs.Items[a.tabs.Active].ID != "a" {
		t.Fatalf("after closing c: active %d", a.tabs.Active)
	}
	if len(a.tabs.Closed) != 2 {
		t.Errorf("closed stack: %d, want 2", len(a.tabs.Closed))
	}
}

func TestCloseRefusesDirtyUntilAnswered(t *testing.T) {
	a := &App{pl: pool.New()}
	a.tabs.Items = []Tab{{ID: "a", SQL: "select 1", SavedSQL: ""}}
	a.CloseTab("a")
	if len(a.tabs.Items) != 1 || !a.dlg.Close.Open || len(a.tabs.Pending) != 1 {
		t.Fatalf("dirty tab: items %d, dialog %v, pending %v", len(a.tabs.Items), a.dlg.Close.Open, a.tabs.Pending)
	}
	a.ConfirmClose(true)
	if len(a.tabs.Items) != 0 || a.dlg.Close.Open {
		t.Fatalf("after Save: items %d, dialog %v", len(a.tabs.Items), a.dlg.Close.Open)
	}
	if a.tabs.Closed[0].SavedSQL != "select 1" {
		t.Errorf("Save did not keep the SQL: %+v", a.tabs.Closed[0])
	}
}

func TestReopenRestoresLastClosed(t *testing.T) {
	a := &App{pl: pool.New()}
	a.tabs.Items = []Tab{
		{ID: "a", Conn: "c", Mode: ModeQuery},
		{ID: "b", Conn: "c", Mode: ModeQuery, Table: "b", Title: "b"},
	}
	a.tabs.Active = 1
	a.CloseTab("b")
	a.ReopenClosedTab()
	if len(a.tabs.Items) != 2 || a.tabs.Items[1].ID != "b" || a.tabs.Active != 1 {
		t.Fatalf("after reopen: %+v, active %d", a.tabs.Items, a.tabs.Active)
	}
}

func TestRestoreTabsDropsInvalid(t *testing.T) {
	a := &App{pl: pool.New()}
	a.restoreTabs(savedTabs{Tabs: []Tab{
		{ID: "", Mode: ModeQuery},
		{ID: "x", Mode: "bogus"},
		{ID: "t", Mode: ModeTable, Table: ""},
		{ID: "ok", Mode: ModeQuery, Title: "", SQL: "select 1"},
	}, ActiveTabID: "ok"})
	if len(a.tabs.Items) != 1 || a.tabs.Items[0].ID != "ok" || a.tabs.Active != 0 {
		t.Fatalf("got %+v, active %d", a.tabs.Items, a.tabs.Active)
	}
	if a.tabs.Items[0].Title != "Query" {
		t.Errorf("title: %q", a.tabs.Items[0].Title)
	}
}
