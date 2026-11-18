package app

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo/ui/editor"

	"seeker/internal/model"
)

// jump is a caret placement waiting for the tab's editor to exist.
type jump struct {
	tab  string
	line int // from 1
	// col and length select a hit, in bytes; no length is a caret alone.
	col, length int
}

func (a *App) loadBookmarks() {
	bs, err := a.db.Bookmarks(a.ctx)
	if err != nil {
		a.toast("Could not read the bookmarks: " + err.Error())
		return
	}
	a.bookmarks = bs
}

// reloadBookmarks rereads them after a write, which runs off the window's
// thread; the rows that lost their file are dropped by the store.
func (a *App) reloadBookmarks() {
	bs, err := a.db.Bookmarks(a.ctx)
	a.upd(func() {
		if err != nil {
			a.toast("Could not read the bookmarks: " + err.Error())
			return
		}
		a.bookmarks = bs
	})
}

// BookmarkMnemonics are the characters bound to a line, which the window
// takes Cmd+<character> for. Only those: a shortcut for a character nothing
// is bound to would take Cmd+C from the editor.
func (a *App) BookmarkMnemonics() []string {
	out := make([]string, len(a.bookmarks))
	for i, b := range a.bookmarks {
		out[i] = b.Mnemonic
	}
	return out
}

// GutterBookmark is a bookmarked line as the gutter draws it: where the line
// starts in the buffer, and the character bound to it. The editor's own
// gutter mark is a dot, so the view draws the tag itself.
type GutterBookmark struct {
	Offset   int
	Mnemonic string
}

// GutterBookmarks lists the bookmarks of the tab's file that fall inside its
// text, a line the file has lost since being skipped.
func (a *App) GutterBookmarks(id string) []GutterBookmark {
	te, i := a.editors[id], a.indexOf(id)
	if te == nil || i < 0 || a.tabs.Items[i].Path == "" {
		return nil
	}
	var out []GutterBookmark
	lines := te.st.Buffer.LineCount()
	for _, b := range a.bookmarks {
		if b.Path == a.tabs.Items[i].Path && b.Line >= 1 && b.Line <= lines {
			out = append(out, GutterBookmark{Offset: te.st.Buffer.Offset(editor.Pos{Line: b.Line - 1}), Mnemonic: b.Mnemonic})
		}
	}
	return out
}

// BookmarkOn is the mnemonic bound to a line (from 1) of a file, "" for
// none, for the tooltip of its gutter mark.
func (a *App) BookmarkOn(path string, line int) string {
	for _, b := range a.bookmarks {
		if b.Path == path && b.Line == line {
			return b.Mnemonic
		}
	}
	return ""
}

// SetBookmarkHere binds mnemonic to the caret's line of the tab's file.
// A scratch tab has no file for a bookmark to point at (they live in the
// app's database, never in the file, and cannot outlive it).
func (a *App) SetBookmarkHere(id, mnemonic string) {
	te := a.editors[id]
	t, ok := a.TabByID(id)
	if te == nil || !ok {
		return
	}
	if t.Path == "" {
		a.toast("This tab is not a file. Save it as a .sql file first, then assign again.")
		return
	}
	buf := te.st.Buffer
	line := buf.Pos(te.caret()).Line
	path, text := t.Path, buf.Line(line)
	go func() {
		if _, err := a.db.SetBookmark(a.ctx, mnemonic, path, line+1, text); err != nil {
			a.upd(func() { a.toast("Could not set the bookmark: " + err.Error()) })
			return
		}
		a.reloadBookmarks()
	}()
}

// JumpBookmark opens the file a mnemonic names, if it is not open, shows it
// and puts the caret on the line, found again by its hash when edits moved
// it. An open tab is left as it is, unsaved edits and all.
func (a *App) JumpBookmark(mnemonic string) {
	i := slices.IndexFunc(a.bookmarks, func(b model.Bookmark) bool { return b.Mnemonic == mnemonic })
	if i < 0 {
		return
	}
	path := a.bookmarks[i].Path
	var open *Tab
	if j := slices.IndexFunc(a.tabs.Items, func(t Tab) bool { return t.Path == path }); j >= 0 {
		open = &a.tabs.Items[j]
	}
	var text string
	var tabID string
	if open != nil {
		text, tabID = open.SQL, open.ID
	} else if _, ok := a.ResolveDatasource(path); !ok {
		a.toast("Add a connection first")
		return
	}
	go func() {
		if open == nil {
			b, err := os.ReadFile(path)
			if err != nil {
				a.upd(func() { a.toast("Could not open " + filepath.Base(path) + ": " + err.Error()) })
				return
			}
			text = string(b)
		}
		b, ok, err := a.db.ResolveBookmark(a.ctx, mnemonic, text)
		a.reloadBookmarks()
		a.upd(func() {
			switch {
			case err != nil:
				a.toast("Could not read the bookmark: " + err.Error())
			case !ok:
				a.toast("Bookmark " + mnemonic + " is gone: its file was deleted")
			default:
				a.landJump(tabID, path, text, b.Line)
			}
		})
	}()
}

// landJump shows the tab (opening the file when no tab has it) and queues
// the caret.
func (a *App) landJump(tabID, path, text string, line int) {
	if tabID == "" {
		// The text was read off the window's thread; its version is the
		// token the file's first write is guarded with.
		a.fileVer[path] = FileVersion(text)
		ds, ok := a.ResolveDatasource(path)
		if !ok {
			return
		}
		a.OpenQueryTab(QueryTab{Conn: ds.Conn, Database: ds.Database, Schema: ds.Schema, Path: path, Title: filepath.Base(path), SQL: text})
		if t, ok := a.ActiveTab(); ok {
			tabID = t.ID
		}
	} else {
		a.Activate(tabID)
	}
	a.jump = &jump{tab: tabID, line: line}
}

// applyJump moves the caret once the tab's editor exists.
func (a *App) applyJump() {
	j := a.jump
	if j == nil {
		return
	}
	te := a.editors[j.tab]
	if te == nil {
		return
	}
	a.jump = nil
	if j.length == 0 {
		te.st.GoTo(editor.Pos{Line: j.line - 1, Col: j.col})
		return
	}
	// The hit is read off the file as it was on disk; a tab with edits
	// since is clamped to its line by Offset, not to the hit.
	off := te.st.Buffer.Offset(editor.Pos{Line: j.line - 1, Col: j.col})
	te.st.GoTo(editor.Pos{Line: j.line - 1, Col: j.col}) // centers the line
	te.st.Select(off, off+j.length)
}
