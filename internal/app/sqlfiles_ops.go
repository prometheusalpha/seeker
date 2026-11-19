package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"seeker/internal/files"
)

// FileDialogKind is what the Files panel's dialog asks for.
type FileDialogKind uint8

const (
	FileDialogNewFile FileDialogKind = iota
	FileDialogNewFolder
	FileDialogRename
	FileDialogDelete
)

// FileDialog is the dialog over the Files panel: a name for a new entry or a
// rename, or the word to put entries in the Trash. Target is the folder a
// new entry goes in, or the entry renamed or deleted.
type FileDialog struct {
	Open   bool
	Kind   FileDialogKind
	Target string
	Name   string
	Err    string
	// Focus asks the view to give the name field the keyboard once.
	Focus bool
}

// fileClip is what Cut or Copy took, waiting for a Paste.
type fileClip struct {
	paths []string
	cut   bool
}

// movedPath is where p goes when from is renamed to to: p itself, or an
// entry under it.
func movedPath(p, from, to string) (string, bool) {
	switch {
	case p == from:
		return to, true
	case strings.HasPrefix(p, from+string(filepath.Separator)):
		return to + p[len(from):], true
	}
	return p, false
}

// isManaged reports a path the panel scanned: an opened folder or
// something under one. Every operation starts from such a path, which is
// what keeps a name typed in the panel inside the folders opened.
func (a *App) isManaged(path string) bool {
	return a.IsFolderRoot(path) || a.FileNode(path) != nil
}

// IsFolderRow reports a row that is a folder, an opened one or one in it.
func (a *App) IsFolderRow(path string) bool {
	if a.IsFolderRoot(path) {
		return true
	}
	n := a.FileNode(path)
	return n != nil && n.IsDir
}

// ParentFolder is the folder an entry created, or pasted, from a row goes
// in: the row's own when it is a folder, else the one it is in.
func (a *App) ParentFolder(path string) string {
	if a.IsFolderRow(path) {
		return path
	}
	return filepath.Dir(path)
}

// RequestNewEntry opens the dialog for a new file or folder under parent.
func (a *App) RequestNewEntry(parent string, dir bool) {
	if !a.IsFolderRow(parent) {
		return
	}
	kind := FileDialogNewFile
	if dir {
		kind = FileDialogNewFolder
	}
	a.dlg.Files = FileDialog{Open: true, Kind: kind, Target: parent, Focus: true}
}

// RequestRename opens the dialog to rename an entry; an opened folder's own
// name is not the panel's to change.
func (a *App) RequestRename(path string) {
	if !a.isManaged(path) || a.IsFolderRoot(path) {
		return
	}
	a.dlg.Files = FileDialog{Open: true, Kind: FileDialogRename, Target: path, Name: filepath.Base(path), Focus: true}
}

// RequestDelete opens the dialog that asks before an entry goes to the
// Trash.
func (a *App) RequestDelete(path string) {
	if !a.isManaged(path) || a.IsFolderRoot(path) {
		return
	}
	a.dlg.Files = FileDialog{Open: true, Kind: FileDialogDelete, Target: path}
}

// CancelFileDialog closes the dialog and does nothing.
func (a *App) CancelFileDialog() { a.dlg.Files = FileDialog{} }

// SubmitFileDialog does what the dialog asked. A refusal stays on the
// dialog, next to the field it is about.
func (a *App) SubmitFileDialog() {
	d := &a.dlg.Files
	if !d.Open || !a.isManaged(d.Target) {
		a.CancelFileDialog()
		return
	}
	kind := d.Kind
	switch kind {
	case FileDialogNewFile, FileDialogNewFolder:
		path, err := files.Create(d.Target, d.Name, kind == FileDialogNewFolder)
		if err != nil {
			d.Err = err.Error()
			return
		}
		a.files.Out.Open.Add(d.Target)
		a.rescanContaining(d.Target)
		a.CancelFileDialog()
		if kind == FileDialogNewFile {
			// An empty file has nothing to read; the tab is the point.
			a.openNew(path)
		}
	case FileDialogRename:
		from := d.Target
		to, err := files.Rename(from, d.Name)
		if err != nil {
			d.Err = err.Error()
			return
		}
		a.repath(from, to)
		a.rescanContaining(from)
		a.CancelFileDialog()
	case FileDialogDelete:
		path := d.Target
		a.CancelFileDialog()
		a.trash(path)
	}
}

// openNew opens a file just created.
func (a *App) openNew(path string) {
	if err := a.OpenFile(path); err != nil {
		a.toast("Could not open " + filepath.Base(path) + ": " + err.Error())
	}
}

// trash puts an entry in the Trash, off the window's thread (Finder is
// slow), and lets go of what pointed at it.
func (a *App) trash(path string) {
	trash := files.Trash
	go func() {
		err := trash(path)
		a.upd(func() {
			if err != nil {
				a.toast("Could not move " + filepath.Base(path) + " to the Trash: " + err.Error())
				return
			}
			a.forget(path)
			a.rescanContaining(path)
		})
	}()
}

