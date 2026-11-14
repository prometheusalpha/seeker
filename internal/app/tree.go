package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"seeker/internal/driver"
	"seeker/internal/model"
)

// Kind is what a sidebar row stands for.
type Kind uint8

const (
	KindConnection Kind = iota
	KindDatabase
	KindSchema
	KindTable
	KindGroup
	KindLeaf
	// KindLoading and KindError are rows the tree makes for a node whose
	// children are on their way, or failed to come.
	KindLoading
	KindError
)

// Group is one of a table's four metadata lists, which load on demand: the
// foreign keys and triggers of a large schema are slow enough that opening
// a table must not fetch them.
type Group string

const (
	GroupColumns    Group = "columns"
	GroupIndexes    Group = "indexes"
	GroupForeignKey Group = "foreignKeys"
	GroupTriggers   Group = "triggers"
)

var groupOrder = []Group{GroupColumns, GroupIndexes, GroupForeignKey, GroupTriggers}

// Node identifies a sidebar row. It is the outline's key, so it stays
// comparable and unique by the path that reaches it, not by a joined
// string: a schema named "a.b" cannot collide with schema "a" and table
// "b", which recall's length-prefixed keys guarded against.
type Node struct {
	Kind   Kind
	Conn   string
	DB     string
	Schema string
	Table  string
	Group  Group
	// Name is a leaf's name; Type a table's kind ("view", …).
	Name string
	Type string
	// The row's own text, built once where the leaf loads: a column's
	// "email (text)", an index's "users_email_key (email)".
	Label string
	Badge string // "Not null" on a column that refuses NULL
	Title string // the row's tooltip
	DDL   string // what Copy DDL writes
	// Message is an error row's text.
	Message string
}

type Tree struct {
	List  ui.OutlineState[Node]
	Row   int
	Query string

	// term is Query parsed into what listTables takes, one dot prefix
	// dropped as recall's does ("mydb.users" searches "users").
	term string
	// gen is bumped by every change that invalidates lists in flight; a
	// result from an older generation is dropped, never written.
	gen     int
	loading map[Node]int // node → generation of the load in flight
	kids    map[Node][]Node
	// raw is each list as the server gave it, which pinning reorders on a
	// copy: sorting kids in place would leave an unpinned table at the top.
	raw  map[Node][]Node
	errs map[Node]string
	// meta holds a table's fetched groups, keyed by the table node. It is
	// not in kids because a group's leaves belong in the group's row, not
	// among the table's children.
	meta    map[Node]map[Group][]Node
	metaErr map[Node]map[Group]string

	// expanding is set while a search term is in the box: new connections,
	// databases and schemas open as they load, but tables never do, or a
	// keystroke would fan out four metadata requests per table.
	expanding   bool
	filterSeq   int
	filterTimer *time.Timer
}

func (t *Tree) init() {
	t.Row = -1
	t.kids = map[Node][]Node{}
	t.raw = map[Node][]Node{}
	t.loading = map[Node]int{}
	t.errs = map[Node]string{}
	t.meta = map[Node]map[Group][]Node{}
	t.metaErr = map[Node]map[Group]string{}
}

// Outline is the sidebar's rows and their choice, which the view binds.
func (a *App) Outline() *ui.OutlineState[Node] { return &a.tree.List }

// TreeRow is the chosen row, -1 for none.
func (a *App) TreeRow() *int { return &a.tree.Row }

// TreeFilter is the search box's text.
func (a *App) TreeFilter() *string { return &a.tree.Query }

// Roots is one row per saved profile; nothing loads until a row opens.
func (a *App) Roots() []Node {
	roots := make([]Node, len(a.profiles))
	for i, p := range a.profiles {
		roots[i] = Node{Kind: KindConnection, Conn: p.ID}
	}
	return roots
}

