package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"

	"seeker/internal/files"
)

// findDelay waits out typing before ripgrep runs: one search per pause.
const findDelay = 200 * time.Millisecond

// FindInFiles is the Find in Files window (⌘⇧F): a ripgrep search over the
// folders opened in the Files panel. Like JetBrains' popup it keeps its
// query and options across closing, since reopening it is usually looking
// again for the same thing.
type FindInFiles struct {
	Open  bool
	Focus bool
	Query string
	// Mask is the file mask, seeded from the Files panel's filter the first
	// time the window opens.
	Mask                string
	CaseSensitive       bool
	WholeWord, UseRegex bool

	Rows      []files.FindMatch
	Truncated bool
	// Err is ripgrep's own message, for a regex that does not parse.
	Err       string
	Searching bool
	// Row is the chosen row; it returns to the first with each result.
	Row int

	seeded bool
	// gen drops the answer for a search the user has typed past; cancel
	// kills its ripgrep.
	gen    int
	cancel context.CancelFunc
	timer  *time.Timer
}

// FindState is the window's state, edited in place by its fields.
func (a *App) FindState() *FindInFiles { return &a.find }

// ripgrep finds the binary: the one shipped in the app's resources, else
// one next to the executable, else the rg on the PATH (for mygo dev, before
// the bundled one is fetched).
func ripgrep() (string, error) {
	name := "rg"
	var dirs []string
	if res, err := mygo.App.Path(mygo.PathResources); err == nil {
		dirs = append(dirs, res)
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	for _, d := range dirs {
		// The bundle has it flat; a source tree (dev, tests) keeps the
		// fetched binary under its platform's folder.
		for _, p := range []string{filepath.Join(d, name), filepath.Join(d, runtime.GOOS+"-"+runtime.GOARCH, name)} {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
	}
	return exec.LookPath(name)
}

// ripgrepPath is a variable for tests, which point it at what they run.
var ripgrepPath = ripgrep

// OpenFind shows the window, with a search for what it held when closed.
func (a *App) OpenFind() {
	f := &a.find
	if !f.seeded {
		f.seeded = true
		f.Mask = a.files.Filter
		if f.Mask == "" {
			f.Mask = "*"
		}
	}
	f.Open, f.Focus = true, true
	if f.Query != "" && f.Rows == nil && !f.Searching {
		a.FindChanged()
	}
}

// CloseFind hides the window and stops the search in flight.
func (a *App) CloseFind() {
	f := &a.find
	f.Open = false
	a.stopFind()
	f.Searching = false
}

func (a *App) stopFind() {
	f := &a.find
	f.gen++
	if f.timer != nil {
		f.timer.Stop()
	}
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
}

// FindChanged is told that the query, the mask or an option changed: the
// search runs when the typing pauses.
func (a *App) FindChanged() {
	f := &a.find
	a.stopFind()
	if f.Query == "" || len(a.files.Folders) == 0 {
		f.Rows, f.Truncated, f.Err, f.Searching, f.Row = nil, false, "", false, 0
		return
	}
	f.Searching = true
	gen := f.gen
	f.timer = time.AfterFunc(findDelay, func() { a.upd(func() { a.runFind(gen) }) })
}

func (a *App) runFind(gen int) {
	f := &a.find
	if f.gen != gen {
		return
	}
	req := files.FindRequest{Folders: append([]string(nil), a.files.Folders...), Query: f.Query, Mask: f.Mask,
		CaseSensitive: f.CaseSensitive, Regex: f.UseRegex, WholeWord: f.WholeWord}
	ctx, cancel := context.WithCancel(a.ctx)
	f.cancel = cancel
	find := ripgrepPath
	go func() {
		defer cancel()
		rg, err := find()
		var res files.FindResult
		if err != nil {
			err = errNoRipgrep
		} else {
			res, err = files.Find(ctx, rg, req)
		}
		a.upd(func() {
			if f.gen != gen {
				return
			}
			f.Searching, f.Rows, f.Truncated, f.Row, f.Err = false, res.Matches, res.Truncated, 0, ""
			if err != nil {
				f.Err = err.Error()
			}
		})
	}()
}

var errNoRipgrep = errString("ripgrep is not available in this build")

type errString string

func (e errString) Error() string { return string(e) }

// FindMove steps the chosen row, stopping at the ends.
func (a *App) FindMove(delta int) {
	f := &a.find
	if len(f.Rows) > 0 {
		f.Row = min(max(f.Row+delta, 0), len(f.Rows)-1)
	}
}

// FindChoose makes row the chosen one, as a click on it does before opening it.
func (a *App) FindChoose(row int) {
	if row >= 0 && row < len(a.find.Rows) {
		a.find.Row = row
	}
}

// FindActivate opens the chosen hit: its file in a tab, the hit selected.
// An open tab on the file is reused, edits and all.
func (a *App) FindActivate() {
	f := &a.find
	if f.Row < 0 || f.Row >= len(f.Rows) {
		return
	}
	m := f.Rows[f.Row]
	a.CloseFind()
	a.openAt(m.Path, m.Line, m.Col, m.Len)
}

// openAt opens path in a tab and puts the editor's selection on a hit.
func (a *App) openAt(path string, line, col, length int) {
	if err := a.OpenFile(path); err != nil {
		a.toast("Could not open " + filepath.Base(path) + ": " + err.Error())
		return
	}
	// No datasource opens nothing, and says so; the jump has no tab then.
	if t, ok := a.ActiveTab(); ok && t.Path == path {
		a.jump = &jump{tab: t.ID, line: line, col: col, length: length}
	}
}
