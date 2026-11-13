package app

import (
	"context"
	"path/filepath"
	"testing"

	"seeker/internal/testenv"
)

// TestSettingsAndTabsSurviveRestart is M2's "quitting and coming back keeps
// the tabs and the theme": the store write is debounced, so the test waits
// out the delay before closing as a quit would.
func TestSettingsAndTabsSurviveRestart(t *testing.T) {
	testenv.SQLite(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "seeker.db")
	recall := filepath.Join(dir, "absent-recall.db")

	first, err := New(context.Background(), dbPath, recall)
	if err != nil {
		t.Fatal(err)
	}
	first.Attach(func(fn func()) { fn() })
	first.ui.Theme.Light = "Catppuccin Latte"
	first.ui.Theme.Appearance = "Dark"
	first.ui.SidebarW = 301
	first.ui.HideSidebar = true
	first.tabs.Items = []Tab{
		{ID: "a", Title: "one.sql", Conn: "c", Mode: ModeQuery, SQL: "select 1", SavedSQL: "select 1", Path: "/one.sql"},
		{ID: "b", Title: "users", Conn: "c", Mode: ModeTable, Table: "users", Pinned: true},
	}
	first.tabs.Active = 1
	first.Sync()
	// No wait: a quit inside the save delay must still write.
	first.Close()

	second, err := New(context.Background(), dbPath, recall)
	if err != nil {
		t.Fatal(err)
	}
	second.Attach(func(fn func()) { fn() })
	t.Cleanup(second.Close)

	if got := second.ui.Theme.Light; got != "Catppuccin Latte" {
		t.Errorf("light theme: %q", got)
	}
	if got := second.ui.Theme.Appearance; got != "Dark" {
		t.Errorf("appearance: %q", got)
	}
	if second.ui.SidebarW != 301 || !second.ui.HideSidebar {
		t.Errorf("layout: %+v", second.ui)
	}
	if len(second.tabs.Items) != 2 {
		t.Fatalf("tabs: %+v", second.tabs.Items)
	}
	if second.tabs.Items[0].ID != "a" || second.tabs.Items[0].Path != "/one.sql" {
		t.Errorf("query tab: %+v", second.tabs.Items[0])
	}
	if !second.tabs.Items[1].Pinned || second.tabs.Active != 1 {
		t.Errorf("table tab: %+v, active %d", second.tabs.Items[1], second.tabs.Active)
	}
}
