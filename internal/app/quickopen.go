package app

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"

	"seeker/internal/driver"
	"seeker/internal/files"
	"seeker/internal/model"
	"seeker/internal/rank"
)

// quickLimit is how many hits the palette lists: past it the query is too
// loose to be worth scrolling.
const quickLimit = 50

// QuickKind is what a hit opens. The order is the tie-break between hits
// that score alike: a table before a file before a connection.
type QuickKind uint8

const (
	QuickTable QuickKind = iota
	QuickFile
	QuickConnection
)

// QuickItem is one candidate of the palette. Key is its identity, which the
// choice follows when the list changes under it.
type QuickItem struct {
	Kind   QuickKind
	Key    string
	Label  string
	Detail string
	// Type is a table's kind ("view", …).
	Type string

	Conn, DB, Schema, Table string
	Path                    string

	demotion int
}

// QuickOpen is the palette (⌘P): tables of every SQL connection,
// the files under the folders opened, and the connections themselves.
type QuickOpen struct {
	Open  bool
	Query string
	// Focus asks the field for the keyboard on the frame the palette opens.
	Focus bool
	Row   int

	sel string // key of the chosen hit
	// moved is set once the user chose a hit by hand for this query; until
	// then the choice stays on the first, whatever lands above it.
	moved  bool
	others []QuickItem // files and connections, as they were when it opened

	// results is what shown (query, version) ranked; ver moves when the
	// candidates do.
	results []QuickItem
	shown   string
	ver     int
	shownV  int
	valid   bool
}

// QuickState is the palette's state, edited in place by its field.
func (a *App) QuickState() *QuickOpen { return &a.quick }

// OpenQuickOpen shows an empty palette. The tables it searches are cached
// per connection for as long as the app runs: a connection is listed the
// first time it is searched, and again only after its profile changes or
// goes, or when that listing failed.
func (a *App) OpenQuickOpen() {
	q := &a.quick
	q.Open, q.Focus, q.Query, q.Row, q.sel, q.moved = true, true, "", 0, "", false
	q.valid = false
	q.others = a.quickOthers()
	q.ver++
	for _, p := range a.profiles {
		if p.DBType.IsSQL() && a.Status(p.ID) == Connected {
			a.warmQuick(p)
		}
	}
}

// CloseQuickOpen hides the palette. A list being read goes on: it fills the
// cache for the next opening.
func (a *App) CloseQuickOpen() { a.quick.Open = false }

// QuickLoading reports a connected connection whose tables are still being
// listed.
func (a *App) QuickLoading() bool {
	for _, p := range a.profiles {
		if e := a.quickTables[p.ID]; e != nil && e.loading && a.Status(p.ID) == Connected {
			return true
		}
	}
	return false
}

func (a *App) quickOthers() []QuickItem {
	var out []QuickItem
	for _, root := range a.files.Folders {
		s := a.files.scans[root]
		if s == nil {
			continue
		}
		base := filepath.Base(root)
		var walk func([]*files.Node)
		walk = func(nodes []*files.Node) {
			for _, n := range nodes {
				if n.IsDir {
					walk(n.Children)
					continue
				}
				rel, err := filepath.Rel(root, n.Path)
				if err != nil {
					rel = n.Name
				}
				out = append(out, QuickItem{Kind: QuickFile, Key: "f|" + n.Path, Label: n.Name,
					Detail: filepath.Join(base, filepath.Dir(rel)), Path: n.Path})
			}
		}
		walk(s.nodes)
	}
	for _, p := range a.profiles {
		out = append(out, QuickItem{Kind: QuickConnection, Key: "c|" + p.ID, Label: p.Name, Conn: p.ID, Detail: kindName(p.DBType)})
	}
	return out
}

// quickParallel bounds the databases of one connection listed at once.
const quickParallel = 16

// quickEntry is one connection's tables as the palette searches them; warm
// once a listing has finished. Items arrive database by database, so a
// search during the first listing already finds what has landed.
type quickEntry struct {
	items   []QuickItem
	loading bool
	warm    bool
}

// warmQuick lists the tables of p unless that was done or is on its way.
// It runs off the window's thread; every write to the entry happens on it.
func (a *App) warmQuick(p model.Profile) {
	e := a.quickTables[p.ID]
	if e == nil {
		e = &quickEntry{}
		a.quickTables[p.ID] = e
	}
	if e.loading || e.warm {
		return
	}
	e.loading, e.items = true, nil
	go func() {
		ok := a.listQuickTables(a.ctx, p, func(hits []QuickItem) {
			a.upd(func() {
				if a.quickTables[p.ID] != e {
					return
				}
				e.items = append(e.items, hits...)
				a.quick.ver++
			})
		})
		a.upd(func() {
			if a.quickTables[p.ID] != e {
				return
			}
			e.loading, e.warm = false, ok
			a.quick.ver++
		})
	}()
}

// quickTablesOf is the cached tables of the connections that are connected.
func (a *App) quickTablesOf() []QuickItem {
	var out []QuickItem
	for _, p := range a.profiles {
		if e := a.quickTables[p.ID]; e != nil && a.Status(p.ID) == Connected {
			out = append(out, e.items...)
		}
	}
	return out
}

