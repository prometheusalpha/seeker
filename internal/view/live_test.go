package view

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/driver"
	"seeker/internal/model"
	"seeker/internal/store"
	"seeker/internal/testenv"
)

// fixture makes the schema the walk ends in, and drops it again.
func fixture(t *testing.T, p model.Profile, password string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := driver.Open(ctx, p, password, p.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, s := range []string{
		"DROP SCHEMA IF EXISTS seeker_m2 CASCADE",
		"CREATE SCHEMA seeker_m2",
		"CREATE TABLE seeker_m2.seeker_m2_users (id integer PRIMARY KEY, email text NOT NULL)",
	} {
		if res := conn.Execute(ctx, s, 10, nil); len(res) != 1 || res[0].Err != nil {
			t.Fatalf("fixture %q: %+v", s, res)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		conn, err := driver.Open(ctx, p, password, p.Database)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Execute(ctx, "DROP SCHEMA IF EXISTS seeker_m2 CASCADE", 10, nil)
	})
}

// pump stands in for the window's thread: work published through Update is
// applied by the test when it asks for a frame, as the window would.
type pump struct {
	q []func()
}

func (p *pump) update(fn func()) { p.q = append(p.q, fn) }

func (p *pump) frame(tt *ui.Tester) {
	for len(p.q) > 0 {
		fn := p.q[0]
		p.q = p.q[1:]
		fn()
	}
	tt.Frame()
}

func (p *pump) waitText(t *testing.T, tt *ui.Tester, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		p.frame(tt)
		if tt.HasText(want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %q in the sidebar", want)
}

// TestLiveShell walks the real window against a real Postgres: a saved
// profile appears, clicking it connects, and every level below opens, ending
// in a table tab. It needs SEEKER_TEST_PG_URL, which recall's compose file
// provides.
func TestLiveShell(t *testing.T) {
	raw := os.Getenv("SEEKER_TEST_PG_URL")
	if raw == "" {
		t.Skip("SEEKER_TEST_PG_URL is not set; start recall/compose.yaml")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	password, _ := u.User.Password()
	profile := model.Profile{
		ID: "live", Name: "live-pg", DBType: model.Postgres, Host: u.Hostname(), Port: port,
		Username: u.User.Username(), Database: strings.TrimPrefix(u.Path, "/"), ConnectTimeoutSecs: 5,
	}
	testenv.SQLite(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "seeker.db")
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveConnections(context.Background(), []model.Profile{profile}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// A schema of its own, so the walk reaches a table nothing else wrote.
	fixture(t, profile, password)
	core, err := app.New(context.Background(), dbPath, filepath.Join(dir, "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	p := &pump{}
	core.Attach(p.update)
	t.Cleanup(core.Close)
	core.SetSessionPassword(profile.ID, password)

	tt := ui.NewTester(New(core).View, 1280, 820)
	if !tt.HasText("live-pg") {
		t.Fatalf("the saved profile is not in the sidebar: %v", tt.Texts())
	}
	// Nothing dials a server until its row asks to: the outline probes rows
	// for an arrow, which must not count as a request.
	p.frame(tt)
	if st := core.Status(profile.ID); st != app.Disconnected {
		t.Fatalf("the app connected at startup: %v", st)
	}
	if err := tt.Click("live-pg"); err != nil {
		t.Fatal(err)
	}
	p.waitText(t, tt, "recall_test", 20*time.Second)
	if err := tt.Click("recall_test"); err != nil {
		t.Fatal(err)
	}
	p.waitText(t, tt, "seeker_m2", 20*time.Second)
	if err := tt.Click("seeker_m2"); err != nil {
		t.Fatal(err)
	}
	p.waitText(t, tt, "seeker_m2_users", 20*time.Second)
	if err := tt.Click("seeker_m2_users"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	if tab, ok := core.ActiveTab(); !ok || tab.Table != "seeker_m2_users" || tab.Mode != app.ModeTable {
		t.Fatalf("active tab: %+v", tab)
	}

	// The table's menu pins it, and the row shows the pin.
	if err := tt.RightClick("seeker_m2_users"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Pin table"); err != nil {
		t.Fatalf("pin menu: %v (menu %v)", err, tt.Menu())
	}
	p.frame(tt)
	if !core.IsPinned(app.Node{Kind: app.KindTable, Conn: profile.ID, DB: profile.Database, Schema: "seeker_m2", Table: "seeker_m2_users"}) {
		t.Error("the pin did not stick")
	}

	// The connection's menu lists the server's databases, which the dialog
	// reads from the session rather than from the sidebar.
	if err := tt.RightClick("live-pg"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Databases…"); err != nil {
		t.Fatalf("databases menu: %v (menu %v)", err, tt.Menu())
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		p.frame(tt)
		if d := core.DialogsState().Databases; d.Open && !d.Loading && len(core.DatabaseItems()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	items := core.DatabaseItems()
	var hasOwn, hasPostgres bool
	for _, it := range items {
		if it.Name == profile.Database && it.Own {
			hasOwn = true
		}
		if it.Name == "postgres" {
			hasPostgres = true
		}
	}
	if !hasOwn || !hasPostgres {
		t.Fatalf("databases dialog: %+v", items)
	}
	if err := tt.Click("Done"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)

	// A dirty tab asks before it closes, and Discard drops the edits.
	core.OpenQueryTab(app.QueryTab{Conn: profile.ID, Database: profile.Database, Schema: "seeker_m2",
		SQL: "select 1", Title: "scratch.sql"})
	p.frame(tt)
	if tab, ok := core.ActiveTab(); ok {
		core.SetTabSQL(tab.ID, "select 2")
	}
	p.frame(tt)
	if err := tt.Click("Close scratch.sql"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	if !tt.HasText("Unsaved changes") {
		t.Fatalf("no close dialog: %v", tt.Texts())
	}
	if err := tt.Click("Discard"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	if n := len(core.Tabs().Items); n != 1 {
		t.Errorf("Discard left %d tabs, want 1", n)
	}
	if closed := core.Tabs().Closed; len(closed) != 1 || closed[0].Title != "scratch.sql" {
		t.Errorf("closed stack: %+v", closed)
	}

	// A drag moves a tab where it is dropped, across the pinned boundary
	// when the target is there.
	core.OpenQueryTab(app.QueryTab{Conn: profile.ID, Database: profile.Database, Schema: "seeker_m2", SQL: "select 1", Title: "one.sql", ForceNew: true})
	core.OpenQueryTab(app.QueryTab{Conn: profile.ID, Database: profile.Database, Schema: "seeker_m2", SQL: "select 1", Title: "two.sql", ForceNew: true})
	p.frame(tt)
	two, ok := tt.Find("two.sql")
	if !ok {
		t.Fatalf("no two.sql pill: %v", tt.Texts())
	}
	one, ok := tt.Find("one.sql")
	if !ok {
		t.Fatalf("no one.sql pill: %v", tt.Texts())
	}
	tt.Press(two.X+two.W/2, two.Y+two.H/2)
	tt.Move(one.X+one.W/2, one.Y+one.H/2)
	tt.Release(one.X+one.W/2, one.Y+one.H/2)
	p.frame(tt)
	var order []string
	for _, tab := range core.Tabs().Items {
		order = append(order, tab.Title)
	}
	if len(order) != 3 || order[1] != "two.sql" || order[2] != "one.sql" {
		t.Errorf("after drag: %v", order)
	}

	// Rename is the strip's only editing form, and it opens from the menu.
	if err := tt.RightClick("one.sql"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Rename"); err != nil {
		t.Fatalf("rename menu: %v (menu %v)", err, tt.Menu())
	}
	p.frame(tt)
	if !tt.HasText("Rename tab") {
		t.Fatalf("no rename dialog: %v", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)

	// New connection opens the form with the engine's defaults.
	if err := tt.Click("New connection"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
	d := core.DialogsState().Conn
	if !d.Open || d.Kind != app.KindPostgres || d.Host != "localhost" || d.Port != "5432" || d.User != "postgres" {
		t.Errorf("connection form: %+v", d)
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	p.frame(tt)
}
