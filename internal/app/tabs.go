package app

import (
	"crypto/rand"
	"slices"

	"seeker/internal/model"
)

// TabMode tells what a tab shows.
type TabMode string

const (
	// ModeQuery is a SQL editor tab; ModeTable a table's rows.
	ModeQuery TabMode = "query"
	ModeTable TabMode = "table"
)

// closedTabs bounds the reopen stack, which lives for the session only.
const closedTabs = 20

// Tab is one editor or table tab. SavedSQL is the snapshot the SQL had when
// it was last saved or loaded, so dirty is derived, never stored.
type Tab struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Conn     string  `json:"connectionId"`
	Database string  `json:"database"`
	Schema   string  `json:"schema"`
	Table    string  `json:"table"`
	Mode     TabMode `json:"mode"`
	SQL      string  `json:"sql"`
	Path     string  `json:"path,omitempty"`
	SavedSQL string  `json:"savedSql"`
	Pinned   bool    `json:"pinned"`
}

type savedTabs struct {
	Tabs        []Tab  `json:"tabs"`
	ActiveTabID string `json:"activeTabId"`
}

type Tabs struct {
	Items  []Tab
	Active int
	// Closed is the stack of tabs closed this session, oldest first.
	// Reopening pops one; recall had no such feature.
	Closed []Tab
	// Pending holds the tabs a close refused to drop for unsaved SQL; the
	// close dialog asks what to do with them.
	Pending []string
	// queued are the clean tabs of the same close, which wait for the
	// dialog's answer so a Cancel keeps every tab.
	queued []string
}

func (t *Tabs) activeID() string {
	if t.Active < 0 || t.Active >= len(t.Items) {
		return ""
	}
	return t.Items[t.Active].ID
}

// Tabs returns the strip's tabs in their raw order; the strip partitions
// pinned ones to the front when it draws.
func (a *App) Tabs() *Tabs { return &a.tabs }

// ActiveTab is the tab the workspace shows, if any.
func (a *App) ActiveTab() (Tab, bool) {
	t := &a.tabs
	if t.Active < 0 || t.Active >= len(t.Items) {
		return Tab{}, false
	}
	return t.Items[t.Active], true
}

// TabDirty reports whether the tab has SQL it has not saved.
func (a *App) TabDirty(t Tab) bool { return t.SQL != t.SavedSQL }

// TabByID finds a tab.
func (a *App) TabByID(id string) (Tab, bool) {
	i := slices.IndexFunc(a.tabs.Items, func(t Tab) bool { return t.ID == id })
	if i < 0 {
		return Tab{}, false
	}
	return a.tabs.Items[i], true
}

// Activate shows the tab, opening its connection when that is not open
// yet: a tab restored from the store must not fail with "no open
// connection".
func (a *App) Activate(id string) {
	i := slices.IndexFunc(a.tabs.Items, func(t Tab) bool { return t.ID == id })
	if i < 0 {
		return
	}
	a.tabs.Active = i
	a.ensureTabConnected(a.tabs.Items[i])
}

func (a *App) ensureTabConnected(t Tab) {
	if t.Conn == "" {
		return
	}
	if a.Status(t.Conn) != Connected {
		a.Connect(t.Conn)
	}
}

// SetTabSQL takes the editor's buffer for the tab, which is what makes it
// dirty.
func (a *App) SetTabSQL(id, sql string) {
	if i := a.indexOf(id); i >= 0 {
		a.tabs.Items[i].SQL = sql
	}
}

// MarkSaved takes the tab's SQL as what is on disk.
func (a *App) MarkSaved(id string) {
	if i := a.indexOf(id); i >= 0 {
		a.tabs.Items[i].SavedSQL = a.tabs.Items[i].SQL
	}
}

// Rename retitles a tab; an empty title keeps the old one.
func (a *App) Rename(id, title string) {
	if i := a.indexOf(id); i >= 0 && title != "" {
		a.tabs.Items[i].Title = title
	}
}

// ToggleTabPin moves a tab between the strip's pinned and unpinned halves.
func (a *App) ToggleTabPin(id string) {
	if i := a.indexOf(id); i >= 0 {
		a.tabs.Items[i].Pinned = !a.tabs.Items[i].Pinned
	}
}

func (a *App) indexOf(id string) int {
	return slices.IndexFunc(a.tabs.Items, func(t Tab) bool { return t.ID == id })
}

// OpenTable shows a table's rows, reusing its tab when one is open: a table
// tab is its connection, database, schema and name.
func (a *App) OpenTable(connID, database, schema, table string) {
	if i := a.findTab(Tab{Conn: connID, Database: database, Schema: schema, Table: table, Mode: ModeTable}, ""); i >= 0 {
		a.tabs.Active = i
		a.ensureTabConnected(a.tabs.Items[i])
		return
	}
	t := Tab{ID: rand.Text(), Title: table, Conn: connID, Database: database, Schema: schema, Table: table, Mode: ModeTable}
	a.tabs.Items = append(a.tabs.Items, t)
	a.tabs.Active = len(a.tabs.Items) - 1
	a.ensureTabConnected(t)
}

