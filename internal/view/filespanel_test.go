package view

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/model"
	"seeker/internal/testenv"
)

func filesWindow(t *testing.T) (*app.App, *pump, *ui.Tester, string) {
	t.Helper()
	testenv.SQLite(t)
	dir := t.TempDir()
	core, err := app.New(context.Background(), filepath.Join(dir, "seeker.db"), filepath.Join(dir, "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	p := &pump{}
	core.Attach(p.update)
	t.Cleanup(core.Close)
	folder := filepath.Join(dir, "queries")
	if err := os.MkdirAll(filepath.Join(folder, "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"users.sql", "reports/daily.sql", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(folder, f), []byte("select 1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tt := ui.NewTester(New(core).View, 1280, 820)
	p.frame(tt)
	return core, p, tt, folder
}

// The panel stands on the right of the workspace, shows the folder opened
// and the files under it, and its split is draggable from its left edge.
func TestFilesPanel(t *testing.T) {
	core, p, tt, folder := filesWindow(t)
	files, ok := tt.Find("FILES")
	if !ok {
		t.Fatalf("no panel: %v", tt.Texts())
	}
	conns, _ := tt.Find("CONNECTIONS")
	if files.X < 900 || conns.X > 100 {
		t.Errorf("FILES at x=%v, CONNECTIONS at x=%v", files.X, conns.X)
	}

	core.AddFolder(folder)
	p.waitUntil(t, tt, "the folder", func() bool { return tt.HasText("queries") && tt.HasText("users.sql") })
	if !tt.HasText("reports") || !tt.HasText("notes.txt") {
		t.Errorf("tree: %v", tt.Texts())
	}
}

// One click on a folder's name opens it, the next closes it, whether or not
// it was the row chosen.
func TestFilesPanelClickTogglesFolder(t *testing.T) {
	core, p, tt, folder := filesWindow(t)
	core.AddFolder(folder)
	p.waitUntil(t, tt, "the folder", func() bool { return tt.HasText("reports") })
	if tt.HasText("daily.sql") {
		t.Fatal("a closed folder shows its files")
	}
	for i, want := range []bool{true, false, true} {
		if err := tt.Click("reports"); err != nil {
			t.Fatal(err)
		}
		p.frame(tt)
		if got := tt.HasText("daily.sql"); got != want {
			t.Fatalf("click %d: daily.sql shown %v, want %v", i+1, got, want)
		}
	}
}

// One click on a file opens it in a tab, with no mark left on its row.
func TestFilesPanelClickOpensFile(t *testing.T) {
	profile := model.Profile{ID: "x", Name: "nowhere", DBType: model.Postgres, Host: "127.0.0.1", Port: 1, Username: "u", Database: "d", ConnectTimeoutSecs: 2}
	core, p, tt := editorWindow(t, profile, "", "select 0")
	folder := t.TempDir()
	file := filepath.Join(folder, "users.sql")
	if err := os.WriteFile(file, []byte("select 42"), 0o644); err != nil {
		t.Fatal(err)
	}
	core.AddFolder(folder)
	p.waitUntil(t, tt, "the file", func() bool { return tt.HasText("users.sql") })
	if err := tt.Click("users.sql"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	if tab, ok := core.ActiveTab(); !ok || tab.Title != "users.sql" || tab.SQL != "select 42" {
		t.Fatalf("active tab: %+v", tab)
	}
}
