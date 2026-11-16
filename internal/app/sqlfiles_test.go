package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"seeker/internal/files"
	"seeker/internal/model"
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

// drained is queued for work that posts several updates: it runs them until
// none comes for a moment.
func drained(t *testing.T, a *App) func() {
	t.Helper()
	ch := make(chan func(), 64)
	a.upd = func(fn func()) { ch <- fn }
	return func() {
		t.Helper()
		select {
		case fn := <-ch:
			fn()
		case <-time.After(5 * time.Second):
			t.Fatal("no update arrived")
		}
		for {
			select {
			case fn := <-ch:
				fn()
			case <-time.After(150 * time.Millisecond):
				return
			}
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

// fileWorld is an app with a scanned folder of
//
//	a.sql  sub/b.sql  sub/deep/c.sql
//
// each open in a tab, bound to a connection and bookmarked.
func fileWorld(t *testing.T) (a *App, run func(), root string) {
	t.Helper()
	a = newTestApp(t)
	run = drained(t, a)
	dir := t.TempDir()
	for _, f := range []string{"a.sql", "sub/b.sql", "sub/deep/c.sql"} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("select 1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.AddFolder(dir)
	run()
	root = a.FileRoots()[0]
	for i, f := range []string{"a.sql", "sub/b.sql", "sub/deep/c.sql"} {
		p := filepath.Join(root, f)
		a.tabs.Items = append(a.tabs.Items, Tab{ID: "t" + strconv.Itoa(i), Title: filepath.Base(p), Path: p, SQL: "select 1", SavedSQL: "select 1", Mode: ModeQuery})
		a.fileVer[p] = FileVersion("select 1")
		a.SetFileBinding(p, model.Datasource{Conn: "c1", Database: "d"})
		if _, err := a.db.SetBookmark(a.ctx, strconv.Itoa(i), p, 1, "select 1"); err != nil {
			t.Fatal(err)
		}
	}
	a.loadBookmarks()
	return a, run, root
}

// Renaming a file, or a folder above it, takes the tabs, saved versions,
// bookmarks and bindings along.
func TestRenameFollowsEverything(t *testing.T) {
	a, run, root := fileWorld(t)
	sub := filepath.Join(root, "sub")
	a.RequestRename(sub)
	a.dlg.Files.Name = "renamed"
	a.SubmitFileDialog()
	run() // the rescan

	newC := filepath.Join(root, "renamed", "deep", "c.sql")
	var tab Tab
	for _, tb := range a.tabs.Items {
		if tb.ID == "t2" {
			tab = tb
		}
	}
	if tab.Path != newC || tab.Title != "c.sql" {
		t.Errorf("tab: %+v", tab)
	}
	if _, ok := a.fileVer[newC]; !ok {
		t.Error("the saved version did not follow")
	}
	if _, ok := a.fileVer[filepath.Join(sub, "deep", "c.sql")]; ok {
		t.Error("the old version stayed")
	}
	if ds, ok := a.BindingFor(newC); !ok || ds.Conn != "c1" {
		t.Errorf("binding: %+v %v", ds, ok)
	}
	if _, ok := a.BindingFor(filepath.Join(sub, "deep", "c.sql")); ok {
		t.Error("the old binding stayed")
	}
	got := map[string]string{}
	for _, b := range a.bookmarks {
		got[b.Mnemonic] = b.Path
	}
	if got["2"] != newC || got["1"] != filepath.Join(root, "renamed", "b.sql") || got["0"] != filepath.Join(root, "a.sql") {
		t.Errorf("bookmarks: %v", got)
	}
	if a.FileNode(newC) == nil {
		t.Error("the rescan did not find the renamed file")
	}
}

func TestRenameRefusals(t *testing.T) {
	a, _, root := fileWorld(t)
	a.RequestRename(filepath.Join(root, "a.sql"))
	a.dlg.Files.Name = "sub"
	a.SubmitFileDialog()
	if !a.dlg.Files.Open || a.dlg.Files.Err == "" {
		t.Errorf("a taken name closed the dialog: %+v", a.dlg.Files)
	}
	a.dlg.Files.Name = "../out.sql"
	a.SubmitFileDialog()
	if !a.dlg.Files.Open || a.dlg.Files.Err == "" {
		t.Error("a path was accepted as a name")
	}
	a.RequestRename(root) // an opened folder is not the panel's to rename
	if a.dlg.Files.Kind == FileDialogRename && a.dlg.Files.Target == root {
		t.Error("an opened folder opened the rename dialog")
	}
	a.RequestRename(filepath.Join(os.TempDir(), "elsewhere.sql"))
	if a.dlg.Files.Target == filepath.Join(os.TempDir(), "elsewhere.sql") {
		t.Error("a path outside the folders opened the dialog")
	}
}

// A deleted file goes to the Trash; a tab that had it keeps its text as a
// draft, and the binding is let go.
func TestDeleteDetachesTab(t *testing.T) {
	a, run, root := fileWorld(t)
	var trashed []string
	old := files.Trash
	files.Trash = func(p string) error { trashed = append(trashed, p); return os.Remove(p) }
	t.Cleanup(func() { files.Trash = old })

	p := filepath.Join(root, "a.sql")
	a.RequestDelete(p)
	a.SubmitFileDialog()
	run() // trash, then the rescan
	if len(trashed) != 1 || trashed[0] != p {
		t.Fatalf("trashed %v", trashed)
	}
	for _, tb := range a.tabs.Items {
		if tb.ID == "t0" && (tb.Path != "" || tb.SQL != "select 1") {
			t.Errorf("tab: %+v", tb)
		}
	}
	if _, ok := a.BindingFor(p); ok {
		t.Error("the binding stayed")
	}
	if a.FileNode(p) != nil {
		t.Error("the file is still in the tree")
	}
}

func TestDeleteFailureKeepsState(t *testing.T) {
	a, run, root := fileWorld(t)
	old := files.Trash
	files.Trash = func(string) error { return errors.New("no Finder") }
	t.Cleanup(func() { files.Trash = old })
	p := filepath.Join(root, "a.sql")
	a.RequestDelete(p)
	a.SubmitFileDialog()
	run()
	if _, ok := a.BindingFor(p); !ok || a.tabs.Items[0].Path != p {
		t.Error("a failed delete let go of the file")
	}
	if len(a.TakeToasts()) == 0 {
		t.Error("a failed delete said nothing")
	}
}

func TestNewEntry(t *testing.T) {
	a, run, root := fileWorld(t)
	a.RequestNewEntry(root, true)
	a.dlg.Files.Name = "fresh"
	a.SubmitFileDialog()
	run()
	if n := a.FileNode(filepath.Join(root, "fresh")); n == nil || !n.IsDir {
		t.Errorf("folder: %+v", n)
	}
	a.RequestNewEntry(root, true)
	a.dlg.Files.Name = "fresh"
	a.SubmitFileDialog()
	if a.dlg.Files.Err == "" {
		t.Error("a folder was made over a folder")
	}
}

// Cut then paste moves the entry and everything that pointed at it; copy
// leaves the source and its bookkeeping where they were.
func TestPaste(t *testing.T) {
	a, run, root := fileWorld(t)
	src, dest := filepath.Join(root, "a.sql"), filepath.Join(root, "sub", "deep")

	a.CopyFiles(src)
	a.PasteFiles(dest)
	run()
	if _, err := os.Stat(filepath.Join(dest, "a.sql")); err != nil {
		t.Fatal("the copy did not land")
	}
	if a.tabs.Items[0].Path != src {
		t.Error("a copy moved the tab")
	}

	// Pasting again: the name is taken in the destination.
	a.PasteFiles(dest)
	run()
	if ts := a.TakeToasts(); len(ts) != 1 {
		t.Errorf("a refused paste: %+v", ts)
	}

	if err := os.Remove(filepath.Join(dest, "a.sql")); err != nil {
		t.Fatal(err)
	}
	a.CutFiles(src)
	a.PasteFiles(dest)
	run()
	moved := filepath.Join(dest, "a.sql")
	if _, err := os.Stat(src); err == nil {
		t.Error("a cut left the source")
	}
	if a.tabs.Items[0].Path != moved {
		t.Errorf("tab path %q, want %q", a.tabs.Items[0].Path, moved)
	}
	if _, ok := a.BindingFor(moved); !ok {
		t.Error("the binding did not follow the move")
	}
	if a.CanPaste() {
		t.Error("the clipboard of a finished cut was kept")
	}
}
