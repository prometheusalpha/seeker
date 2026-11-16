package store

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/egoist/mygo/plugins/sqlite"

	"seeker/internal/model"
	"seeker/internal/testenv"
)

func open(t *testing.T) *Store {
	t.Helper()
	testenv.SQLite(t)
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "sub", "seeker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestConnectionsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	in := []model.Profile{
		{ID: "b", Name: "pg", DBType: model.Postgres, Host: "localhost", Port: 5432, Username: "u", SSL: true,
			ConnectTimeoutSecs: 10, Note: "n", SavePassword: true, Database: "app", DefaultSchema: "public",
			URLParams: "sslmode=require", QueryTimeoutSecs: 30, ShowSystemSchemas: true, HiddenDatabases: []string{"template1"}},
		{ID: "a", Name: "cache", DBType: model.Redis, Host: "r", Port: 6379, DefaultDB: 3,
			// SQL fields on a Redis profile are not kept.
			Database: "ignored", HiddenDatabases: []string{"x"}},
	}
	if err := s.SaveConnections(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Connections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Profile{in[0], in[1]}
	want[1].Database, want[1].HiddenDatabases = "", nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%+v\nwant\n%+v", got, want)
	}
	// Saving again replaces the list, in the new order.
	if err := s.SaveConnections(ctx, []model.Profile{in[1]}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Connections(ctx); len(got) != 1 || got[0].ID != "a" {
		t.Errorf("after replacing: %+v", got)
	}
}

func TestBookmarks(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	file := filepath.Join(t.TempDir(), "q.sql")
	os.WriteFile(file, []byte("select 1"), 0o644)
	if b, err := s.SetBookmark(ctx, "a", file, 1, "  select 1 \t"); err != nil || b.LineHash != "822ae07d4783158b" {
		t.Fatalf("hash %q (recall's is 822ae07d4783158b), err %v", b.LineHash, err)
	}
	// Another mnemonic on the same line takes it over.
	s.SetBookmark(ctx, "b", file, 1, "select 1")
	if bs, _ := s.Bookmarks(ctx); len(bs) != 1 || bs[0].Mnemonic != "b" {
		t.Errorf("bookmarks %+v, want only b", bs)
	}
	if _, err := s.SetBookmark(ctx, "A", file, 1, ""); err == nil {
		t.Error("uppercase mnemonic accepted")
	}

	// Two lines inserted above: the bookmark follows its line.
	b, ok, err := s.ResolveBookmark(ctx, "b", "-- x\n-- y\nselect 1\nselect 1 -- not it")
	if err != nil || !ok || b.Line != 3 {
		t.Errorf("resolved to %+v ok=%v err=%v, want line 3", b, ok, err)
	}
	if bs, _ := s.Bookmarks(ctx); bs[0].Line != 3 {
		t.Errorf("row not moved: %+v", bs)
	}
	// The line is gone: it stays where it was, clamped to the file.
	if b, _, _ := s.ResolveBookmark(ctx, "b", "a"); b.Line != 1 {
		t.Errorf("clamped to %d, want 1", b.Line)
	}

	os.Remove(file)
	if bs, _ := s.Bookmarks(ctx); len(bs) != 0 {
		t.Errorf("bookmark of a deleted file kept: %+v", bs)
	}
}

func TestPinsAndSettings(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	users := model.PinnedTable{ConnectionID: "c", Database: "d", Schema: "public", Table: "users"}
	orders := users
	orders.Table = "orders"
	s.SetPinned(ctx, users, true)
	s.SetPinned(ctx, orders, true)
	s.SetPinned(ctx, users, true) // keeps its place
	if got, _ := s.PinnedTables(ctx); !reflect.DeepEqual(got, []model.PinnedTable{users, orders}) {
		t.Errorf("pins %+v", got)
	}
	s.SetPinned(ctx, users, false)
	if got, _ := s.PinnedTables(ctx); len(got) != 1 || got[0] != orders {
		t.Errorf("after unpinning: %+v", got)
	}

	var sizes map[string]float32
	if err := s.Setting(ctx, "panels", &sizes); err != ErrNoSetting {
		t.Errorf("unset setting: %v", err)
	}
	s.SetSetting(ctx, "panels", map[string]float32{"sidebar": 260})
	s.SetSetting(ctx, "panels", map[string]float32{"sidebar": 300})
	if err := s.Setting(ctx, "panels", &sizes); err != nil || sizes["sidebar"] != 300 {
		t.Errorf("setting %v, err %v", sizes, err)
	}
}