// QueryTab is what opening a SQL tab takes: the target, and the text a file
// brings with it.
type QueryTab struct {
	Conn, Database, Schema, Path, Title, SQL string
	// ForceNew keeps a DDL preview or an empty tab from overwriting the
	// tab already on that target.
	ForceNew bool
}

// OpenQueryTab shows a SQL tab, reusing the one on the same target or file.
// A file tab is identified by its path alone, so two files bound to one
// connection stay separate and a file never lands in a scratch tab; the
// tab keeps its edits when reopened.
func (a *App) OpenQueryTab(o QueryTab) {
	if !o.ForceNew {
		if i := a.findTab(Tab{Conn: o.Conn, Database: o.Database, Schema: o.Schema, Mode: ModeQuery}, o.Path); i >= 0 {
			t := &a.tabs.Items[i]
			t.Conn, t.Database, t.Schema = o.Conn, o.Database, o.Schema
			a.tabs.Active = i
			a.ensureTabConnected(*t)
			return
		}
	}
	t := Tab{ID: rand.Text(), Title: o.Title, Conn: o.Conn, Database: o.Database, Schema: o.Schema,
		Mode: ModeQuery, SQL: o.SQL, SavedSQL: o.SQL, Path: o.Path}
	if t.Title == "" {
		t.Title = "Query"
	}
	a.tabs.Items = append(a.tabs.Items, t)
	a.tabs.Active = len(a.tabs.Items) - 1
	a.ensureTabConnected(t)
}

// NewQueryTab opens an empty tab on the active SQL connection, else the
// first one, as recall's toolbar does.
func (a *App) NewQueryTab() {
	p, ok := a.queryProfile()
	if !ok {
		a.toast("Add a SQL connection first")
		return
	}
	schema := p.DefaultSchema
	if p.DBType == model.MySQL {
		schema = p.Database
	}
	if schema == "" && p.DBType == model.Postgres {
		schema = "public"
	}
	a.OpenQueryTab(QueryTab{Conn: p.ID, Database: p.Database, Schema: schema, Title: "Query", ForceNew: true})
}

func (a *App) queryProfile() (model.Profile, bool) {
	if t, ok := a.ActiveTab(); ok {
		if p, ok := a.Profile(t.Conn); ok && p.DBType.IsSQL() {
			return p, true
		}
	}
	for _, p := range a.profiles {
		if p.DBType.IsSQL() {
			return p, true
		}
	}
	return model.Profile{}, false
}

// findTab is recall's identity rule: a path identifies a tab by itself;
// otherwise the target does.
func (a *App) findTab(target Tab, path string) int {
	for i, t := range a.tabs.Items {
		if path != "" {
			if t.Path == path {
				return i
			}
			continue
		}
		if t.Path != "" {
			continue
		}
		if t.Conn == target.Conn && t.Database == target.Database && t.Schema == target.Schema &&
			t.Mode == target.Mode && t.Table == target.Table {
			return i
		}
	}
	return -1
}

// CloseTab closes one tab, asking first when its SQL is unsaved.
func (a *App) CloseTab(id string) { a.requestClose([]string{id}) }

// CloseOtherTabs closes every tab but id, after showing it.
func (a *App) CloseOtherTabs(id string) {
	if a.indexOf(id) < 0 {
		return
	}
	a.Activate(id)
	var ids []string
	for _, t := range a.tabs.Items {
		if t.ID != id {
			ids = append(ids, t.ID)
		}
	}
	a.requestClose(ids)
}

// CloseAllTabs closes every tab, asking once for all that are dirty.
func (a *App) CloseAllTabs() {
	ids := make([]string, len(a.tabs.Items))
	for i, t := range a.tabs.Items {
		ids[i] = t.ID
	}
	a.requestClose(ids)
}

func (a *App) requestClose(ids []string) {
	a.tabs.Pending, a.tabs.queued = nil, nil
	for _, id := range ids {
		t, ok := a.TabByID(id)
		if !ok {
			continue
		}
		if a.TabDirty(t) {
			a.tabs.Pending = append(a.tabs.Pending, id)
		} else {
			a.tabs.queued = append(a.tabs.queued, id)
		}
	}
	if len(a.tabs.Pending) > 0 {
		a.dlg.Close.Open = true
		return
	}
	a.dlg.Close.Open = false
	for _, id := range a.tabs.queued {
		a.forceClose(id)
	}
	a.tabs.queued = nil
}

