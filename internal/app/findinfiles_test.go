package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"seeker/internal/model"
)

func needRipgrep(t *testing.T) {
	t.Helper()
	if _, err := ripgrepPath(); err != nil {
		if _, err := exec.LookPath("rg"); err != nil {
			t.Skip("ripgrep is not installed")
		}
	}
}

// Typing a query runs one search after the pause; Enter on a hit opens the
// file with the hit selected, in bytes, past a two-byte character.
func TestFindInFilesOpensTheHit(t *testing.T) {
	needRipgrep(t)
	a := newTestApp(t)
	run := queued(t, a)
	dir := t.TempDir()
	path := filepath.Join(dir, "x.sql")
	if err := os.WriteFile(path, []byte("select 1;\nselect 'é', users from t;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.profiles = []model.Profile{{ID: "c1", Name: "c1", DBType: model.Postgres, Database: "d"}}
	a.AddFolder(dir)
	run()
	root := a.FileRoots()[0]
	path = filepath.Join(root, "x.sql")
	a.SetFileBinding(path, model.Datasource{Conn: "c1", Database: "d"})

	a.OpenFind()
	if a.find.Mask != "*" {
		t.Errorf("mask %q: it follows the files filter, which is empty", a.find.Mask)
	}
	a.find.Query = "users"
	a.FindChanged()
	run() // the pause is over: the search starts
	run() // and answers
	if f := a.find; f.Searching || f.Err != "" || len(f.Rows) != 1 || f.Rows[0].Line != 2 {
		t.Fatalf("search: %+v", f)
	}

	// Typing on supersedes: the answer to the first query is dropped.
	a.find.Query = "select"
	a.FindChanged()
	a.find.Query = "nothing-here"
	a.FindChanged()
	run()
	run()
	if len(a.find.Rows) != 0 || a.find.Searching {
		t.Fatalf("a superseded search answered: %+v", a.find.Rows)
	}

	a.find.Query = "users"
	a.FindChanged()
	run()
	run()
	a.FindActivate()
	if a.find.Open {
		t.Error("activating left the window open")
	}
	tab, ok := a.ActiveTab()
	if !ok || tab.Path != path {
		t.Fatalf("active tab: %+v", tab)
	}
	te := a.tabEditor(tab.ID) // the view makes it as the tab shows
	a.SyncEditors()
	if te.st.SelectedText() != "users" {
		t.Fatalf("selection %q", te.st.SelectedText())
	}
	if line := te.st.CursorPos().Line; line != 1 {
		t.Errorf("caret on line %d", line)
	}
}

// Closing stops the search, and a bad regex is the answer, not a crash.
func TestFindInFilesRegexError(t *testing.T) {
	needRipgrep(t)
	a := newTestApp(t)
	run := queued(t, a)
	dir := t.TempDir()
	a.AddFolder(dir)
	run()
	a.OpenFind()
	a.find.Query, a.find.UseRegex = "(", true
	a.FindChanged()
	run()
	run()
	if a.find.Err == "" || a.find.Searching {
		t.Errorf("err %q, searching %v", a.find.Err, a.find.Searching)
	}
	a.find.Query = ""
	a.FindChanged()
	if a.find.Err != "" || a.find.Rows != nil {
		t.Errorf("an empty query left %q, %v", a.find.Err, a.find.Rows)
	}
	a.CloseFind()
}
