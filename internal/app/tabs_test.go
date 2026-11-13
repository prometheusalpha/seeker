package app

import (
	"slices"
	"testing"

	"seeker/internal/model"
)

func tabIDs(a *App) []string {
	var ids []string
	for _, i := range a.VisibleTabs() {
		ids = append(ids, a.tabs.Items[i].ID)
	}
	return ids
}

func TestCancelCloseKeepsCleanTabsToo(t *testing.T) {
	a := newTestApp(t)
	a.tabs.Items = []Tab{{ID: "clean", Mode: ModeQuery}, {ID: "dirty", Mode: ModeQuery, SQL: "x"}}
	a.tabs.Active = 0
	a.CloseAllTabs()
	if !a.dlg.Close.Open || len(a.tabs.Items) != 2 {
		t.Fatalf("dialog %v, tabs %d", a.dlg.Close.Open, len(a.tabs.Items))
	}
	a.CancelClose()
	if len(a.tabs.Items) != 2 {
		t.Errorf("cancel closed tabs: %d left", len(a.tabs.Items))
	}
	a.CloseAllTabs()
	a.ConfirmClose(false)
	if len(a.tabs.Items) != 0 {
		t.Errorf("discard left %d tabs", len(a.tabs.Items))
	}
}

func TestDiscardedEditsDoNotReopen(t *testing.T) {
	a := newTestApp(t)
	a.tabs.Items = []Tab{{ID: "d", Mode: ModeQuery, SQL: "edited", SavedSQL: "saved"}}
	a.tabs.Active = 0
	a.CloseTab("d")
	a.ConfirmClose(false)
	a.ReopenClosedTab()
	if got := a.tabs.Items[0].SQL; got != "saved" {
		t.Errorf("reopened with %q", got)
	}
}

func TestDragAcrossPinnedDividerPins(t *testing.T) {
	a := newTestApp(t)
	a.tabs.Items = []Tab{{ID: "p", Pinned: true}, {ID: "x"}, {ID: "y"}}
	a.tabs.Active = 0
	a.MoveVisible(2, 0) // y into the pinned half
	if !a.tabs.Items[slices.IndexFunc(a.tabs.Items, func(t Tab) bool { return t.ID == "y" })].Pinned {
		t.Error("y was not pinned")
	}
	if got := tabIDs(a); !slices.Equal(got, []string{"y", "p", "x"}) {
		t.Errorf("order %v", got)
	}
}

func TestUnpinRestoresServerOrder(t *testing.T) {
	a := newTestApp(t)
	parent := Node{Kind: KindSchema, Conn: "c", DB: "d", Schema: "s"}
	kids := []Node{}
	for _, n := range []string{"a", "b", "c"} {
		kids = append(kids, Node{Kind: KindTable, Conn: "c", DB: "d", Schema: "s", Table: n})
	}
	a.tree.raw[parent] = slices.Clone(kids)
	a.tree.kids[parent] = kids
	c := kids[2]
	a.TogglePin(c)
	if got := a.tree.kids[parent][0].Table; got != "c" {
		t.Fatalf("pinned table not first: %s", got)
	}
	a.TogglePin(c)
	var got []string
	for _, k := range a.tree.kids[parent] {
		got = append(got, k.Table)
	}
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("after unpin: %v", got)
	}
}

func TestLostConnectionShowsErrorRow(t *testing.T) {
	a := newTestApp(t)
	a.profiles = []model.Profile{{ID: "c", Name: "c"}}
	a.connectionLost("c", "eof")
	n := Node{Kind: KindConnection, Conn: "c"}
	if kids := a.Children(n); len(kids) != 1 || kids[0].Kind != KindError {
		t.Errorf("children %+v", kids)
	}
	if a.Status("c") != Disconnected {
		t.Error("row reconnected on its own")
	}
}