// Children lists the rows under n, starting the load when it is not cached.
// The outline calls it while building, so it answers at once with a
// placeholder, which the load's result replaces.
func (a *App) Children(n Node) []Node {
	switch n.Kind {
	case KindConnection:
		return a.cachedChildren(n, func() { a.loadConnection(n) })
	case KindDatabase:
		return a.cachedChildren(n, func() { a.loadDatabase(n) })
	case KindSchema:
		return a.cachedChildren(n, func() { a.loadSchema(n) })
	case KindTable:
		return groupNodes(n)
	case KindGroup:
		table := tableOf(n)
		if leaves, ok := a.tree.meta[table][n.Group]; ok {
			return leaves
		}
		if msg, ok := a.tree.metaErr[table][n.Group]; ok {
			return []Node{errorChild(n, msg)}
		}
		if gen, ok := a.tree.loading[n]; ok && gen == a.tree.gen {
			return []Node{loadingChild(n)}
		}
		a.loadGroup(n)
		return []Node{loadingChild(n)}
	}
	return nil
}

func (a *App) cachedChildren(n Node, load func()) []Node {
	if kids, ok := a.tree.kids[n]; ok {
		return kids
	}
	if msg, ok := a.tree.errs[n]; ok {
		return []Node{errorChild(n, msg)}
	}
	if gen, ok := a.tree.loading[n]; ok && gen == a.tree.gen {
		return []Node{loadingChild(n)}
	}
	load()
	return []Node{loadingChild(n)}
}

func loadingChild(parent Node) Node {
	return Node{Kind: KindLoading, Conn: parent.Conn, DB: parent.DB, Schema: parent.Schema, Table: parent.Table, Group: parent.Group}
}

func errorChild(parent Node, msg string) Node {
	n := loadingChild(parent)
	n.Kind, n.Message = KindError, msg
	return n
}

func groupNodes(table Node) []Node {
	out := make([]Node, len(groupOrder))
	for i, g := range groupOrder {
		out[i] = Node{Kind: KindGroup, Conn: table.Conn, DB: table.DB, Schema: table.Schema, Table: table.Table, Group: g}
	}
	return out
}

func tableOf(n Node) Node {
	return Node{Kind: KindTable, Conn: n.Conn, DB: n.DB, Schema: n.Schema, Table: n.Table}
}

// Activate is what a click on a row does, as recall's: a table opens its
// tab, a container opens or closes, a leaf does nothing.
func (a *App) ActivateNode(n Node) {
	switch n.Kind {
	case KindConnection, KindDatabase, KindSchema, KindGroup:
		if a.tree.List.Open.Has(n) {
			a.tree.List.Open.Remove(n)
			// A row that failed or came back empty retries when it opens
			// again, as recall's does, rather than keeping the error it
			// showed before; a row that loaded keeps its lists.
			if _, failed := a.tree.errs[n]; failed {
				a.dropNode(n)
			} else if kids, ok := a.tree.kids[n]; ok && len(kids) == 0 {
				delete(a.tree.kids, n)
			}
		} else {
			a.tree.List.Open.Add(n)
		}
	case KindTable:
		a.OpenTable(n.Conn, n.DB, n.Schema, n.Table)
	}
}

// FilterChanged reads the search box on every keystroke. The generation
// moves at once so a crawl under the old term stops writing, as recall's
// watcher does; the settled term drops the table lists 250 ms later.
func (a *App) FilterChanged() {
	a.tree.term = treeTerm(a.tree.Query)
	a.bump()
	a.tree.filterSeq++
	seq := a.tree.filterSeq
	if a.tree.filterTimer != nil {
		a.tree.filterTimer.Stop()
	}
	a.tree.filterTimer = time.AfterFunc(250*time.Millisecond, func() {
		a.upd(func() {
			if seq != a.tree.filterSeq {
				return
			}
			a.applyFilter()
		})
	})
}

// treeTerm drops what stands before the first dot: the sidebar accepts
// "database.table" as a hint, and the prefix is not part of the search.
func treeTerm(q string) string {
	q = strings.TrimSpace(q)
	if i := strings.IndexByte(q, '.'); i > 0 {
		q = strings.TrimSpace(q[i+1:])
	}
	return q
}

