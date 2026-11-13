package driver

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"seeker/internal/model"
)

// liveProfile reads SEEKER_TEST_PG_URL / SEEKER_TEST_MYSQL_URL, which
// point at ../recall/compose.yaml:
//
//	SEEKER_TEST_PG_URL=postgres://postgres:postgres@127.0.0.1:55432/recall_test
//	SEEKER_TEST_MYSQL_URL=mysql://root:mysql@127.0.0.1:53306/recall_test
func liveProfile(t *testing.T, typ model.DBType) (model.Profile, string, string) {
	t.Helper()
	env := map[model.DBType]string{model.Postgres: "SEEKER_TEST_PG_URL", model.MySQL: "SEEKER_TEST_MYSQL_URL"}[typ]
	raw := os.Getenv(env)
	if raw == "" {
		t.Skipf("%s is not set; start ../recall/compose.yaml and set it to run this test", env)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	pw, _ := u.User.Password()
	p := model.Profile{ID: "live-" + string(typ), DBType: typ, Host: u.Hostname(), Port: port, Username: u.User.Username(),
		ConnectTimeoutSecs: 5, URLParams: u.RawQuery, Database: strings.TrimPrefix(u.Path, "/")}
	return p, pw, p.Database
}

func liveConn(t *testing.T, typ model.DBType) Conn {
	t.Helper()
	p, pw, db := liveProfile(t, typ)
	c, err := Open(context.Background(), p, pw, db)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func run(t *testing.T, c Conn, script string) []Result {
	t.Helper()
	return c.Execute(context.Background(), script, MaxRows, nil)
}

func mustOK(t *testing.T, rs []Result) {
	t.Helper()
	for _, r := range rs {
		if r.Err != nil {
			t.Fatalf("statement %d %q: %v", r.Index, r.Statement.SQL, r.Err)
		}
	}
}

func TestPostgresExecute(t *testing.T) {
	c := liveConn(t, model.Postgres)
	script := "create temp table x (id int primary key, ts timestamptz, wall timestamp, b bytea, n numeric);\n" +
		"insert into x values (1, '2026-01-01 06:15:00.123456+00', '2026-01-01 06:15:00.654321', '\\x00ff', 1.50), (2, null, null, null, null);\n" +
		"select * from x order by id;\n" +
		"select nope;\n" +
		"update x set n = 2"
	var progress []int
	rs := c.Execute(context.Background(), script, MaxRows, func(r Result) { progress = append(progress, r.Index) })
	if len(rs) != 5 || len(progress) != 5 {
		t.Fatalf("an error must not stop the batch: %d results, progress %v", len(rs), progress)
	}
	if rs[1].Affected != 2 || rs[4].Affected != 2 {
		t.Fatalf("affected: insert %d, update %d", rs[1].Affected, rs[4].Affected)
	}
	sel := rs[2]
	types := []string{}
	for _, col := range sel.Columns {
		types = append(types, col.Type)
	}
	if strings.Join(types, ",") != "integer,timestamp with time zone,timestamp without time zone,bytea,numeric" {
		t.Fatalf("types %v", types)
	}
	row := sel.Rows[0]
	if row[0] != "1" || !strings.HasPrefix(row[1].(string), "2026-01-01 06:15:00.123456") || row[2] != "2026-01-01 06:15:00.654321" || row[4] != "1.50" {
		t.Fatalf("text not kept: %#v", row)
	}
	if b, ok := row[3].([]byte); !ok || len(b) != 2 || b[0] != 0 || b[1] != 0xff {
		t.Fatalf("bytea: %#v", row[3])
	}
	if sel.Rows[1][1] != nil {
		t.Fatalf("NULL: %#v", sel.Rows[1][1])
	}
	e := rs[3].Err
	if e == nil || e.Code != "42703" || script[e.Offset:e.Offset+4] != "nope" {
		t.Fatalf("error position: %+v", e)
	}
}

func TestPostgresErrorOffsetCountsCharacters(t *testing.T) {
	c := liveConn(t, model.Postgres)
	script := "select 'ảảả';\nselect 'đ', nope"
	e := run(t, c, script)[1].Err
	if e == nil || script[e.Offset:] != "nope" {
		t.Fatalf("got %+v", e)
	}
}

func TestPostgresCapAndDDL(t *testing.T) {
	c := liveConn(t, model.Postgres)
	r := c.Execute(context.Background(), "select g from generate_series(1, 10) g", 3, nil)[0]
	if len(r.Rows) != 3 || !r.Truncated || r.Affected != 10 {
		t.Fatalf("cap: %d rows, truncated %v, affected %d", len(r.Rows), r.Truncated, r.Affected)
	}
	// A cached plan would fail the second select after the type change.
	rs := run(t, c, "create temp table d (a int); insert into d values (1); select * from d; alter table d alter a type text; select * from d")
	mustOK(t, rs)
	if rs[4].Columns[0].Type != "text" {
		t.Fatalf("type after DDL: %q", rs[4].Columns[0].Type)
	}
}

func TestPostgresCancelKeepsSession(t *testing.T) {
	c := liveConn(t, model.Postgres)
	mustOK(t, run(t, c, "create temp table keep (v int); insert into keep values (42)"))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	rs := c.Execute(ctx, "select pg_sleep(10); select 1", MaxRows, nil)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("cancel took %v", time.Since(start))
	}
	if len(rs) != 1 || rs[0].Err == nil || rs[0].Err.Code != "57014" {
		t.Fatalf("want one cancelled statement, got %+v", rs)
	}
	if c.IsClosed() {
		t.Fatal("cancel closed the session")
	}
	rs = run(t, c, "select v from keep")
	mustOK(t, rs)
	if rs[0].Rows[0][0] != "42" {
		t.Fatal("temp table lost after cancel")
	}
}

func TestPostgresCatalog(t *testing.T) {
	c := liveConn(t, model.Postgres)
	mustOK(t, run(t, c, `drop schema if exists seeker_cat cascade;
create schema seeker_cat;
create table seeker_cat.parent (id int primary key, code text not null);
create table seeker_cat.child (
  id int generated always as identity primary key,
  parent_id int references seeker_cat.parent(id) on delete cascade,
  "na""me" text default 'x',
  created timestamptz not null default now()
);
comment on table seeker_cat.child is 'kids';
comment on column seeker_cat.child.parent_id is 'who''s the parent';
create unique index child_name on seeker_cat.child ("na""me") where parent_id is not null;
create function seeker_cat.noop() returns trigger language plpgsql as $$ begin return new; end $$;
create trigger child_touch before insert or update on seeker_cat.child for each row execute function seeker_cat.noop();
create view seeker_cat.v as select id from seeker_cat.child`))
	t.Cleanup(func() { run(t, c, "drop schema seeker_cat cascade") })
	ctx := context.Background()

	schemas, err := c.ListSchemas(ctx, false)
	if err != nil || !contains(schemas, "seeker_cat") || contains(schemas, "information_schema") {
		t.Fatalf("schemas %v %v", schemas, err)
	}
	sys, _ := c.ListSchemas(ctx, true)
	if !contains(sys, "pg_catalog") {
		t.Fatalf("system schemas %v", sys)
	}
	tables, err := c.ListTables(ctx, "seeker_cat", "")
	if err != nil || len(tables) != 3 || tables[0].Name != "child" || tables[0].Comment != "kids" || tables[2].Type != "view" {
		t.Fatalf("tables %+v %v", tables, err)
	}
	if f, _ := c.ListTables(ctx, "seeker_cat", "PAR"); len(f) != 1 || f[0].Name != "parent" {
		t.Fatalf("filter %+v", f)
	}
	cols, err := c.ListColumns(ctx, "seeker_cat", "child")
	if err != nil || len(cols) != 4 || !cols[0].PrimaryKey || cols[1].Comment != "who's the parent" || cols[2].Default != "'x'::text" {
		t.Fatalf("columns %+v %v", cols, err)
	}
	if !strings.Contains(cols[1].DDL, `COMMENT ON COLUMN "seeker_cat"."child"."parent_id" IS 'who''s the parent'`) ||
		!strings.Contains(cols[0].DDL, "GENERATED ALWAYS AS IDENTITY NOT NULL") {
		t.Fatalf("column ddl %q / %q", cols[0].DDL, cols[1].DDL)
	}
	idx, err := c.ListIndexes(ctx, "seeker_cat", "child")
	if err != nil || len(idx) != 2 || idx[0].Name != "child_name" || idx[0].Condition != "(parent_id IS NOT NULL)" || idx[0].Columns[0] != `na"me` || !idx[1].Primary {
		t.Fatalf("indexes %+v %v", idx, err)
	}
	fks, err := c.ListForeignKeys(ctx, "seeker_cat", "child")
	if err != nil || len(fks) != 1 || fks[0].ReferencedTable != "parent" || fks[0].OnDelete != "CASCADE" || fks[0].OnUpdate != "NO ACTION" {
		t.Fatalf("fks %+v %v", fks, err)
	}
	trg, err := c.ListTriggers(ctx, "seeker_cat", "child")
	if err != nil || len(trg) != 1 || trg[0].Timing != "BEFORE" || trg[0].Event != "INSERT OR UPDATE" {
		t.Fatalf("triggers %+v %v", trg, err)
	}
	ddl, err := c.TableDDL(ctx, "seeker_cat", "child")
	if err != nil || !strings.HasPrefix(ddl, `CREATE TABLE "seeker_cat"."child" (`) || !strings.Contains(ddl, "FOREIGN KEY") || !strings.Contains(ddl, "COMMENT ON TABLE") {
		t.Fatalf("ddl %s %v", ddl, err)
	}
	// The DDL must rebuild the table.
	mustOK(t, run(t, c, "create schema seeker_cat2; set search_path = seeker_cat2, seeker_cat; "+strings.ReplaceAll(ddl, `"seeker_cat"."child"`, `"seeker_cat2"."child"`)+"; reset search_path; drop schema seeker_cat2 cascade"))
	if v, err := c.TableDDL(ctx, "seeker_cat", "v"); err != nil || !strings.HasPrefix(v, `CREATE VIEW "seeker_cat"."v" AS`) || strings.Contains(v, ";;") {
		t.Fatalf("view ddl %q %v", v, err)
	}
	if _, err := c.TableDDL(ctx, "seeker_cat", "missing"); err == nil {
		t.Fatal("missing table should fail")
	}
	mustOK(t, run(t, c, "insert into seeker_cat.parent values (1, 'a'), (2, 'b')"))
	if n, err := c.CountRows(ctx, "seeker_cat", "parent", "code = 'b'"); err != nil || n != 1 {
		t.Fatalf("count %d %v", n, err)
	}
	if _, err := c.CountRows(ctx, "seeker_cat", "parent", "true; drop table seeker_cat.parent"); err == nil {
		t.Fatal("a second statement in WHERE must be refused")
	}
	dbs, err := c.ListDatabases(ctx, "")
	if err != nil || !contains(names(dbs), c.Database()) {
		t.Fatalf("databases %+v %v", dbs, err)
	}
	info, err := c.Ping(ctx)
	if err != nil || info.ProductName != "PostgreSQL" || info.CurrentDatabase != c.Database() {
		t.Fatalf("ping %+v %v", info, err)
	}
}

func TestPostgresExplain(t *testing.T) {
	c := liveConn(t, model.Postgres)
	mustOK(t, run(t, c, "create temp table e (id int)"))
	p, err := c.Explain(context.Background(), "delete from e")
	if err != nil || !strings.HasPrefix(p.SQL, "EXPLAIN (FORMAT JSON)") || !strings.Contains(p.Raw, `"Node Type"`) {
		t.Fatalf("plan %+v %v", p, err)
	}
	p, err = c.Explain(context.Background(), "explain select 1")
	if err != nil || p.SQL != "explain select 1" || !strings.Contains(p.Raw, "Result") {
		t.Fatalf("own explain %+v %v", p, err)
	}
	if _, err := c.Explain(context.Background(), "select nope"); err == nil {
		t.Fatal("explain error expected")
	}
}

func TestPostgresUpdateRows(t *testing.T) {
	c := liveConn(t, model.Postgres)
	mustOK(t, run(t, c, "create temp table ru (id int primary key, name text, n int); insert into ru values (1, 'a', 1), (2, 'b', 2)"))
	ctx := context.Background()
	n, err := c.UpdateRows(ctx, "", "ru", []string{"id"}, []RowChange{
		{KeyValues: []any{"1"}, Changes: []CellChange{{"name", "A"}, {"n", "10"}}},
		{KeyValues: []any{"2"}, Changes: []CellChange{{"name", nil}}},
	})
	if err != nil || n != 2 {
		t.Fatalf("update %d %v", n, err)
	}
	rs := run(t, c, "select id, name, n from ru order by id")
	if got := rs[0].Rows; got[0][1] != "A" || got[0][2] != "10" || got[1][1] != nil {
		t.Fatalf("rows %v", got)
	}
	_, err = c.UpdateRows(ctx, "", "ru", []string{"id"}, []RowChange{
		{KeyValues: []any{"1"}, Changes: []CellChange{{"name", "Z"}}},
		{KeyValues: []any{"2"}, Changes: []CellChange{{"n", "not a number"}}},
	})
	if err == nil {
		t.Fatal("bad value should fail")
	}
	if rs := run(t, c, "select name from ru where id = 1"); rs[0].Rows[0][0] != "A" {
		t.Fatal("earlier row was not rolled back")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func names(dbs []DatabaseInfo) []string {
	var out []string
	for _, d := range dbs {
		out = append(out, d.Name)
	}
	return out
}
