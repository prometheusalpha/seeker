package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"seeker/internal/driver"
	"seeker/internal/model"
	"seeker/internal/sqltext"
)

// PageSizes are the rows a page of a table tab can hold; the first is the
// default.
var PageSizes = []int{100, 500, 1000, 2000}

// TableView is what a table tab shows of its table: the page of rows, and
// the WHERE, ORDER BY and page that make it. It lives as long as the tab and
// in memory only, as recall's did.
type TableView struct {
	// WhereText and OrderText are what the filter bar's fields hold; Where
	// and OrderBy are what the rows on screen were read with. A filter is
	// applied on Enter, never while it is typed.
	WhereText, OrderText string
	Where, OrderBy       string
	Page, PageSize       int

	Grid     *ResultGrid
	Duration time.Duration
	Loading  bool
	// Started is when the load in flight began; Cancelled is set when the
	// user stopped the last one, which leaves the rows from before it.
	Started   time.Time
	Cancelled bool
	// Err is the failure of the last load, shown over the rows from before
	// it; ErrTarget names the field to blame, "where" or "orderBy", when
	// only one of them has text.
	Err, ErrTarget string

	// Count is the table's exact row count under Where, once asked for:
	// COUNT(*) is a full scan, so it is never run on load.
	Count    int64
	HasCount bool
	Counting bool

	started    bool
	keys       []string
	keysKnown  bool
	gen        int
	countGen   int
	cancel     context.CancelFunc
	committing bool
}

// Committing reports a write of staged edits in flight.
func (tv *TableView) Committing() bool { return tv.committing }

// HasNextPage is inferred from the page that came back: a short page is the
// last. A full one proves nothing, and a COUNT would cost more than the page.
func (tv *TableView) HasNextPage() bool {
	return tv.Grid != nil && len(tv.Grid.Rows) >= tv.PageSize
}

func (a *App) tableView(id string) *TableView {
	if tv := a.tables[id]; tv != nil {
		return tv
	}
	tv := &TableView{Page: 1, PageSize: PageSizes[0]}
	if a.tables == nil {
		a.tables = map[string]*TableView{}
	}
	a.tables[id] = tv
	return tv
}

// TableOf is the state of a table tab, made when first asked for; nil for
// any other tab.
func (a *App) TableOf(id string) *TableView {
	if t, ok := a.TabByID(id); !ok || t.Mode != ModeTable {
		return nil
	}
	return a.tableView(id)
}

// TableSync loads the table of the active table tab the first time it
// shows. The load starts here and not as the view builds, which runs again
// on every event.
func (a *App) TableSync() {
	t, ok := a.ActiveTab()
	if !ok || t.Mode != ModeTable {
		return
	}
	if tv := a.tableView(t.ID); !tv.started {
		a.LoadTable(t.ID)
	}
}

// tableSQL is the SELECT of one page. The WHERE and the ORDER BY are the
// user's own text; the caller refuses a statement that is more than one.
func tableSQL(t model.DBType, schema, table, where, orderBy string, page, size int) string {
	var b strings.Builder
	b.WriteString("SELECT * FROM " + driver.Qualify(t, schema, table))
	if where = strings.TrimSpace(where); where != "" {
		b.WriteString(" WHERE " + where)
	}
	if orderBy = strings.TrimSpace(orderBy); orderBy != "" {
		b.WriteString(" ORDER BY " + orderBy)
	}
	fmt.Fprintf(&b, " LIMIT %d", size)
	// Page 1 is what nearly every run is, and OFFSET 0 only makes the
	// statement noisier in the error line.
	if off := (max(page, 1) - 1) * size; off > 0 {
		fmt.Fprintf(&b, " OFFSET %d", off)
	}
	return b.String()
}

// clauseBlame says which field an error belongs to when only one of the two
// has text, "" otherwise.
func clauseBlame(where, orderBy string) string {
	w, o := strings.TrimSpace(where) != "", strings.TrimSpace(orderBy) != ""
	switch {
	case w && !o:
		return "where"
	case o && !w:
		return "orderBy"
	}
	return ""
}

// tableTarget resolves where a table tab's rows are: the schema the
// session reads it in (for MySQL, the database).
func (a *App) tableTarget(t Tab) (schema string, ok bool) {
	key, ok := a.tabKey(t)
	return key.schema, ok
}