func (a *App) applyFilter() {
	a.bump()
	for n := range a.tree.kids {
		switch n.Kind {
		case KindDatabase, KindSchema, KindTable, KindGroup:
			delete(a.tree.kids, n)
		}
	}
	a.tree.meta = map[Node]map[Group][]Node{}
	a.tree.metaErr = map[Node]map[Group]string{}
	// A group whose leaves were dropped would show as empty, so it closes.
	var groups []Node
	for n := range a.tree.List.Open.All() {
		if n.Kind == KindGroup {
			groups = append(groups, n)
		}
	}
	for _, n := range groups {
		a.tree.List.Open.Remove(n)
	}
	a.tree.expanding = a.tree.term != ""
	if a.tree.expanding {
		for _, p := range a.profiles {
			a.tree.List.Open.Add(Node{Kind: KindConnection, Conn: p.ID})
		}
	}
}

// Reload drops a node's subtree and opens it, so a collapsed database shows
// what it fetched again.
func (a *App) Reload(n Node) {
	a.bump()
	a.dropNode(n)
	a.tree.List.Open.Add(n)
}

// CollapseAll closes every row; the caches stay, so opening one again is
// free.
func (a *App) CollapseAll() { a.tree.List.Open.Clear() }

func (a *App) bump() {
	a.tree.gen++
	a.tree.loading = map[Node]int{}
}

func (a *App) dropConn(id string) {
	a.cat.forget(id)
	for n := range a.tree.kids {
		if n.Conn == id {
			delete(a.tree.kids, n)
		}
	}
	for n := range a.tree.errs {
		if n.Conn == id {
			delete(a.tree.errs, n)
		}
	}
	for n := range a.tree.meta {
		if n.Conn == id {
			delete(a.tree.meta, n)
		}
	}
	for n := range a.tree.metaErr {
		if n.Conn == id {
			delete(a.tree.metaErr, n)
		}
	}
}

func (a *App) dropNode(parent Node) {
	for n := range a.tree.kids {
		if under(parent, n) {
			delete(a.tree.kids, n)
		}
	}
	for n := range a.tree.errs {
		if under(parent, n) {
			delete(a.tree.errs, n)
		}
	}
	for n := range a.tree.meta {
		if under(parent, n) {
			delete(a.tree.meta, n)
		}
	}
	for n := range a.tree.metaErr {
		if under(parent, n) {
			delete(a.tree.metaErr, n)
		}
	}
}

// under reports whether n is parent or lies below it.
func under(parent, n Node) bool {
	if parent.Conn != n.Conn {
		return false
	}
	switch parent.Kind {
	case KindConnection:
		return true
	case KindDatabase:
		return n.DB == parent.DB
	case KindSchema:
		return n.DB == parent.DB && n.Schema == parent.Schema
	case KindTable, KindGroup:
		return n.DB == parent.DB && n.Schema == parent.Schema && n.Table == parent.Table
	}
	return false
}

func (a *App) treeDone(n Node, gen int, kids []Node, err error) {
	a.upd(func() {
		if cur, ok := a.tree.loading[n]; ok && cur == gen {
			delete(a.tree.loading, n)
		}
		if gen != a.tree.gen {
			return
		}
		if err != nil {
			a.tree.errs[n] = shortError(err)
			return
		}
		delete(a.tree.errs, n)
		a.tree.raw[n] = slices.Clone(kids)
		a.orderTables(kids)
		a.tree.kids[n] = kids
		if a.tree.expanding {
			for _, k := range kids {
				if k.Kind == KindDatabase || k.Kind == KindSchema {
					a.tree.List.Open.Add(k)
				}
			}
		}
	})
}

// shortError keeps a server's long message from taking several rows.
func shortError(err error) string {
	msg := strings.Join(strings.Fields(err.Error()), " ")
	if r := []rune(msg); len(r) > 160 {
		return string(r[:157]) + "…"
	}
	return msg
}

