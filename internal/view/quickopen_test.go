package view

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/model"
)

// quickWindow is a window with a connection (a file needs one to open on)
// and a folder opened with users.sql and reports/daily.sql.
func quickWindow(t *testing.T) (*app.App, *pump, *ui.Tester) {
	t.Helper()
	profile := model.Profile{ID: "x", Name: "nowhere", DBType: model.Postgres, Host: "127.0.0.1", Port: 1, Username: "u", Database: "d", ConnectTimeoutSecs: 2}
	core, p, tt := editorWindow(t, profile, "", "select 0")
	folder := t.TempDir()
	if err := os.MkdirAll(filepath.Join(folder, "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"users.sql", "reports/daily.sql", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(folder, f), []byte("select 1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	core.AddFolder(folder)
	p.waitUntil(t, tt, "the folder", func() bool { return tt.HasText("users.sql") })
	return core, p, tt
}

// ⌘P opens the palette with the keyboard in its field; typing lists the
// hits, Enter opens the chosen one, and Escape closes it.
func TestQuickOpenByKeyboard(t *testing.T) {
	core, p, tt := quickWindow(t)

	tt.Key(ui.Cmd, ui.KeyP)
	p.frame(tt)
	if !core.QuickState().Open || !tt.HasText("Type to find a table, a file or a connection.") {
		t.Fatalf("not open: %v", tt.Texts())
	}
	tt.Type("sql")
	p.frame(tt)
	if res := core.QuickResults(); len(res) != 2 || !tt.HasText("daily.sql") {
		t.Fatalf("hits %+v, texts %v", res, tt.Texts())
	}

	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("daily")
	p.frame(tt)
	tt.Key(0, ui.KeyEnter)
	p.frame(tt)
	tab, ok := core.ActiveTab()
	if !ok || tab.Path != filepath.Join(core.FileRoots()[0], "reports/daily.sql") || core.QuickState().Open {
		t.Fatalf("tab %+v, open %v", tab, core.QuickState().Open)
	}

	tt.Key(ui.Cmd, ui.KeyP)
	p.frame(tt)
	tt.Key(0, ui.KeyEscape)
	p.frame(tt)
	if core.QuickState().Open {
		t.Error("Escape left it open")
	}
}

// The title bar's field opens it, ↓ moves the choice, and a click on a hit
// opens that one.
func TestQuickOpenClickAndArrows(t *testing.T) {
	core, p, tt := quickWindow(t)

	if err := tt.Click("Search tables, files, connections"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	if !core.QuickState().Open {
		t.Fatal("the title bar field did not open it")
	}
	tt.Type("sql")
	p.frame(tt)
	first := core.QuickResults()[0].Label
	tt.Key(0, ui.KeyDown)
	p.frame(tt)
	if core.QuickState().Row != 1 || core.QuickResults()[core.QuickState().Row].Label == first {
		t.Errorf("↓ left the choice at row %d", core.QuickState().Row)
	}
	// daily.sql sits in a folder the panel has not opened: only the palette shows it.
	if err := tt.Click("daily.sql"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	if tab, ok := core.ActiveTab(); !ok || filepath.Base(tab.Path) != "daily.sql" || core.QuickState().Open {
		t.Errorf("tab %+v, open %v", tab, core.QuickState().Open)
	}
}
