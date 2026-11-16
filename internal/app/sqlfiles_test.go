package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// queued makes the app's off-thread updates wait for the test to run them,
// so the test reads its state from one goroutine only.
func queued(t *testing.T, a *App) func() {
	t.Helper()
	ch := make(chan func(), 16)
	a.upd = func(fn func()) { ch <- fn }
	return func() {
		t.Helper()
		select {
		case fn := <-ch:
			fn()
		case <-time.After(5 * time.Second):
			t.Fatal("no update arrived")
		}
	}
}

func TestFilesPanelScanAndPersist(t *testing.T) {
	a := newTestApp(t)
	run := queued(t, a)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"b.sql", "sub/c.sql", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("select 1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.files.Filter = "*.sql"
	a.AddFolder(dir)
	run()

	root := a.FileRoots()[0]
	if loading, err := a.FolderState(root); loading || err != "" {
		t.Fatalf("state: loading %v, err %q", loading, err)
	}
	kids := a.FileChildren(root)
	if len(kids) != 2 || filepath.Base(kids[0]) != "sub" || filepath.Base(kids[1]) != "b.sql" {
		t.Fatalf("children: %v", kids)
	}
	if got := a.FileChildren(kids[0]); len(got) != 1 || filepath.Base(got[0]) != "c.sql" {
		t.Errorf("sub children: %v", got)
	}
	if a.FileNode(kids[1]) == nil || a.FileNode(filepath.Join(dir, "nope")) != nil {
		t.Error("FileNode lookup")
	}

	// The same folder reached through a link opens once.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	a.AddFolder(link)
	run()
	if len(a.FileRoots()) != 1 {
		t.Errorf("folders: %v", a.FileRoots())
	}

	a.saves.flush()
	var fs filesSettings
	if err := a.db.Setting(a.ctx, settingFiles, &fs); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fs.Folders, a.FileRoots()) || fs.Filter != "*.sql" {
		t.Errorf("saved %+v", fs)
	}

	a.RemoveFolder(root)
	if len(a.FileRoots()) != 0 || a.FileNode(kids[1]) != nil {
		t.Error("RemoveFolder left the folder")
	}
}

func TestFilesPanelFolderErrors(t *testing.T) {
	a := newTestApp(t)
	queued(t, a)
	a.AddFolder(filepath.Join(t.TempDir(), "gone"))
	if len(a.FileRoots()) != 0 || len(a.TakeToasts()) != 1 {
		t.Error("a folder that is not there opened")
	}
	f := filepath.Join(t.TempDir(), "f.sql")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a.AddFolder(f)
	if len(a.FileRoots()) != 0 {
		t.Error("a file opened as a folder")
	}
}