func (a *App) loadConnection(n Node) {
	p, ok := a.Profile(n.Conn)
	if !ok {
		a.tree.errs[n] = "This connection no longer exists."
		return
	}
	gen, c := a.tree.gen, a.conn(n.Conn)
	a.tree.loading[n] = gen
	if a.Status(n.Conn) != Connected {
		a.Connect(n.Conn)
	}
	go func() {
		if err := waitConnected(c); err != nil {
			if errors.Is(err, errCancelled) {
				a.upd(func() {
					if a.tree.loading[n] == gen {
						delete(a.tree.loading, n)
					}
				})
				return
			}
			a.treeDone(n, gen, nil, err)
			return
		}
		var dbs []driver.DatabaseInfo
		err := a.pl.Catalog(a.ctx, p.ID, p.Database, func(ctx context.Context, conn driver.Conn) error {
			var e error
			dbs, e = conn.ListDatabases(ctx, "")
			return e
		})
		var kids []Node
		if err == nil {
			kids = databaseNodes(p, dbs)
		}
		a.treeDone(n, gen, kids, err)
	}()
}

// databaseNodes keeps the profile's own database and drops the hidden ones.
func databaseNodes(p model.Profile, dbs []driver.DatabaseInfo) []Node {
	out := make([]Node, 0, len(dbs))
	for _, db := range dbs {
		if db.Name != p.Database && slices.Contains(p.HiddenDatabases, db.Name) {
			continue
		}
		out = append(out, Node{Kind: KindDatabase, Conn: p.ID, DB: db.Name})
	}
	return out
}

func (a *App) loadDatabase(n Node) {
	p, ok := a.Profile(n.Conn)
	if !ok {
		a.tree.errs[n] = "This connection no longer exists."
		return
	}
	gen, term := a.tree.gen, a.tree.term
	a.tree.loading[n] = gen
	go func() {
		var kids []Node
		var err error
		if p.DBType == model.Postgres {
			var schemas []string
			err = a.pl.Catalog(a.ctx, p.ID, n.DB, func(ctx context.Context, conn driver.Conn) error {
				var e error
				schemas, e = conn.ListSchemas(ctx, p.ShowSystemSchemas)
				return e
			})
			for _, s := range schemas {
				kids = append(kids, Node{Kind: KindSchema, Conn: p.ID, DB: n.DB, Schema: s})
			}
		} else {
			kids, err = a.listTables(p.ID, n.DB, n.DB, term)
		}
		a.treeDone(n, gen, kids, err)
	}()
}

func (a *App) loadSchema(n Node) {
	gen, term := a.tree.gen, a.tree.term
	a.tree.loading[n] = gen
	go func() {
		kids, err := a.listTables(n.Conn, n.DB, n.Schema, term)
		a.treeDone(n, gen, kids, err)
	}()
}

func (a *App) listTables(connID, database, schema, term string) ([]Node, error) {
	var tables []driver.TableInfo
	err := a.pl.Catalog(a.ctx, connID, database, func(ctx context.Context, conn driver.Conn) error {
		var e error
		tables, e = conn.ListTables(ctx, schema, term)
		return e
	})
	if err != nil {
		return nil, err
	}
	out := make([]Node, len(tables))
	for i, t := range tables {
		out[i] = Node{Kind: KindTable, Conn: connID, DB: database, Schema: schema, Table: t.Name, Type: t.Type, Title: t.Comment}
	}
	return out, nil
}