// forceClose drops the tab, closes the query session it held, and leaves
// the choice on the tab that took its place in the raw order, else the
// previous one.
func (a *App) forceClose(id string) {
	i := a.indexOf(id)
	if i < 0 {
		return
	}
	a.tabs.Closed = append(a.tabs.Closed, a.tabs.Items[i])
	if len(a.tabs.Closed) > closedTabs {
		a.tabs.Closed = a.tabs.Closed[1:]
	}
	a.tabs.Items = slices.Delete(a.tabs.Items, i, i+1)
	a.pl.CloseTab(id)
	a.Stop(id)
	a.cancelAutosave(id)
	delete(a.saveBlocked, id)
	delete(a.editors, id)
	if tv := a.tables[id]; tv != nil && tv.cancel != nil {
		tv.cancel()
	}
	delete(a.tables, id)
	switch {
	case len(a.tabs.Items) == 0:
		a.tabs.Active = -1
	case a.tabs.Active > i:
		a.tabs.Active--
	case a.tabs.Active == i:
		a.tabs.Active = min(i, len(a.tabs.Items)-1)
	}
}

// ConfirmClose answers the close dialog: Save keeps each tab's SQL as its
// snapshot without writing a file, as recall does, and Discard drops the
// edits.
func (a *App) ConfirmClose(save bool) {
	pending := a.tabs.Pending
	ids := append(slices.Clone(a.tabs.queued), pending...)
	a.tabs.Pending, a.tabs.queued, a.dlg.Close.Open = nil, nil, false
	for _, id := range ids {
		if slices.Contains(pending, id) {
			if save {
				a.MarkSaved(id)
			} else if i := a.indexOf(id); i >= 0 {
				// Discarded edits must not come back with Reopen.
				a.tabs.Items[i].SQL = a.tabs.Items[i].SavedSQL
			}
		}
		a.forceClose(id)
	}
}

// CancelClose keeps the tabs the dialog asked about.
func (a *App) CancelClose() {
	a.tabs.Pending, a.tabs.queued, a.dlg.Close.Open = nil, nil, false
}

// ReopenClosedTab brings back the last closed tab, or shows it when it is
// open again already.
func (a *App) ReopenClosedTab() {
	n := len(a.tabs.Closed)
	if n == 0 {
		return
	}
	t := a.tabs.Closed[n-1]
	a.tabs.Closed = a.tabs.Closed[:n-1]
	target := Tab{Conn: t.Conn, Database: t.Database, Schema: t.Schema, Mode: t.Mode, Table: t.Table}
	if i := a.findTab(target, t.Path); i >= 0 {
		a.Activate(a.tabs.Items[i].ID)
		return
	}
	a.tabs.Items = append(a.tabs.Items, t)
	a.tabs.Active = len(a.tabs.Items) - 1
	a.ensureTabConnected(t)
}

// VisibleTabs is the strip's order: pinned tabs first, each half in raw
// order, as recall partitions them when drawing.
func (a *App) VisibleTabs() []int {
	var pinned, rest []int
	for i, t := range a.tabs.Items {
		if t.Pinned {
			pinned = append(pinned, i)
		} else {
			rest = append(rest, i)
		}
	}
	return append(pinned, rest...)
}

// MoveVisible is a drag from one tab to another's place; both are indices
// into VisibleTabs.
func (a *App) MoveVisible(from, to int) {
	vis := a.VisibleTabs()
	if from < 0 || from >= len(vis) || to < 0 || to >= len(vis) || from == to {
		return
	}
	active := a.tabs.activeID()
	ids := make([]string, len(vis))
	for i, raw := range vis {
		ids[i] = a.tabs.Items[raw].ID
	}
	id := ids[from]
	// Dropping a tab across the pinned divider pins or unpins it, which is
	// what the strip's partition would otherwise undo.
	pinTo := a.tabs.Items[vis[to]].Pinned
	ids = slices.Delete(ids, from, from+1)
	ids = slices.Insert(ids, min(to, len(ids)), id)

	byID := make(map[string]Tab, len(a.tabs.Items))
	for _, t := range a.tabs.Items {
		byID[t.ID] = t
	}
	if m := byID[id]; m.Pinned != pinTo {
		m.Pinned = pinTo
		byID[id] = m
	}
	items := make([]Tab, len(ids))
	for i, id := range ids {
		items[i] = byID[id]
	}
	a.tabs.Items = items
	a.tabs.Active = slices.IndexFunc(items, func(t Tab) bool { return t.ID == active })
}

// restoreTabs reads the tabs the store kept, keeping only those that can
// still be shown; a tab may point at a connection this app no longer has,
// which shows as unknown instead of failing here.
func (a *App) restoreTabs(saved savedTabs) {
	for _, t := range saved.Tabs {
		switch {
		case t.ID == "":
			continue
		case t.Mode == ModeTable && t.Table == "":
			continue
		case t.Mode != ModeTable && t.Mode != ModeQuery:
			continue
		}
		if t.Title == "" {
			t.Title = "Query"
			if t.Mode == ModeTable {
				t.Title = t.Table
			}
		}
		a.tabs.Items = append(a.tabs.Items, t)
	}
	a.tabs.Active = -1
	if i := slices.IndexFunc(a.tabs.Items, func(t Tab) bool { return t.ID == saved.ActiveTabID }); i >= 0 {
		a.tabs.Active = i
	} else if len(a.tabs.Items) > 0 {
		a.tabs.Active = 0
	}
}
