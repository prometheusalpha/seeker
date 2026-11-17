package app

import (
	"path/filepath"
	"strings"
	"time"

	"seeker/internal/model"
)

// revealWait is how long a reveal waits for the tree to load the levels
// above its table: past it the table is not coming (a failed load, a search
// term hiding it) and the request is dropped rather than firing much later.
const revealWait = 20 * time.Second

type reveal struct {
	table Node
	until time.Time
}

// RevealActiveTable opens the connection, database and schema holding the
// active table tab. The table's own row is found by the view once the
// levels load, through TakeReveal; nothing happens for any other tab.
func (a *App) RevealActiveTable() {
	t, ok := a.ActiveTab()
	if !ok || t.Mode != ModeTable {
		return
	}
	a.tree.List.Open.Add(Node{Kind: KindConnection, Conn: t.Conn})
	a.tree.List.Open.Add(Node{Kind: KindDatabase, Conn: t.Conn, DB: t.Database})
	if p, ok := a.Profile(t.Conn); ok && p.DBType == model.Postgres {
		a.tree.List.Open.Add(Node{Kind: KindSchema, Conn: t.Conn, DB: t.Database, Schema: t.Schema})
	}
	a.tree.reveal = &reveal{table: Node{Kind: KindTable, Conn: t.Conn, DB: t.Database, Schema: t.Schema, Table: t.Table}, until: time.Now().Add(revealWait)}
}

// TakeReveal reports whether n is the row a reveal is waiting for, and ends
// the reveal if so. The match ignores the row's kind of table and text,
// which the tab does not know.
func (a *App) TakeReveal(n Node) bool {
	r := a.tree.reveal
	if r == nil {
		return false
	}
	if time.Now().After(r.until) {
		a.tree.reveal = nil
		return false
	}
	if n.Kind != KindTable || tableOf(n) != r.table {
		return false
	}
	a.tree.reveal = nil
	return true
}

// RevealActiveFile opens the folders above the active file tab in the Files
// panel; the view then chooses its row through TakeFileReveal.
func (a *App) RevealActiveFile() {
	t, ok := a.ActiveTab()
	if !ok || t.Mode != ModeQuery || t.Path == "" {
		return
	}
	for _, root := range a.files.Folders {
		if !strings.HasPrefix(t.Path, root+string(filepath.Separator)) {
			continue
		}
		for dir := filepath.Dir(t.Path); ; dir = filepath.Dir(dir) {
			a.files.Out.Open.Add(dir)
			if dir == root {
				break
			}
		}
		a.files.reveal = t.Path
		return
	}
}

// TakeFileReveal reports whether path is the row the Files panel was asked
// to show, and ends the request if so.
func (a *App) TakeFileReveal(path string) bool {
	if a.files.reveal == "" || a.files.reveal != path {
		return false
	}
	a.files.reveal = ""
	return true
}

// RevealPending reports whether a table reveal waits for its row.
func (a *App) RevealPending() bool { return a.tree.reveal != nil }

// FileRevealPending reports whether a file reveal waits for its row.
func (a *App) FileRevealPending() bool { return a.files.reveal != "" }