func (a *App) loadGroup(n Node) {
	group := n.Group
	table := tableOf(n)
	gen := a.tree.gen
	a.tree.loading[n] = gen
	go func() {
		var leaves []Node
		var err error
		err = a.pl.Catalog(a.ctx, n.Conn, n.DB, func(ctx context.Context, conn driver.Conn) error {
			switch group {
			case GroupColumns:
				cols, e := conn.ListColumns(ctx, n.Schema, n.Table)
				if e != nil {
					return e
				}
				leaves = columnLeaves(n, cols)
			case GroupIndexes:
				var idx []driver.IndexInfo
				var e error
				idx, e = conn.ListIndexes(ctx, n.Schema, n.Table)
				if e != nil {
					return e
				}
				leaves = indexLeaves(n, idx)
			case GroupForeignKey:
				var fks []driver.ForeignKeyInfo
				var e error
				fks, e = conn.ListForeignKeys(ctx, n.Schema, n.Table)
				if e != nil {
					return e
				}
				leaves = foreignKeyLeaves(n, fks)
			case GroupTriggers:
				var trs []driver.TriggerInfo
				var e error
				trs, e = conn.ListTriggers(ctx, n.Schema, n.Table)
				if e != nil {
					return e
				}
				leaves = triggerLeaves(n, trs)
			}
			return nil
		})
		a.upd(func() {
			if cur, ok := a.tree.loading[n]; ok && cur == gen {
				delete(a.tree.loading, n)
			}
			if gen != a.tree.gen {
				return
			}
			if err != nil {
				a.metaErrors(table)[group] = shortError(err)
				return
			}
			delete(a.tree.metaErr[table], group)
			a.metaOf(table)[group] = leaves
		})
	}()
}

func (a *App) metaOf(table Node) map[Group][]Node {
	m := a.tree.meta[table]
	if m == nil {
		m = map[Group][]Node{}
		a.tree.meta[table] = m
	}
	return m
}

func (a *App) metaErrors(table Node) map[Group]string {
	m := a.tree.metaErr[table]
	if m == nil {
		m = map[Group]string{}
		a.tree.metaErr[table] = m
	}
	return m
}

func columnLeaves(parent Node, cols []driver.ColumnInfo) []Node {
	out := make([]Node, len(cols))
	for i, c := range cols {
		title := c.Name + " (" + c.DataType + ")"
		badge := "Nullable"
		if c.Nullable {
			title += " · NULL"
		} else {
			title += " · NOT NULL"
			badge = "Not null"
		}
		if c.PrimaryKey {
			title += " · PRIMARY KEY"
		}
		if c.HasDefault {
			title += " · DEFAULT " + c.Default
		}
		if c.Comment != "" {
			title += " · " + c.Comment
		}
		out[i] = Node{Kind: KindLeaf, Conn: parent.Conn, DB: parent.DB, Schema: parent.Schema, Table: parent.Table,
			Group: parent.Group, Name: c.Name, Label: c.Name + " (" + c.DataType + ")", Badge: badge, Title: title, DDL: c.DDL}
	}
	return out
}

func indexLeaves(parent Node, idx []driver.IndexInfo) []Node {
	out := make([]Node, len(idx))
	for i, x := range idx {
		label := x.Name + " (" + strings.Join(x.Columns, ", ") + ")"
		title := label
		if x.Primary {
			title += " · PRIMARY"
		}
		if x.Unique {
			title += " · UNIQUE"
		}
		if x.Method != "" {
			title += " · " + x.Method
		}
		if x.Condition != "" {
			title += " · WHERE " + x.Condition
		}
		out[i] = Node{Kind: KindLeaf, Conn: parent.Conn, DB: parent.DB, Schema: parent.Schema, Table: parent.Table,
			Group: parent.Group, Name: x.Name, Label: label, Title: title, DDL: x.DDL}
	}
	return out
}

func foreignKeyLeaves(parent Node, fks []driver.ForeignKeyInfo) []Node {
	out := make([]Node, len(fks))
	for i, f := range fks {
		label := f.Name + " → " + f.ReferencedTable + " (" + strings.Join(f.ReferencedColumns, ", ") + ")"
		title := label + " (" + strings.Join(f.Columns, ", ") + ")"
		if f.OnDelete != "" {
			title += " · ON DELETE " + f.OnDelete
		}
		if f.OnUpdate != "" {
			title += " · ON UPDATE " + f.OnUpdate
		}
		out[i] = Node{Kind: KindLeaf, Conn: parent.Conn, DB: parent.DB, Schema: parent.Schema, Table: parent.Table,
			Group: parent.Group, Name: f.Name, Label: label, Title: title}
	}
	return out
}

