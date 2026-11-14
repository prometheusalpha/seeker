package app

import (
	"context"
	"sync"
	"time"

	"seeker/internal/driver"
	"seeker/internal/model"
	"seeker/internal/sqltext"
)

// The schema view completion reads (recall's useSchemaCatalog): table and
// column lists cached per target, so a burst of typing costs one request
// per table. Completion runs on every keystroke and never waits on the
// wire; a cold entry starts a load and the editor asks again when it lands.
const (
	catalogTTL = 5 * time.Minute
	// A failed load is cached too, but only briefly: one dropped socket
	// must not black out completions for minutes.
	catalogFailureTTL = 10 * time.Second
	// warmTables bounds how many tables a tab's script warms up front.
	warmTables = 25
)

// schemaKey is where a list lives: the session's database and the schema
// (for MySQL the schema is the database).
type schemaKey struct{ conn, db, schema string }

type columnKey struct {
	schemaKey
	table string
}

type entry[T any] struct {
	items     T
	fetchedAt time.Time
	failed    bool
	loading   bool
}

func (e *entry[T]) fresh() bool {
	ttl := catalogTTL
	if e.failed {
		ttl = catalogFailureTTL
	}
	return time.Since(e.fetchedAt) < ttl
}

type catalog struct {
	mu     sync.Mutex
	tables map[schemaKey]*entry[[]driver.TableInfo]
	cols   map[columnKey]*entry[[]driver.ColumnInfo]
}

func newCatalog() *catalog {
	return &catalog{tables: map[schemaKey]*entry[[]driver.TableInfo]{}, cols: map[columnKey]*entry[[]driver.ColumnInfo]{}}
}

// forget drops what was cached for a connection: its profile changed.
func (c *catalog) forget(conn string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.tables {
		if k.conn == conn {
			delete(c.tables, k)
		}
	}
	for k := range c.cols {
		if k.conn == conn {
			delete(c.cols, k)
		}
	}
}

// catalogTables returns the cached tables of k, nil while cold, and starts
// a load when they are missing or stale; the editors are told when it lands.
func (a *App) catalogTables(k schemaKey) []driver.TableInfo {
	c := a.cat
	c.mu.Lock()
	e := c.tables[k]
	if e == nil {
		e = &entry[[]driver.TableInfo]{}
		c.tables[k] = e
	}
	items := e.items
	start := !e.loading && !e.fresh()
	if start {
		e.loading = true
	}
	c.mu.Unlock()
	if start {
		go func() {
			var tables []driver.TableInfo
			err := a.pl.Catalog(a.ctx, k.conn, k.db, func(ctx context.Context, conn driver.Conn) error {
				var e error
				tables, e = conn.ListTables(ctx, k.schema, "")
				return e
			})
			c.mu.Lock()
			e.loading, e.fetchedAt, e.failed = false, time.Now(), err != nil
			if err == nil {
				// Empty, not nil: a schema with no tables is a warm answer.
				e.items = append([]driver.TableInfo{}, tables...)
			}
			c.mu.Unlock()
			a.catalogLanded()
		}()
	}
	return items
}

// catalogColumns is catalogTables for the columns of one table.
func (a *App) catalogColumns(k schemaKey, table string) []driver.ColumnInfo {
	c := a.cat
	ck := columnKey{k, table}
	c.mu.Lock()
	e := c.cols[ck]
	if e == nil {
		e = &entry[[]driver.ColumnInfo]{}
		c.cols[ck] = e
	}
	items := e.items
	start := !e.loading && !e.fresh()
	if start {
		e.loading = true
	}
	c.mu.Unlock()
	if start {
		go func() {
			var cols []driver.ColumnInfo
			err := a.pl.Catalog(a.ctx, k.conn, k.db, func(ctx context.Context, conn driver.Conn) error {
				var e error
				cols, e = conn.ListColumns(ctx, k.schema, table)
				return e
			})
			c.mu.Lock()
			e.loading, e.fetchedAt, e.failed = false, time.Now(), err != nil
			if err == nil {
				e.items = append([]driver.ColumnInfo{}, cols...)
			}
			c.mu.Unlock()
			a.catalogLanded()
		}()
	}
	return items
}

// catalogLanded tells the editors a list arrived, so one that asked while
// it was cold asks again. A catalog load can finish after Close.
func (a *App) catalogLanded() {
	if a.upd == nil {
		return
	}
	a.upd(func() {
		for _, te := range a.editors {
			te.retryCompletion()
		}
	})
}

// tabKey is the schema a query tab runs in. The database falls back to the
// profile's, then to the one the server reported at connect; "" schema
// (a MySQL profile with no database) has nothing to list.
func (a *App) tabKey(t Tab) (schemaKey, bool) {
	p, ok := a.Profile(t.Conn)
	if !ok || t.Conn == "" {
		return schemaKey{}, false
	}
	db := t.Database
	if db == "" {
		db = p.Database
	}
	if db == "" {
		if info, ok := a.ServerInfo(t.Conn); ok {
			db = info.CurrentDatabase
		}
	}
	schema := t.Schema
	if p.DBType == model.MySQL {
		schema = db
	} else if schema == "" {
		schema = "public"
	}
	if schema == "" {
		return schemaKey{}, false
	}
	return schemaKey{t.Conn, db, schema}, true
}

// warmTab loads what completion will want in a tab the moment it shows:
// the schema's tables, and the columns of the tables its script names.
// The trigger is the tab and its target, never the text: rescanning on
// each keystroke would put the whole script through a scan per character.
func (a *App) warmTab(t Tab) {
	key, ok := a.tabKey(t)
	if !ok || a.Status(t.Conn) != Connected {
		return
	}
	stamp := warmStamp{t.ID, key}
	if a.warmed == stamp {
		return
	}
	a.warmed = stamp
	a.catalogTables(key)
	tables := sqltext.ReferencedTables(t.SQL[:min(len(t.SQL), scanLimit)])
	for _, name := range tables[:min(len(tables), warmTables)] {
		a.catalogColumns(key, name)
	}
}

type warmStamp struct {
	tab string
	key schemaKey
}