// listQuickTables hands found the tables of each database of the
// connection, a few databases at a time, and reports whether any could be
// listed. On Postgres every database is a session of its own, as the
// sidebar treats it: each is opened for its schemas and their tables, and
// all of them are searched, not only the profile's. On MySQL a database is
// a schema. What cannot be listed, a database the user may not enter, is
// left out so it does not blank the rest.
func (a *App) listQuickTables(ctx context.Context, p model.Profile, found func([]QuickItem)) bool {
	home := p.Database
	if home == "" {
		if info, ok := a.ServerInfo(p.ID); ok {
			home = info.CurrentDatabase
		}
	}
	dbs := []string{home}
	_ = a.pl.Catalog(ctx, p.ID, home, func(ctx context.Context, conn driver.Conn) error {
		infos, err := conn.ListDatabases(ctx, "")
		if err != nil {
			return err
		}
		dbs = dbs[:0]
		for _, n := range databaseNodes(p, infos) {
			dbs = append(dbs, n.DB)
		}
		return nil
	})
	var (
		wg     sync.WaitGroup
		listed atomic.Bool
		sem    = make(chan struct{}, quickParallel)
	)
	for _, db := range dbs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			if hits, ok := a.listQuickDatabase(ctx, p, db); ok {
				listed.Store(true)
				found(hits)
			}
		}()
	}
	wg.Wait()
	return listed.Load()
}

// listQuickDatabase is the tables of one database; ok is false when its
// schemas could not be listed at all.
func (a *App) listQuickDatabase(ctx context.Context, p model.Profile, db string) (hits []QuickItem, ok bool) {
	schemas := []string{db}
	if p.DBType == model.Postgres {
		schemas = nil
		if a.pl.Catalog(ctx, p.ID, db, func(ctx context.Context, conn driver.Conn) error {
			var e error
			schemas, e = conn.ListSchemas(ctx, p.ShowSystemSchemas)
			return e
		}) != nil {
			return nil, false
		}
	}
	for _, schema := range schemas {
		var tables []driver.TableInfo
		if a.pl.Catalog(ctx, p.ID, db, func(ctx context.Context, conn driver.Conn) error {
			var e error
			tables, e = conn.ListTables(ctx, schema, "")
			return e
		}) != nil {
			continue
		}
		where := schema
		if p.DBType == model.Postgres {
			where = db + "." + schema
		}
		for _, t := range tables {
			hits = append(hits, QuickItem{Kind: QuickTable, Key: "t|" + p.ID + "|" + db + "|" + schema + "|" + t.Name,
				Label: t.Name, Detail: p.Name + " · " + where, Type: t.Type,
				Conn: p.ID, DB: db, Schema: schema, Table: t.Name, demotion: rank.TableDemotion(schema, t.Name)})
		}
	}
	return hits, true
}

// QuickResults ranks the candidates against the query: a table of a system
// schema sinks below the user's own however well it matches, then the
// better match first, the shorter name inside a tier. The choice stays on
// the same hit when the list changes, and falls to the first when that hit
// is gone. An empty query lists nothing.
func (a *App) QuickResults() []QuickItem {
	q := &a.quick
	if q.valid && q.shown == q.Query && q.shownV == q.ver {
		return q.results
	}
	if q.shown != q.Query {
		q.moved = false
	}
	q.shown, q.shownV, q.valid = q.Query, q.ver, true
	q.results = q.results[:0:0]
	if q.Query != "" {
		type scored struct {
			QuickItem
			score int
		}
		var hits []scored
		for _, list := range [][]QuickItem{a.quickTablesOf(), q.others} {
			for _, it := range list {
				if m, ok := rank.MatchFuzzy(q.Query, it.Label); ok {
					hits = append(hits, scored{it, m.Score})
				}
			}
		}
		slices.SortFunc(hits, func(x, y scored) int {
			return cmp.Or(cmp.Compare(x.demotion, y.demotion), cmp.Compare(x.score, y.score),
				cmp.Compare(x.Kind, y.Kind), cmp.Compare(x.Label, y.Label), cmp.Compare(x.Key, y.Key))
		})
		for _, h := range hits[:min(len(hits), quickLimit)] {
			q.results = append(q.results, h.QuickItem)
		}
	}
	q.Row = 0
	if q.moved {
		q.Row = max(0, slices.IndexFunc(q.results, func(it QuickItem) bool { return it.Key == q.sel }))
	}
	if q.Row < len(q.results) {
		q.sel = q.results[q.Row].Key
	}
	return q.results
}

// QuickMove steps the choice, stopping at the ends.
func (a *App) QuickMove(delta int) {
	q := &a.quick
	res := a.QuickResults()
	if len(res) == 0 {
		return
	}
	q.Row = min(max(q.Row+delta, 0), len(res)-1)
	q.sel, q.moved = res[q.Row].Key, true
}

// QuickChoose makes row the choice, as a click on it does before opening it.
func (a *App) QuickChoose(row int) {
	if res := a.QuickResults(); row >= 0 && row < len(res) {
		a.quick.Row, a.quick.sel, a.quick.moved = row, res[row].Key, true
	}
}

// QuickActivate opens the chosen hit and closes the palette: a table's
// rows, a file in a tab, a new query tab on a connection.
func (a *App) QuickActivate() {
	res := a.QuickResults()
	if a.quick.Row < 0 || a.quick.Row >= len(res) {
		return
	}
	it := res[a.quick.Row]
	a.CloseQuickOpen()
	switch it.Kind {
	case QuickTable:
		a.OpenTable(it.Conn, it.DB, it.Schema, it.Table)
	case QuickFile:
		a.ActivateFile(it.Path)
	case QuickConnection:
		if p, ok := a.Profile(it.Conn); ok && p.DBType.IsSQL() {
			a.newQueryTab(p)
		} else {
			a.Connect(it.Conn)
		}
	}
}