func triggerLeaves(parent Node, trs []driver.TriggerInfo) []Node {
	out := make([]Node, len(trs))
	for i, t := range trs {
		label := t.Name + " (" + strings.TrimSpace(t.Timing+" "+t.Event) + ")"
		out[i] = Node{Kind: KindLeaf, Conn: parent.Conn, DB: parent.DB, Schema: parent.Schema, Table: parent.Table,
			Group: parent.Group, Name: t.Name, Label: label, Title: t.Statement}
	}
	return out
}

// TogglePin pins or unpins a table; the sidebar then sorts it to the front
// of its siblings, which is how recall shows pins instead of a section of
// their own.
func (a *App) TogglePin(n Node) {
	t := model.PinnedTable{ConnectionID: n.Conn, Database: n.DB, Schema: n.Schema, Table: n.Table}
	_, pinned := a.pinAt[t]
	if err := a.db.SetPinned(a.ctx, t, !pinned); err != nil {
		a.toast("Could not change the pin: " + err.Error())
		return
	}
	if pinned {
		delete(a.pinAt, t)
		a.pins = slices.DeleteFunc(a.pins, func(x model.PinnedTable) bool { return x == t })
	} else {
		a.pinAt[t] = len(a.pins)
		a.pins = append(a.pins, t)
	}
	a.indexPins()
	for n, raw := range a.tree.raw {
		if _, ok := a.tree.kids[n]; ok {
			kids := slices.Clone(raw)
			a.orderTables(kids)
			a.tree.kids[n] = kids
		}
	}
}

// IsPinned reports whether the table's row shows a pin.
func (a *App) IsPinned(n Node) bool {
	if n.Kind != KindTable {
		return false
	}
	_, ok := a.pinAt[model.PinnedTable{ConnectionID: n.Conn, Database: n.DB, Schema: n.Schema, Table: n.Table}]
	return ok
}

func (a *App) indexPins() {
	a.pinAt = make(map[model.PinnedTable]int, len(a.pins))
	for i, p := range a.pins {
		a.pinAt[p] = i
	}
}

// orderTables puts pinned tables first in their own order, the rest as the
// server listed them.
func (a *App) orderTables(kids []Node) {
	if len(kids) < 2 || len(a.pins) == 0 {
		return
	}
	rank := func(n Node) int {
		if n.Kind != KindTable {
			return len(a.pins)
		}
		if i, ok := a.pinAt[model.PinnedTable{ConnectionID: n.Conn, Database: n.DB, Schema: n.Schema, Table: n.Table}]; ok {
			return i
		}
		return len(a.pins)
	}
	slices.SortStableFunc(kids, func(x, y Node) int { return rank(x) - rank(y) })
}

// GenerateDDL reads a table's DDL and opens it in a new SQL tab. ForceNew
// keeps it from overwriting the query tab already open on that target.
func (a *App) GenerateDDL(n Node) {
	c := a.conn(n.Conn)
	if a.Status(n.Conn) != Connected {
		a.Connect(n.Conn)
	}
	go func() {
		if err := waitConnected(c); err != nil {
			a.upd(func() { a.toast(shortError(err)) })
			return
		}
		var ddl string
		err := a.pl.Catalog(a.ctx, n.Conn, n.DB, func(ctx context.Context, conn driver.Conn) error {
			var e error
			ddl, e = conn.TableDDL(ctx, n.Schema, n.Table)
			return e
		})
		a.upd(func() {
			if err != nil {
				a.toast(shortError(err))
				return
			}
			a.OpenQueryTab(QueryTab{Conn: n.Conn, Database: n.DB, Schema: n.Schema, SQL: ddl, ForceNew: true})
		})
	}()
}
