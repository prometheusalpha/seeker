package app

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"seeker/internal/driver"
	"seeker/internal/model"
	"seeker/internal/testenv"
)

// pump stands in for the window's thread: workers publish through Update,
// and the test applies what they queued on its own goroutine, as the window
// would on its next frame.
type pump struct {
	mu   sync.Mutex
	q    []func()
	wake chan struct{}
}

func newPump() *pump { return &pump{wake: make(chan struct{}, 1)} }

func (p *pump) update(fn func()) {
	p.mu.Lock()
	p.q = append(p.q, fn)
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *pump) drain() {
	for {
		p.mu.Lock()
		if len(p.q) == 0 {
			p.mu.Unlock()
			return
		}
		fn := p.q[0]
		p.q = p.q[1:]
		p.mu.Unlock()
		fn()
	}
}

func (p *pump) waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		p.drain()
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// pgProfile reads the URL the recall compose file documents, and skips when
// the suite was not pointed at a server.
func pgProfile(t *testing.T) (model.Profile, string) {
	t.Helper()
	raw := os.Getenv("SEEKER_TEST_PG_URL")
	if raw == "" {
		t.Skip("SEEKER_TEST_PG_URL is not set; start recall/compose.yaml")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port := 5432
	if ps := u.Port(); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil {
			port = n
		}
	}
	password, _ := u.User.Password()
	p := model.Profile{
		ID: "live", Name: "live", DBType: model.Postgres, Host: u.Hostname(), Port: port,
		Username: u.User.Username(), Database: strings.TrimPrefix(u.Path, "/"), ConnectTimeoutSecs: 5,
	}
	return p, password
}