// forget lets go of everything that pointed at a path now in the Trash, or
// under it. Tabs that had the file close without asking, unsaved edits and
// all, and tabs closed earlier leave the reopen stack: the file they would
// reopen is gone.
func (a *App) forget(path string) {
	under := func(p string) bool { _, ok := movedPath(p, path, ""); return ok }
	for i := len(a.tabs.Items) - 1; i >= 0; i-- {
		if p := a.tabs.Items[i].Path; p != "" && under(p) {
			delete(a.fileVer, p)
			a.dropTab(i)
		}
	}
	a.tabs.Closed = slices.DeleteFunc(a.tabs.Closed, func(t Tab) bool { return t.Path != "" && under(t.Path) })
	before := len(a.bindings)
	a.bindings = slices.DeleteFunc(a.bindings, func(b binding) bool { return under(b.Path) })
	if len(a.bindings) != before {
		a.saveBindings()
	}
	a.loadBookmarks()
	a.dropOpen(path, "")
}

// repath makes everything that named from, or something under it, name where
// it went: the tabs that have the file open, their saved versions, the
// bookmarks, the bindings, the folders open in the panel and the clipboard.
func (a *App) repath(from, to string) {
	for i := range a.tabs.Items {
		t := &a.tabs.Items[i]
		if np, ok := movedPath(t.Path, from, to); ok && t.Path != "" {
			if t.Title == filepath.Base(t.Path) {
				t.Title = filepath.Base(np)
			}
			t.Path = np
		}
	}
	for p, v := range a.fileVer {
		if np, ok := movedPath(p, from, to); ok {
			delete(a.fileVer, p)
			a.fileVer[np] = v
		}
	}
	changed := false
	for i, b := range a.bindings {
		if np, ok := movedPath(b.Path, from, to); ok {
			a.bindings[i].Path, changed = np, true
		}
	}
	if changed {
		a.saveBindings()
	}
	if err := a.db.RepathBookmarks(a.ctx, from, to); err != nil {
		a.toast("Could not move the bookmarks: " + err.Error())
	}
	a.loadBookmarks()
	a.dropOpen(from, to)
	for i, p := range a.files.clip.paths {
		if np, ok := movedPath(p, from, to); ok {
			a.files.clip.paths[i] = np
		}
	}
	for i, f := range a.files.Folders {
		if np, ok := movedPath(f, from, to); ok {
			a.files.Folders[i] = np
			a.saveFiles()
		}
	}
}

// dropOpen carries the panel's open folders from a path to its new one, or,
// with to empty, forgets them.
func (a *App) dropOpen(from, to string) {
	var keys []string
	for k := range a.files.Out.Open.All() {
		if _, ok := movedPath(k, from, to); ok {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		a.files.Out.Open.Remove(k)
		if np, _ := movedPath(k, from, to); to != "" {
			a.files.Out.Open.Add(np)
		}
	}
}

// rescanContaining reads again the opened folders a path is in.
func (a *App) rescanContaining(path string) {
	for _, root := range a.files.Folders {
		if _, ok := movedPath(path, root, root); ok {
			a.Rescan(root)
		}
	}
}

// CopyFiles and CutFiles take entries for a Paste. A cut moves them when
// pasted; nothing changes until then.
func (a *App) CopyFiles(paths ...string) { a.takeFiles(false, paths) }
func (a *App) CutFiles(paths ...string)  { a.takeFiles(true, paths) }

func (a *App) takeFiles(cut bool, paths []string) {
	var ok []string
	for _, p := range paths {
		if a.isManaged(p) && !a.IsFolderRoot(p) {
			ok = append(ok, p)
		}
	}
	a.files.clip = fileClip{paths: ok, cut: cut}
}

// CanPaste reports entries waiting on the clipboard.
func (a *App) CanPaste() bool { return len(a.files.clip.paths) > 0 }

// PasteFiles puts what Cut or Copy took into a folder. Each entry is its own
// outcome: what cannot land is said in one toast, and the rest lands.
func (a *App) PasteFiles(dest string) {
	clip := a.files.clip
	if len(clip.paths) == 0 || !a.IsFolderRow(dest) {
		return
	}
	go func() {
		moved, failures := files.Transfer(clip.paths, dest, clip.cut)
		a.upd(func() {
			for _, m := range moved {
				if clip.cut {
					a.repath(m.From, m.To)
				}
				a.rescanContaining(m.From)
			}
			a.files.Out.Open.Add(dest)
			a.rescanContaining(dest)
			if clip.cut && len(moved) > 0 && len(failures) == 0 {
				a.files.clip = fileClip{}
			}
			if len(failures) > 0 {
				a.toast(pasteFailure(len(moved)+len(failures), failures))
			}
		})
	}()
}

func pasteFailure(total int, failures []files.Failure) string {
	f := failures[0]
	name := filepath.Base(f.Path)
	if len(failures) == 1 && total == 1 {
		return fmt.Sprintf("Could not paste %s: %s", name, f.Message)
	}
	return fmt.Sprintf("%d of %d could not be pasted (%s: %s)", len(failures), total, name, f.Message)
}

// RevealFile shows an entry in Finder.
func (a *App) RevealFile(path string) {
	if !a.isManaged(path) {
		return
	}
	if err := files.Reveal(path); err != nil {
		a.toast("Could not show it in Finder: " + err.Error())
	}
}
