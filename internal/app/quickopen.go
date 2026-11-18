package app

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	// Hits are the runes of Label the query matched, for highlighting; empty
	// when there is no query.
	Hits []int

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

// OpenQuickOpen shows the palette, with every candidate listed. The tables
// are the cache's, so a search has hits at once; a connection whose list is
// stale or incomplete is read again behind it (see warmQuick).
func (a *App) OpenQuickOpen() {
	q := &a.quick
	q.Open, q.Focus, q.Query, q.Row, q.sel, q.moved = true, true, "", 0, "", false
	q.valid = false
	q.others = a.quickOthers()
	q.ver++
	a.QuickTyped()
}

// QuickTyped is told of a keystroke in the field, and refreshes the lists
// gone stale since the palette opened: a palette left open for a while must
// not search a list as old as its opening.
func (a *App) QuickTyped() {
	for _, p := range a.profiles {
		if p.DBType.IsSQL() && a.Status(p.ID) == Connected {
			a.warmQuick(p)
		}
	}
}

// CloseQuickOpen hides the palette and clears the query. A list being read
// goes on: it fills the cache for the next opening.
func (a *App) CloseQuickOpen() {
	a.quick.Open = false
	a.quick.Query = ""
}

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
				// The palette is for SQL: a file with no connection to run
				// against, or that is not .sql, is not listed.
				if !strings.EqualFold(filepath.Ext(n.Name), ".sql") {
					continue
				}
				if _, ok := a.ResolveDatasource(n.Path); !ok {
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
		out = append(out, QuickItem{Kind: QuickConnection, Key: "c|" + p.ID, Label: p.Name, Conn: p.ID,
			Detail: fmt.Sprintf("%s · %s:%d · %s", kindName(p.DBType), p.Host, p.Port, p.Database)})
	}
	return out
}

// quickParallel bounds the databases of one connection listed at once:
// load, not coverage, since every database is still chased. On Postgres
// each one the session is not in opens a session of its own.
const quickParallel = 10

const (
	// quickTTL is how long a complete list is trusted before it is read
	// again behind the cache. A table made five minutes ago is found.
	quickTTL = 5 * time.Minute
	// quickRetry is how soon a list that could not be read whole is tried
	// again, so a schema that is never readable is not asked for on every
	// keystroke.
	quickRetry = 30 * time.Second
)

// quickEntry is one connection's tables as the palette searches them. Items
// arrive database by database, so a search during the first listing already
// finds what has landed. A listing behind a warm one lands in next and
// replaces items when it is done, so the palette never shrinks while it
// refreshes, and a server that cannot be reached keeps its old rows.
type quickEntry struct {
	items, next []QuickItem
	loading     bool
	warm        bool // some listing has delivered
	complete    bool // the last one reached every database and schema
	at          time.Time
}

func (e *quickEntry) stale() bool {
	if !e.warm {
		return true
	}
	if e.complete {
		return time.Since(e.at) >= quickTTL
	}
	return time.Since(e.at) >= quickRetry
}

// warmQuick reads the tables of p behind the cache, when its list is stale
// and no listing is on its way. It runs when the palette opens, as the
// user types, and as soon as a connection connects, so the first search has
// rows. Every write to the entry happens on the window's thread.
func (a *App) warmQuick(p model.Profile) {
	e := a.quickTables[p.ID]
	if e == nil {
		e = &quickEntry{}
		a.quickTables[p.ID] = e
	}
	if e.loading || !e.stale() {
		return
	}
	e.loading, e.next = true, nil
	if !e.warm {
		e.items = nil
	}
	go func() {
		some, all := a.listQuickTables(a.ctx, p, func(hits []QuickItem) {
			a.upd(func() {
				if a.quickTables[p.ID] != e {
					return
				}
				if e.warm {
					e.next = append(e.next, hits...)
				} else {
					e.items = append(e.items, hits...)
				}
				a.quick.ver++
			})
		})
		a.upd(func() {
			if a.quickTables[p.ID] != e {
				return
			}
			e.loading, e.complete, e.at = false, all, time.Now()
			if some {
				if e.warm {
					e.items = e.next
				}
				e.warm = true
			}
			e.next = nil
			a.quick.ver++
		})
	}()
}