// TestLiveTree walks the sidebar against a real Postgres: connect, list
// databases, schemas, tables, and a table's structs, which is what M2 must
// show.
func TestLiveTree(t *testing.T) {
	profile, password := pgProfile(t)
	testenv.SQLite(t)
	dir := t.TempDir()
	core, err := New(context.Background(), filepath.Join(dir, "seeker.db"), filepath.Join(dir, "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	p := newPump()
	core.Attach(p.update)
	t.Cleanup(core.Close)

	fixtures(t, profile)
	if profile.Database == "" {
		profile.Database = "recall_test"
	}
	core.profiles = []model.Profile{profile}
	core.conn(profile.ID).setPassword(password)
	core.Connect(profile.ID)
	p.waitFor(t, "connection", func() bool { return core.Status(profile.ID) == Connected })

	connNode := Node{Kind: KindConnection, Conn: profile.ID}
	core.tree.List.Open.Add(connNode)
	var dbs []Node
	p.waitFor(t, "databases", func() bool {
		dbs = core.Children(connNode)
		return len(dbs) > 0 && dbs[0].Kind == KindDatabase
	})
	var hasDB bool
	for _, n := range dbs {
		if n.Kind == KindDatabase && n.DB == profile.Database {
			hasDB = true
		}
	}
	if !hasDB {
		t.Fatalf("databases: %+v", dbs)
	}

	dbNode := Node{Kind: KindDatabase, Conn: profile.ID, DB: profile.Database}
	core.tree.List.Open.Add(dbNode)
	var schemas []Node
	p.waitFor(t, "schemas", func() bool {
		schemas = core.Children(dbNode)
		return len(schemas) > 0 && schemas[0].Kind == KindSchema
	})
	var schemaNode Node
	for _, n := range schemas {
		if n.Kind == KindSchema && n.Schema == "seeker_m2" {
			schemaNode = n
		}
	}
	if schemaNode.Kind != KindSchema {
		t.Fatalf("schemas: %+v", schemas)
	}

	core.tree.List.Open.Add(schemaNode)
	var tables []Node
	p.waitFor(t, "tables", func() bool {
		tables = core.Children(schemaNode)
		return len(tables) > 0 && tables[0].Kind == KindTable
	})
	var usersNode Node
	for _, n := range tables {
		if n.Table == "seeker_m2_users" {
			usersNode = n
		}
	}
	if usersNode.Kind != KindTable {
		t.Fatalf("tables: %+v", tables)
	}

	// A table's four groups come from its row, not from the server.
	groups := core.Children(usersNode)
	if len(groups) != 4 || groups[0].Group != GroupColumns {
		t.Fatalf("groups: %+v", groups)
	}
	colsNode := groups[0]
	core.tree.List.Open.Add(colsNode)
	var cols []Node
	p.waitFor(t, "columns", func() bool {
		cols = core.Children(colsNode)
		return len(cols) > 0 && cols[0].Kind == KindLeaf
	})
	var email Node
	for _, n := range cols {
		if n.Name == "email" {
			email = n
		}
	}
	if email.Label != "email (text)" || email.Badge != "Not null" {
		t.Errorf("email column: %+v", email)
	}

	// Indexes and foreign keys load on demand, each in its own group.
	idxNode, fkNode := groups[1], groups[2]
	core.tree.List.Open.Add(idxNode)
	core.tree.List.Open.Add(fkNode)
	var idx, fks []Node
	p.waitFor(t, "indexes and foreign keys", func() bool {
		idx, fks = core.Children(idxNode), core.Children(fkNode)
		return len(idx) > 0 && len(fks) > 0 && idx[0].Kind == KindLeaf && fks[0].Kind == KindLeaf
	})

	// The filter is the server's: only matching tables come back.
	*core.TreeFilter() = "seeker_m2_user"
	core.FilterChanged()
	p.waitFor(t, "filtered tables", func() bool {
		k := core.Children(schemaNode)
		return len(k) == 1 && k[0].Table == "seeker_m2_users"
	})
}

// fixtures builds a schema the tree can walk, and drops it again.
func fixtures(t *testing.T, p model.Profile) {
	t.Helper()
	pw := ""
	if u, err := url.Parse(os.Getenv("SEEKER_TEST_PG_URL")); err == nil {
		pw, _ = u.User.Password()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := driver.Open(ctx, p, pw, p.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stmts := []string{
		"DROP SCHEMA IF EXISTS seeker_m2 CASCADE",
		"CREATE SCHEMA seeker_m2",
		"CREATE TABLE seeker_m2.seeker_m2_accounts (id integer PRIMARY KEY)",
		`CREATE TABLE seeker_m2.seeker_m2_users (
			id integer PRIMARY KEY,
			email text NOT NULL,
			note text,
			account_id integer REFERENCES seeker_m2.seeker_m2_accounts (id),
			created_at timestamptz NOT NULL DEFAULT now()
		)`,
		"CREATE INDEX seeker_m2_users_email_idx ON seeker_m2.seeker_m2_users (email)",
	}
	for _, s := range stmts {
		if res := conn.Execute(ctx, s, 10, nil); len(res) != 1 || res[0].Err != nil {
			t.Fatalf("fixture %q: %+v", s, res)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		conn, err := driver.Open(ctx, p, pw, p.Database)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Execute(ctx, "DROP SCHEMA IF EXISTS seeker_m2 CASCADE", 10, nil)
	})
}

// TestLiveTreeMySQL walks the same levels MySQL has: a database holds
// tables directly, with no schema between them.
func TestLiveTreeMySQL(t *testing.T) {
	raw := os.Getenv("SEEKER_TEST_MYSQL_URL")
	if raw == "" {
		t.Skip("SEEKER_TEST_MYSQL_URL is not set; start recall/compose.yaml")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	password, _ := u.User.Password()
	profile := model.Profile{
		ID: "live-my", Name: "live-my", DBType: model.MySQL, Host: u.Hostname(), Port: port,
		Username: u.User.Username(), Database: strings.TrimPrefix(u.Path, "/"), ConnectTimeoutSecs: 5,
	}
	testenv.SQLite(t)
	dir := t.TempDir()
	core, err := New(context.Background(), filepath.Join(dir, "seeker.db"), filepath.Join(dir, "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	p := newPump()
	core.Attach(p.update)
	t.Cleanup(core.Close)

	mysqlFixtures(t, profile, password)
	core.profiles = []model.Profile{profile}
	core.conn(profile.ID).setPassword(password)
	core.Connect(profile.ID)
	p.waitFor(t, "connection", func() bool { return core.Status(profile.ID) == Connected })

	connNode := Node{Kind: KindConnection, Conn: profile.ID}
	core.tree.List.Open.Add(connNode)
	var dbs []Node
	p.waitFor(t, "databases", func() bool {
		dbs = core.Children(connNode)
		return len(dbs) > 0 && dbs[0].Kind == KindDatabase
	})
	var dbNode Node
	for _, n := range dbs {
		if n.DB == profile.Database {
			dbNode = n
		}
	}
	if dbNode.Kind != KindDatabase {
		t.Fatalf("databases: %+v", dbs)
	}

	core.tree.List.Open.Add(dbNode)
	var tables []Node
	p.waitFor(t, "tables", func() bool {
		tables = core.Children(dbNode)
		return len(tables) > 0 && tables[0].Kind == KindTable
	})
	var usersNode Node
	for _, n := range tables {
		if n.Table == "seeker_m2_users" {
			usersNode = n
		}
	}
	if usersNode.Kind != KindTable || usersNode.Schema != profile.Database {
		t.Fatalf("tables: %+v", tables)
	}

	groups := core.Children(usersNode)
	if len(groups) != 4 {
		t.Fatalf("groups: %+v", groups)
	}
	core.tree.List.Open.Add(groups[0])
	core.tree.List.Open.Add(groups[1])
	var cols, idx []Node
	p.waitFor(t, "columns and indexes", func() bool {
		cols, idx = core.Children(groups[0]), core.Children(groups[1])
		return len(cols) > 0 && cols[0].Kind == KindLeaf && len(idx) > 0 && idx[0].Kind == KindLeaf
	})
	var email Node
	for _, n := range cols {
		if n.Name == "email" {
			email = n
		}
	}
	if email.Badge != "Not null" || !strings.HasPrefix(email.Label, "email (") {
		t.Errorf("email column: %+v", email)
	}
}

// mysqlFixtures builds the table the MySQL walk ends in, and drops it again.
func mysqlFixtures(t *testing.T, p model.Profile, password string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := driver.Open(ctx, p, password, p.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, s := range []string{
		"DROP TABLE IF EXISTS seeker_m2_accounts",
		"DROP TABLE IF EXISTS seeker_m2_users",
		"CREATE TABLE seeker_m2_accounts (id int PRIMARY KEY)",
		`CREATE TABLE seeker_m2_users (
			id int PRIMARY KEY,
			email varchar(120) NOT NULL,
			account_id int,
			KEY seeker_m2_users_email_idx (email),
			CONSTRAINT seeker_m2_users_account FOREIGN KEY (account_id) REFERENCES seeker_m2_accounts (id)
		)`,
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
		conn.Execute(ctx, "DROP TABLE IF EXISTS seeker_m2_users", 10, nil)
		conn.Execute(ctx, "DROP TABLE IF EXISTS seeker_m2_accounts", 10, nil)
	})
}

// TestCancelConnectHoldsTheRow covers the sidebar's Cancel connect: the
// attempt stops, the row stays open and empty, and nothing retries until the
// row is opened again. A listener that never answers stands in for a server
// that hangs.
func TestCancelConnectHoldsTheRow(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var held []net.Conn
	defer func() {
		for _, c := range held {
			c.Close()
		}
	}()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, c)
		}
	}()

	testenv.SQLite(t)
	dir := t.TempDir()
	core, err := New(context.Background(), filepath.Join(dir, "seeker.db"), filepath.Join(dir, "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	p := newPump()
	core.Attach(p.update)
	t.Cleanup(core.Close)

	port := ln.Addr().(*net.TCPAddr).Port
	profile := model.Profile{ID: "hang", Name: "hang", DBType: model.Postgres, Host: "127.0.0.1",
		Port: port, Username: "postgres", Database: "postgres", ConnectTimeoutSecs: 30}
	core.profiles = []model.Profile{profile}
	node := Node{Kind: KindConnection, Conn: profile.ID}
	core.tree.List.Open.Add(node)
	p.waitFor(t, "connecting", func() bool {
		core.Children(node)
		return core.Status(profile.ID) == Connecting
	})
	core.CancelConnect(profile.ID)
	p.drain()
	if st := core.Status(profile.ID); st != Disconnected {
		t.Fatalf("after cancel: %v", st)
	}
	// The empty list is cached, so a frame asking for children neither
	// shows a spinner nor starts another attempt.
	for i := 0; i < 5; i++ {
		time.Sleep(10 * time.Millisecond)
		p.drain()
		kids := core.Children(node)
		if len(kids) != 0 {
			t.Fatalf("children after cancel: %+v", kids)
		}
	}
	if st := core.Status(profile.ID); st != Disconnected {
		t.Fatalf("a cancelled attempt restarted: %v", st)
	}
	// Closing the row drops the empty list, so opening it again retries.
	core.ActivateNode(node)
	core.ActivateNode(node)
	if kids := core.Children(node); len(kids) == 0 {
		t.Fatal("opening the row again did not ask for its children")
	}
}