// LoadTable reads the tab's current page, replacing the rows on screen when
// they arrive. A load that is overtaken by another is cancelled and its
// answer dropped.
func (a *App) LoadTable(id string) {
	t, ok := a.TabByID(id)
	if !ok || t.Mode != ModeTable {
		return
	}
	tv := a.tableView(id)
	tv.started = true
	prof, ok := a.Profile(t.Conn)
	if !ok {
		tv.Err, tv.ErrTarget = "Pick a connection first", ""
		return
	}
	schema, ok := a.tableTarget(t)
	if !ok {
		tv.Err, tv.ErrTarget = "This table has no database to read from", ""
		return
	}
	sql := tableSQL(prof.DBType, schema, t.Table, tv.Where, tv.OrderBy, tv.Page, tv.PageSize)
	d := a.dialect(t)
	if n := len(sqltext.Split(sql, d)); n != 1 {
		tv.Err, tv.ErrTarget = fmt.Sprintf("expected one statement, got %d", n), clauseBlame(tv.Where, tv.OrderBy)
		return
	}
	a.ensureTabConnected(t)
	cs := a.conn(t.Conn)
	if tv.cancel != nil {
		tv.cancel()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	tv.cancel = cancel
	tv.gen++
	gen := tv.gen
	tv.Loading, tv.Err, tv.ErrTarget, tv.Cancelled, tv.Started = true, "", "", false, time.Now()
	known, keys := tv.keysKnown, slices.Clone(tv.keys)
	orderBy := tv.OrderBy
	go func() {
		defer cancel()
		var res driver.Result
		var found bool
		err := waitConnected(cs)
		if err == nil {
			err = a.pl.Query(ctx, t.Conn, t.Database, id, func(ctx context.Context, c driver.Conn) error {
				if !known {
					// The key is what makes rows editable; a table whose
					// columns cannot be read is shown read-only.
					if cols, e := c.ListColumns(ctx, schema, t.Table); e == nil {
						for _, col := range cols {
							if col.PrimaryKey {
								keys = append(keys, col.Name)
							}
						}
						known = true
					}
				}
				if all := c.Execute(ctx, sql, driver.MaxRows, nil); len(all) > 0 {
					res, found = all[0], true
				}
				return nil
			})
		}
		a.upd(func() {
			tv := a.tables[id]
			if tv == nil || tv.gen != gen {
				return
			}
			tv.Loading = false
			if tv.keysKnown = known; known {
				tv.keys = keys
			}
			switch {
			case err != nil:
				if !errors.Is(err, context.Canceled) {
					tv.Err = err.Error()
				}
			case !found:
			case res.Err != nil:
				tv.Err, tv.ErrTarget = res.Err.Message, clauseBlame(tv.Where, tv.OrderBy)
			default:
				// For MySQL the schema is the database, and what INSERT and
				// UPDATE qualify the table by.
				target := &TableTarget{Type: prof.DBType, Dialect: d, Database: t.Database, Schema: schema, Table: t.Table, Keys: keys}
				g := newResultGrid(res, target, a.ui.CharWidth())
				g.SetSort(orderBy)
				g.Inherit(tv.Grid)
				tv.Grid, tv.Duration = g, res.Duration
			}
		})
	}()
}

// CancelTable stops the table's load in flight. The driver interrupts the
// statement on the server and keeps the session; the rows from before stay,
// and the load's answer, if it still comes, is dropped by gen.
func (a *App) CancelTable(id string) {
	tv := a.tables[id]
	if tv == nil || !tv.Loading {
		return
	}
	tv.gen++
	tv.Loading, tv.Cancelled = false, true
	if tv.cancel != nil {
		tv.cancel()
	}
}

// unsaved reports edits staged on the table that reading the page again
// would drop, and says so in a toast whose button drops them and goes on
// with again. The edits are the user's typing: they leave only by their
// word.
func (a *App) unsaved(id string, again func()) bool {
	tv := a.TableOf(id)
	if tv == nil || tv.Grid == nil || tv.Grid.Pending() == 0 {
		return false
	}
	n := tv.Grid.Pending()
	msg := fmt.Sprintf("%d edited cells are not saved: Save (Cmd+S) or discard them first", n)
	if n == 1 {
		msg = "1 edited cell is not saved: Save (Cmd+S) or discard it first"
	}
	a.toastAction(msg, "Discard", func() {
		a.DiscardEdits(id)
		again()
	})
	return true
}

// ReloadTable runs the page again (Cmd+R). Edits staged on it are not
// dropped without asking.
func (a *App) ReloadTable(id string) {
	if tv := a.TableOf(id); tv != nil && !a.unsaved(id, func() { a.ReloadTable(id) }) {
		a.LoadTable(id)
	}
}

// ApplyTableFilter takes the filter bar's text as the table's WHERE and
// ORDER BY and reads page 1: a new filter changes which rows there are, so
// the page it was on means nothing.
func (a *App) ApplyTableFilter(id string) {
	tv := a.TableOf(id)
	if tv == nil || a.unsaved(id, func() { a.ApplyTableFilter(id) }) {
		return
	}
	tv.Where, tv.OrderBy = strings.TrimSpace(tv.WhereText), strings.TrimSpace(tv.OrderText)
	tv.Page = 1
	// The count belongs to the WHERE that produced it.
	tv.HasCount, tv.Counting = false, false
	tv.countGen++
	a.LoadTable(id)
}

// SortTable orders the table by a result column, writing the expression in
// the ORDER BY field: that field is the only sort state, so the statement
// and the bar never disagree.
func (a *App) SortTable(id string, resultCol int, desc bool) {
	tv := a.TableOf(id)
	t, _ := a.TabByID(id)
	if tv == nil || tv.Grid == nil || resultCol < 0 || resultCol >= len(tv.Grid.Columns) {
		return
	}
	if a.unsaved(id, func() { a.SortTable(id, resultCol, desc) }) {
		return
	}
	prof, ok := a.Profile(t.Conn)
	if !ok {
		return
	}
	dir := " ASC"
	if desc {
		dir = " DESC"
	}
	tv.OrderText = driver.QuoteIdent(prof.DBType, tv.Grid.Columns[resultCol].Name) + dir
	a.ApplyTableFilter(id)
}

// ClearSort empties the ORDER BY.
func (a *App) ClearSort(id string) {
	if tv := a.TableOf(id); tv != nil && !a.unsaved(id, func() { a.ClearSort(id) }) {
		tv.OrderText = ""
		a.ApplyTableFilter(id)
	}
}

// SetTablePage goes to a page, from 1.
func (a *App) SetTablePage(id string, page int) {
	tv := a.TableOf(id)
	if tv == nil || max(page, 1) == tv.Page || a.unsaved(id, func() { a.SetTablePage(id, page) }) {
		return
	}
	tv.Page = max(page, 1)
	a.LoadTable(id)
}

// SetTablePageSize changes the rows a page holds and goes back to page 1: at
// another size the page it was on is usually past the end.
func (a *App) SetTablePageSize(id string, size int) {
	tv := a.TableOf(id)
	if tv == nil || !slices.Contains(PageSizes, size) || size == tv.PageSize || a.unsaved(id, func() { a.SetTablePageSize(id, size) }) {
		return
	}
	tv.PageSize, tv.Page = size, 1
	a.LoadTable(id)
}

// CountTable asks for the exact number of rows under the WHERE on screen.
func (a *App) CountTable(id string) {
	tv := a.TableOf(id)
	t, _ := a.TabByID(id)
	if tv == nil || tv.Counting {
		return
	}
	schema, ok := a.tableTarget(t)
	if !ok {
		return
	}
	a.ensureTabConnected(t)
	cs := a.conn(t.Conn)
	tv.Counting = true
	gen, where := tv.countGen, tv.Where
	go func() {
		var n int64
		err := waitConnected(cs)
		if err == nil {
			err = a.pl.Query(a.ctx, t.Conn, t.Database, id, func(ctx context.Context, c driver.Conn) error {
				var e error
				n, e = c.CountRows(ctx, schema, t.Table, where)
				return e
			})
		}
		a.upd(func() {
			tv := a.tables[id]
			if tv == nil || tv.countGen != gen {
				return
			}
			tv.Counting = false
			if err != nil {
				a.toast("Could not count: " + err.Error())
				return
			}
			tv.Count, tv.HasCount = n, true
		})
	}()
}

// CommitEdits writes the edits staged on the table's grid, one UPDATE per
// row by primary key in one transaction, then reads the page again. A
// failure keeps the edits, so they can be fixed or tried again.
func (a *App) CommitEdits(id string) {
	tv := a.TableOf(id)
	t, _ := a.TabByID(id)
	if tv == nil || tv.Grid == nil || tv.committing {
		return
	}
	g := tv.Grid
	changes := g.Changes()
	if len(changes) == 0 || !g.CanEdit() {
		return
	}
	a.ensureTabConnected(t)
	cs := a.conn(t.Conn)
	tv.committing = true
	schema, keys := g.Target.Schema, slices.Clone(g.Target.Keys)
	go func() {
		var n int
		err := waitConnected(cs)
		if err == nil {
			err = a.pl.Query(a.ctx, t.Conn, t.Database, id, func(ctx context.Context, c driver.Conn) error {
				var e error
				n, e = c.UpdateRows(ctx, schema, t.Table, keys, changes)
				return e
			})
		}
		a.upd(func() {
			tv := a.tables[id]
			if tv == nil {
				return
			}
			tv.committing = false
			if err != nil {
				a.toast("Could not save the edits: " + err.Error())
				return
			}
			a.toast(fmt.Sprintf("Updated %d rows", n))
			a.LoadTable(id)
		})
	}()
}

// DiscardEdits drops the edits staged on the table's grid.
func (a *App) DiscardEdits(id string) {
	if tv := a.TableOf(id); tv != nil && tv.Grid != nil {
		tv.Grid.Discard()
	}
}
