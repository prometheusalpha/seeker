package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"seeker/internal/model"
	"seeker/internal/rank"
	"seeker/internal/testenv"
)

func quickTable(conn, schema, name string) QuickItem {
	return QuickItem{Kind: QuickTable, Key: "t|" + conn + "|" + schema + "|" + name, Label: name, Conn: conn, DB: "d", Schema: schema, Table: name,
		demotion: rank.TableDemotion(schema, name)}
}

func quickLabels(res []QuickItem) []string {
	var out []string
	for _, it := range res {
		out = append(out, it.Label)
	}
	return out
}

// A system table never outranks the user's own, however well it matches; a
// better match beats a worse one, and the shorter name wins inside a tier.
func TestQuickResultsOrder(t *testing.T) {
	a := newTestApp(t)
	q := &a.quick
	cachedTables(a, "c",
		quickTable("c", "pg_catalog", "users"),
		quickTable("c", "public", "users_archive_2019"),
		quickTable("c", "public", "users"),
		quickTable("c", "public", "app_users"),
		quickTable("c", "public", "unrelated"))
	q.Query = "users"
	q.ver++
	got := quickLabels(a.QuickResults())
	want := []string{"users", "users_archive_2019", "app_users", "users"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if a.QuickResults()[3].Schema != "pg_catalog" {
		t.Errorf("the system table is not last: %v", a.QuickResults())
	}

	// An empty query lists everything, the user's tables before the system's.
	q.Query = ""
	q.ver++
	res := a.QuickResults()
	if len(res) != 5 || res[len(res)-1].Schema != "pg_catalog" {
		t.Errorf("an empty query listed %v", quickLabels(res))
	}
}

// Until the user chooses, the choice stays on the first hit as better ones
// land above it; once chosen by hand it follows its hit, and falls to the
// first when the hit is gone.
func TestQuickChoiceFollowsItsHit(t *testing.T) {
	a := newTestApp(t)
	q := &a.quick
	e := cachedTables(a, "c", quickTable("c", "public", "orders_old"))
	q.Query = "orders"
	q.ver++
	a.QuickResults()
	// Nothing chosen yet: a better match landing takes the first row and
	// the cursor with it.
	e.items = append(e.items, quickTable("c", "public", "orders_new"))
	q.ver++
	if res := a.QuickResults(); res[0].Label != "orders_new" || q.Row != 0 {
		t.Fatalf("the cursor left the top: row %d of %v", q.Row, quickLabels(res))
	}
	e.items = e.items[:1]
	q.ver++
	a.QuickResults()
	a.QuickMove(0)
	chosen := q.sel

	// A better match arrives and sorts above the chosen one.
	e.items = append(e.items, quickTable("c", "public", "orders"))
	q.ver++
	res := a.QuickResults()
	if res[0].Label != "orders" || q.Row != 1 || res[q.Row].Key != chosen {
		t.Errorf("choice moved: row %d of %v", q.Row, quickLabels(res))
	}

	q.Query = "orders_o"
	res = a.QuickResults()
	if q.Row != 0 || len(res) != 1 {
		t.Errorf("row %d of %v", q.Row, quickLabels(res))
	}
	a.QuickMove(5)
	if q.Row != 0 {
		t.Errorf("moved past the end: %d", q.Row)
	}
}

// Files under the opened folders and the connections are candidates, and
// activating a hit opens it and closes the palette.
func TestQuickOpensFilesAndConnections(t *testing.T) {
	a, run, root := fileWorld(t)
	a.profiles = []model.Profile{{ID: "c1", Name: "Warehouse", DBType: model.Postgres, Database: "d"}}
	a.OpenQuickOpen()
	q := &a.quick
	if !q.Open || !q.Focus {
		t.Fatalf("open %v focus %v", q.Open, q.Focus)
	}

	q.Query = "deep"
	res := a.QuickResults()
	if len(res) != 0 {
		// c.sql is the file; "deep" is only its folder.
		t.Fatalf("a folder name matched: %v", quickLabels(res))
	}
	q.Query = "c.sql"
	res = a.QuickResults()
	if len(res) != 1 || res[0].Kind != QuickFile || res[0].Path != filepath.Join(root, "sub/deep/c.sql") {
		t.Fatalf("hits: %+v", res)
	}
	a.QuickActivate()
	run()
	if q.Open {
		t.Error("activating left the palette open")
	}
	if tab, ok := a.ActiveTab(); !ok || tab.Path != filepath.Join(root, "sub/deep/c.sql") {
		t.Errorf("active tab: %+v", tab)
	}

	a.OpenQuickOpen()
	q.Query = "ware"
	if res := a.QuickResults(); len(res) != 1 || res[0].Kind != QuickConnection {
		t.Fatalf("hits: %+v", res)
	}
	a.QuickActivate()
	if tab, _ := a.ActiveTab(); tab.Conn != "c1" || tab.Mode != ModeQuery {
		t.Errorf("a new query tab was expected on c1: %+v", tab)
	}
}

// cachedTables is a connected connection whose tables were listed.
func cachedTables(a *App, conn string, items ...QuickItem) *quickEntry {
	a.profiles = append(a.profiles, model.Profile{ID: conn, Name: conn, DBType: model.Postgres, Host: "127.0.0.1", Port: 1, Database: "d"})
	c := a.conn(conn)
	c.mu.Lock()
	c.status = Connected
	c.mu.Unlock()
	e := &quickEntry{warm: true, items: items}
	a.quickTables[conn] = e
	return e
}

// A list younger than the TTL is used as it is; an older one is searched at
// once and read again behind it; a read that fails leaves the old list and
// is tried again only after the retry wait.
func TestQuickCache(t *testing.T) {
	a := newTestApp(t)
	run := drained(t, a)
	e := cachedTables(a, "c", quickTable("c", "public", "orders"))
	e.complete, e.at = true, time.Now()

	a.OpenQuickOpen()
	a.QuickTyped()
	if a.QuickLoading() {
		t.Fatal("a fresh list was read again")
	}

	e.at = time.Now().Add(-2 * quickTTL)
	a.OpenQuickOpen()
	if !a.QuickLoading() {
		t.Fatal("a stale list was not read again")
	}
	a.quick.Query = "orders"
	if len(a.QuickResults()) != 1 {
		t.Fatalf("the cache was not used meanwhile: %v", a.QuickResults())
	}
	run() // the read fails: nothing listens on the port
	if a.QuickLoading() || len(e.items) != 1 || !e.warm {
		t.Errorf("loading %v, items %d, warm %v", a.QuickLoading(), len(e.items), e.warm)
	}
	if e.complete {
		t.Error("a read that reached nothing was called complete")
	}
	a.QuickTyped()
	if a.QuickLoading() {
		t.Error("a failed read was tried again at once")
	}
	e.at = time.Now().Add(-2 * quickRetry)
	a.QuickTyped()
	if !a.QuickLoading() {
		t.Error("a failed read was never tried again")
	}

	// What a read being made brings in does not show until it is whole.
	e.next = append(e.next, quickTable("c", "public", "orders_v2"))
	a.quick.ver++
	if got := quickLabels(a.QuickResults()); len(got) != 1 || got[0] != "orders" {
		t.Errorf("a half-read list showed: %v", got)
	}
}

// The user's tables come first, then files, then connections; hiding a
// database forgets what was listed.
func TestQuickBlankOrderAndRefresh(t *testing.T) {
	a, run, root := fileWorld(t)
	_, _ = run, root
	cachedTables(a, "z", quickTable("z", "public", "zebra"))
	a.OpenQuickOpen()
	res := a.QuickResults()
	if len(res) < 3 || res[0].Kind != QuickTable || res[1].Kind != QuickFile || res[len(res)-1].Kind != QuickConnection {
		t.Fatalf("order: %+v", res)
	}
	a.refreshQuick("z")
	if e := a.quickTables["z"]; e != nil && e.warm {
		t.Errorf("the cache survived: %+v", e)
	}
}

// On Postgres every database is its own: a profile whose database is
// `postgres` still finds the tables of another one on the server.
func TestLiveQuickOpenSearchesOtherDatabases(t *testing.T) {
	profile, password := pgProfile(t)
	testenv.SQLite(t)
	if profile.Database == "" {
		profile.Database = "recall_test"
	}
	fixtures(t, profile)
	profile.Database = "postgres"

	dir := t.TempDir()
	a, err := New(context.Background(), filepath.Join(dir, "seeker.db"), filepath.Join(dir, "absent.db"))
	if err != nil {
		t.Fatal(err)
	}
	p := newPump()
	a.Attach(p.update)
	t.Cleanup(a.Close)
	a.profiles = []model.Profile{profile}
	a.conn(profile.ID).setPassword(password)
	a.Connect(profile.ID)
	p.waitFor(t, "connection", func() bool { return a.Status(profile.ID) == Connected })

	a.OpenQuickOpen()
	a.quick.Query = "seeker_m2_users"
	p.waitFor(t, "the tables", func() bool {
		a.quick.ver++
		return a.QuickResults() != nil && len(a.QuickResults()) > 0
	})
	if it := a.QuickResults()[0]; it.Kind != QuickTable || it.Schema != "seeker_m2" || it.DB == "postgres" {
		t.Errorf("hit: %+v", it)
	}
	p.waitFor(t, "the lists to finish", func() bool { return !a.QuickLoading() })
}

// Only a connection that is connected is searched, and nothing is opened.
func TestQuickOpenDoesNotConnect(t *testing.T) {
	a := newTestApp(t)
	a.profiles = []model.Profile{{ID: "c", Name: "c", DBType: model.Postgres, Host: "127.0.0.1", Port: 1, Database: "d"}}
	a.OpenQuickOpen()
	if a.QuickLoading() || a.Status("c") != Disconnected {
		t.Errorf("loading %v, status %v", a.QuickLoading(), a.Status("c"))
	}
}
