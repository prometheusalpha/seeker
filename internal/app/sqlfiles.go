package app

import (
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"seeker/internal/files"
)

const (
	settingFiles = "sqlFiles"
	// filterDelay waits out typing in the filter before the folders are
	// scanned again.
	filterDelay = 250 * time.Millisecond
)

// filesSettings is what the Files panel keeps between runs: the folders
// opened and the glob a file name must fit.
type filesSettings struct {
	Folders []string `json:"folders"`
	Filter  string   `json:"filter"`
}

// folderScan is one opened folder as the last scan saw it. index finds a
// node by path, whichever level it sits on.
type folderScan struct {
	nodes   []*files.Node
	index   map[string]*files.Node
	err     string
	loading bool
	gen     int
}

// Files is the Files panel's state. Its outline is keyed by real path: the
// opened folders at the top, then what a scan found under each.
type Files struct {
	Folders []string
	Filter  string
	Out     ui.OutlineState[string]
	Row     int
	// reveal is the file whose row the next build chooses and scrolls to.
	reveal string

	scans       map[string]*folderScan
	clip        fileClip
	filterTimer *time.Timer
	saved       filesSettings
}

func (f *Files) init() {
	f.scans = map[string]*folderScan{}
	f.Row = -1
	f.Out.List.Selected = &f.Row
}

// FilesOutline is the outline the panel shows.
func (a *App) FilesOutline() *ui.OutlineState[string] { return &a.files.Out }

// FilesRow is the row the outline has chosen.
func (a *App) FilesRow() *int { return &a.files.Row }

// FilesFilter is the glob the filter field edits in place.
func (a *App) FilesFilter() *string { return &a.files.Filter }

// FileRoots are the folders opened, in the order they were added.
func (a *App) FileRoots() []string { return a.files.Folders }

// IsFolderRoot reports a path that is an opened folder, not something in one.
func (a *App) IsFolderRoot(path string) bool { return slices.Contains(a.files.Folders, path) }

// FolderState says what a root is doing: whether its scan is running, and
// why it failed, when it did.
func (a *App) FolderState(root string) (loading bool, err string) {
	if s := a.files.scans[root]; s != nil {
		return s.loading, s.err
	}
	return false, ""
}

// FileChildren lists what sits under path, in the order of the scan. A file
// has none, and nil tells the outline it is a leaf, with no arrow; a folder
// with nothing in it gets an empty list, which keeps its arrow.
func (a *App) FileChildren(path string) []string {
	var nodes []*files.Node
	if s := a.files.scans[path]; s != nil && a.IsFolderRoot(path) {
		nodes = s.nodes
	} else if n := a.FileNode(path); n != nil && n.IsDir {
		nodes = n.Children
	} else {
		return nil
	}
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Path
	}
	return out
}

// FileNode is the scanned entry at path, nil when there is none.
func (a *App) FileNode(path string) *files.Node {
	for _, s := range a.files.scans {
		if n := s.index[path]; n != nil {
			return n
		}
	}
	return nil
}

// pickFolder asks for a folder; "" is cancel. The native panel waits for the
// user, so it runs off the window's thread.
var pickFolder = func() (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:             "Open folder of SQL files",
		ButtonLabel:       "Open",
		Directory:         true,
		CreateDirectories: true,
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// ChooseFolder asks for a folder and opens it in the panel.
func (a *App) ChooseFolder() {
	pick := pickFolder
	go func() {
		path, err := pick()
		a.upd(func() {
			if err != nil {
				a.toast("Could not choose a folder: " + err.Error())
				return
			}
			if path != "" {
				a.AddFolder(path)
			}
		})
	}()
}

// AddFolder opens a folder in the panel, by its real path: that is the
// path every entry under it is checked against, and the same folder reached
// through a link must not show twice.
func (a *App) AddFolder(path string) {
	real, err := filepath.EvalSymlinks(path)
	if err == nil {
		var info os.FileInfo
		if info, err = os.Stat(real); err == nil && !info.IsDir() {
			err = os.ErrInvalid
		}
	}
	if err != nil {
		a.toast("Could not open that folder: " + err.Error())
		return
	}
	if a.IsFolderRoot(real) {
		a.Rescan(real)
		return
	}
	a.files.Folders = append(a.files.Folders, real)
	a.files.Out.Open.Add(real)
	a.Rescan(real)
	a.saveFiles()
}

// RemoveFolder closes a folder in the panel; nothing on disk changes.
func (a *App) RemoveFolder(path string) {
	if i := slices.Index(a.files.Folders, path); i >= 0 {
		a.files.Folders = slices.Delete(a.files.Folders, i, i+1)
		delete(a.files.scans, path)
		a.saveFiles()
	}
}

// Rescan reads a folder again, off the window's thread. A scan that
// finishes after a newer one began is dropped.
func (a *App) Rescan(root string) {
	s := a.files.scans[root]
	if s == nil {
		s = &folderScan{}
		a.files.scans[root] = s
	}
	s.loading = true
	s.gen++
	gen, filter := s.gen, a.files.Filter
	go func() {
		_, nodes, err := files.Scan(root, filter)
		a.upd(func() {
			s := a.files.scans[root]
			if s == nil || s.gen != gen {
				return
			}
			s.loading = false
			if err != nil {
				s.nodes, s.index, s.err = nil, nil, err.Error()
				return
			}
			s.nodes, s.err = nodes, ""
			s.index = map[string]*files.Node{}
			var walk func([]*files.Node)
			walk = func(ns []*files.Node) {
				for _, n := range ns {
					s.index[n.Path] = n
					walk(n.Children)
				}
			}
			walk(nodes)
		})
	}()
}

// RescanAll reads every opened folder again.
func (a *App) RescanAll() {
	for _, root := range a.files.Folders {
		a.Rescan(root)
	}
}

// FilesFilterChanged rescans once the typing in the filter stops, and saves
// it.
func (a *App) FilesFilterChanged() {
	if a.files.filterTimer != nil {
		a.files.filterTimer.Stop()
	}
	a.files.filterTimer = time.AfterFunc(filterDelay, func() {
		a.upd(func() {
			a.RescanAll()
			a.saveFiles()
		})
	})
}

// ActivateFile acts on a row: a file opens in a tab, a folder opens or
// closes, so one click on its name does what a click on its arrow does.
func (a *App) ActivateFile(path string) {
	if a.IsFolderRoot(path) || a.FileNode(path) != nil && a.FileNode(path).IsDir {
		if a.files.Out.Open.Has(path) {
			a.files.Out.Open.Remove(path)
		} else {
			a.files.Out.Open.Add(path)
		}
		return
	}
	n := a.FileNode(path)
	if n == nil {
		return
	}
	if err := a.OpenFile(path); err != nil {
		a.toast("Could not open " + n.Name + ": " + err.Error())
	}
}

// saveFiles writes the panel's settings when they differ from the last ones.
func (a *App) saveFiles() {
	cur := filesSettings{Folders: slices.Clone(a.files.Folders), Filter: a.files.Filter}
	if slices.Equal(cur.Folders, a.files.saved.Folders) && cur.Filter == a.files.saved.Filter {
		return
	}
	a.files.saved = cur
	a.saves.later(settingFiles, func() {
		if err := a.db.SetSetting(a.ctx, settingFiles, cur); err != nil {
			a.upd(func() { a.toast("Could not save the folders: " + err.Error()) })
		}
	})
}