// recallFile writes a file shaped as recall's, with one profile of each kind.
func recallFile(t *testing.T) string {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "recall.db")
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range append(schema[:5:5],
		`INSERT INTO connections VALUES ('p1','local','postgres','localhost',5432,'me','app','public',0,'',10,0,'',1,0,'["template0"]','{}',0)`,
		`INSERT INTO connections VALUES ('r1','cache','redis','localhost',6379,'','','',0,'',10,0,'',0,0,'[]','{"defaultDb":2}',1)`,
		`INSERT INTO bookmarks VALUES ('q','/tmp/x.sql',4,'822ae07d4783158b')`,
		`INSERT INTO pinned_tables VALUES ('p1','app','public','users',1)`,
	) {
		if _, err := db.Execute(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestImportRecall(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	src := recallFile(t)
	n, err := s.ImportRecall(ctx, src)
	if err != nil || n != 2 {
		t.Fatalf("imported %d, err %v", n, err)
	}
	ps, _ := s.Connections(ctx)
	if len(ps) != 2 || ps[0].HiddenDatabases[0] != "template0" || ps[1].DefaultDB != 2 {
		t.Errorf("profiles %+v", ps)
	}
	if pins, _ := s.PinnedTables(ctx); len(pins) != 1 || pins[0].Table != "users" {
		t.Errorf("pins %+v", pins)
	}
	rows, _ := s.db.Query(ctx, "SELECT mnemonic, line FROM bookmarks")
	if len(rows.Values) != 1 || str(rows.Values[0][0]) != "q" {
		t.Errorf("bookmarks %v", rows.Values)
	}

	// Once only, even after the user deletes what came over.
	s.SaveConnections(ctx, nil)
	if n, err := s.ImportRecall(ctx, src); n != 0 || err != nil {
		t.Errorf("second import: %d, %v", n, err)
	}
}

func TestImportRecallSkipsWithConnections(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	s.SaveConnections(ctx, []model.Profile{{ID: "own", Name: "own", DBType: model.MySQL}})
	if n, err := s.ImportRecall(ctx, recallFile(t)); n != 0 || err != nil {
		t.Errorf("imported %d over existing connections, err %v", n, err)
	}
	if n, err := s.ImportRecall(ctx, filepath.Join(t.TempDir(), "missing.db")); n != 0 || err != nil {
		t.Errorf("missing file: %d, %v", n, err)
	}
}

// A bookmark follows its file when the file or a folder above it is
// renamed; a path that only shares a prefix is left alone.
func TestRepathBookmarks(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	dir := t.TempDir()
	for _, f := range []string{"a/x.sql", "a/deep/y.sql", "ab/z.sql", "b/x.sql"} {
		p := filepath.Join(dir, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("select 1"), 0o644)
	}
	for m, f := range map[string]string{"x": "a/x.sql", "y": "a/deep/y.sql", "z": "ab/z.sql"} {
		if _, err := s.SetBookmark(ctx, m, filepath.Join(dir, f), 1, "select 1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(filepath.Join(dir, "a"), filepath.Join(dir, "c")); err != nil {
		t.Fatal(err)
	}
	if err := s.RepathBookmarks(ctx, filepath.Join(dir, "a"), filepath.Join(dir, "c")); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	bs, err := s.Bookmarks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bs {
		got[b.Mnemonic] = b.Path
	}
	want := map[string]string{"x": filepath.Join(dir, "c/x.sql"), "y": filepath.Join(dir, "c/deep/y.sql"), "z": filepath.Join(dir, "ab/z.sql")}
	for m, p := range want {
		if got[m] != p {
			t.Errorf("%s: %q, want %q", m, got[m], p)
		}
	}
}