// refreshQuick forgets what the palette knew of a connection and reads it
// again if it is connected: the databases it may show have changed.
func (a *App) refreshQuick(id string) {
	delete(a.quickTables, id)
	a.quick.ver++
	if p, ok := a.Profile(id); ok && p.DBType.IsSQL() && a.Status(id) == Connected {
		a.warmQuick(p)
	}
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
// connection, a few at a time, and reports whether any database could be
// listed and whether all of them could. The profile's own database goes
// first, then the rest as the server names them (minus the ones the user
// hid). On Postgres every database is a session of its own, as the sidebar
// treats it, and all are searched, not only the profile's. On MySQL a
// database is a schema. What cannot be listed, a database the user may not
// enter, is left out so it does not blank the rest.
func (a *App) listQuickTables(ctx context.Context, p model.Profile, found func([]QuickItem)) (some, all bool) {
	home := p.Database
	if home == "" {
		if info, ok := a.ServerInfo(p.ID); ok {
			home = info.CurrentDatabase
		}
	}
	dbs := []string{home}
	all = true
	if a.pl.Catalog(ctx, p.ID, home, func(ctx context.Context, conn driver.Conn) error {
		infos, err := conn.ListDatabases(ctx, "")
		if err != nil {
			return err
		}
		dbs = dbs[:0]
		for _, n := range databaseNodes(p, infos) {
			dbs = append(dbs, n.DB)
		}
		return nil
	}) != nil {
		all = false
	}
	if i := slices.Index(dbs, home); i > 0 {
		dbs[0], dbs[i] = dbs[i], dbs[0]
	}
	var (
		wg     sync.WaitGroup
		listed atomic.Int32
		failed atomic.Int32
		sem    = make(chan struct{}, quickParallel)
	)
	for _, db := range dbs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			hits, ok, whole := a.listQuickDatabase(ctx, p, db)
			if !whole {
				failed.Add(1)
			}
			if ok {
				listed.Add(1)
				found(hits)
			}
		}()
	}
	wg.Wait()
	return listed.Load() > 0, all && failed.Load() == 0
}

// listQuickDatabase is the tables of one database, a schema's at a time on
// Postgres. ok is false when nothing could be listed; whole when every
// schema was. The profile's own schema goes first. When the schemas cannot
// be read (a role that may not read information_schema) the default schema
// is tried alone: a schema that does not exist lists nothing, with no error.
func (a *App) listQuickDatabase(ctx context.Context, p model.Profile, db string) (hits []QuickItem, ok, whole bool) {
	schemas := []string{db}
	whole = true
	if p.DBType == model.Postgres {
		own := cmp.Or(p.DefaultSchema, "public")
		schemas = nil
		if a.pl.Catalog(ctx, p.ID, db, func(ctx context.Context, conn driver.Conn) error {
			var e error
			schemas, e = conn.ListSchemas(ctx, p.ShowSystemSchemas)
			return e
		}) != nil || len(schemas) == 0 {
			schemas, whole = []string{own}, false
		}
		if i := slices.Index(schemas, own); i > 0 {
			schemas[0], schemas[i] = schemas[i], schemas[0]
		}
	}
	for _, schema := range schemas {
		var tables []driver.TableInfo
		if a.pl.Catalog(ctx, p.ID, db, func(ctx context.Context, conn driver.Conn) error {
			var e error
			tables, e = conn.ListTables(ctx, schema, "")
			return e
		}) != nil {
			whole = false
			continue
		}
		ok = true
		for _, t := range tables {
			hits = append(hits, QuickItem{Kind: QuickTable, Key: "t|" + p.ID + "|" + db + "|" + schema + "|" + t.Name,
				Label: t.Name, Detail: p.Name + " / " + db + " / " + schema, Type: t.Type,
				Conn: p.ID, DB: db, Schema: schema, Table: t.Name, demotion: rank.TableDemotion(schema, t.Name)})
		}
	}
	return hits, ok, whole
}

// QuickResults ranks the candidates against the query: a table of a system
// schema sinks below the user's own however well it matches, then the
// better match first, the shorter name inside a tier. The choice stays on
// the same hit when the list changes, and falls to the first when that hit
// is gone. An empty query lists everything, tables first, then files, then
// connections, each by demotion and name: opening the palette is never a
// dead end.
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
	if q.Query == "" {
		all := append(a.quickTablesOf(), q.others...)
		slices.SortFunc(all, func(x, y QuickItem) int {
			return cmp.Or(cmp.Compare(x.Kind, y.Kind), cmp.Compare(x.demotion, y.demotion), cmp.Compare(x.Label, y.Label), cmp.Compare(x.Key, y.Key))
		})
		q.results = append(q.results, all[:min(len(all), quickLimit)]...)
	} else {
		type scored struct {
			QuickItem
			score int
		}
		var hits []scored
		for _, list := range [][]QuickItem{a.quickTablesOf(), q.others} {
			for _, it := range list {
				if m, ok := rank.MatchFuzzy(q.Query, it.Label); ok {
					it.Hits = m.Indices
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
