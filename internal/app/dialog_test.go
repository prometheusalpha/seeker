package app

import (
	"context"
	"path/filepath"
	"testing"

	"seeker/internal/model"
	"seeker/internal/testenv"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	testenv.SQLite(t)
	dir := t.TempDir()
	a, err := New(context.Background(), filepath.Join(dir, "seeker.db"), filepath.Join(dir, "absent.db"))
	if err != nil {
		t.Fatal(err)
	}
	a.Attach(func(fn func()) { fn() })
	t.Cleanup(a.Close)
	return a
}

// Editing a MySQL profile used to read as a change of type on the first
// frame and reset its port, user and parameters.
func TestEditMySQLKeepsFields(t *testing.T) {
	a := newTestApp(t)
	a.profiles = []model.Profile{{ID: "m", Name: "m", DBType: model.MySQL, Host: "db", Port: 3307,
		Username: "app", URLParams: "tls=skip-verify", SSL: true}}
	a.OpenConnectionDialog("m")
	a.ConnSync()
	d := a.dlg.Conn
	if d.Port != "3307" || d.User != "app" || d.Params != "tls=skip-verify" || !d.SSL {
		t.Errorf("fields were reset: port %q user %q params %q ssl %v", d.Port, d.User, d.Params, d.SSL)
	}
}

func TestDraftIDIsStable(t *testing.T) {
	a := newTestApp(t)
	a.OpenConnectionDialog("")
	d := &a.dlg.Conn
	if a.profile(d).ID != a.profile(d).ID || a.profile(d).ID == "" {
		t.Error("a new connection's id changes between calls")
	}
	if a.profile(d).ID != d.draftID {
		t.Error("profile id is not the draft id")
	}
}

func TestSaveWaitsForPassword(t *testing.T) {
	a := newTestApp(t)
	a.OpenConnectionDialog("")
	d := &a.dlg.Conn
	d.PasswordLoading = true
	a.SaveConnectionDialog()
	if !d.Open || len(a.profiles) != 0 {
		t.Error("saved while the password was still loading")
	}
}

func TestSessionPasswordBeatsKeychain(t *testing.T) {
	a := newTestApp(t)
	c := a.conn("x")
	c.setPassword("new")
	if got := a.password(model.Profile{ID: "x", SavePassword: true, Username: "u"}, c); got != "new" {
		t.Errorf("password %q", got)
	}
}

func TestCloseFlushesPendingSave(t *testing.T) {
	a := newTestApp(t)
	wrote := false
	a.saves.later("k", func() { wrote = true })
	a.saves.flush()
	if !wrote {
		t.Error("pending write was dropped")
	}
	a.saves.later("k", func() { t.Error("write after flush") })
}

func TestAttachConnectsRestoredTab(t *testing.T) {
	a := newTestApp(t)
	a.profiles = []model.Profile{{ID: "p", Name: "p", DBType: model.Postgres, Host: "127.0.0.1", Port: 1, Username: "u"}}
	a.tabs.Items = []Tab{{ID: "t", Conn: "p", Mode: ModeQuery}}
	a.tabs.Active = 0
	a.Attach(func(fn func()) { fn() })
	if s := a.Status("p"); s != Connecting && s != Failed {
		t.Errorf("status %v: the restored tab's connection was not started", s)
	}
}

// A Redis form saves a profile with no SQL fields, whatever the form held
// before the type changed, and needs no user.
func TestRedisDialogProfile(t *testing.T) {
	a := &App{}
	d := &a.dlg.Conn
	*d = ConnDialog{Open: true, Kind: KindRedis, Name: "cache", Host: "10.0.0.5", Port: "6379", DefaultDB: "3",
		Database: "leftover", Schema: "public", Params: "sslmode=require", ConnectTimeout: "7", QueryTimeout: "9", SSL: true}
	if !d.validate() {
		t.Fatalf("a Redis form without a user must be valid: %q", d.Err)
	}
	p := a.profile(d)
	if p.DBType != model.Redis || p.DefaultDB != 3 || !p.SSL || p.ConnectTimeoutSecs != 7 || p.Host != "10.0.0.5" {
		t.Errorf("profile %+v", p)
	}
	if p.Database != "" || p.DefaultSchema != "" || p.URLParams != "" || p.QueryTimeoutSecs != 0 {
		t.Errorf("SQL fields leaked into a Redis profile: %+v", p)
	}
	d.DefaultDB = "x"
	if d.validate() {
		t.Error("a database that is not a number must be refused")
	}
}
