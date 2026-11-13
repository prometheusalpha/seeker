package driver

import (
	"context"
	"strings"
	"testing"
	"time"

	"seeker/internal/model"
)

func TestMySQLExecute(t *testing.T) {
	c := liveConn(t, model.MySQL)
	script := "drop table if exists seeker_x;\n" +
		"create table seeker_x (id int primary key, dt datetime(6), ts timestamp(6) null, b varbinary(4), s varchar(10), n decimal(5,2), f float, d double);\n" +
		"set time_zone = '+07:00';\n" +
		"insert into seeker_x values (1, '2026-01-01 06:15:00.123456', '2026-01-01 13:15:00.654321', x'00ff', '', 1.5, 1.1, 0.1), (2, null, null, null, null, null, null, null);\n" +
		"select * from seeker_x order by id;\n" +
		"select nope;\n" +
		"update seeker_x set n = 2"
	t.Cleanup(func() { run(t, c, "drop table if exists seeker_x") })
	rs := run(t, c, script)
	if len(rs) != 7 {
		t.Fatalf("an error must not stop the batch: %d results", len(rs))
	}
	if rs[3].Affected != 2 || rs[6].Affected != 2 {
		t.Fatalf("affected: insert %d, update %d", rs[3].Affected, rs[6].Affected)
	}
	sel := rs[4]
	var types []string
	for _, col := range sel.Columns {
		types = append(types, col.Type)
	}
	if strings.Join(types, ",") != "INT,DATETIME,TIMESTAMP,VARBINARY,VARCHAR,DECIMAL,FLOAT,DOUBLE" {
		t.Fatalf("types %v", types)
	}
	row := sel.Rows[0]
	// The TIMESTAMP is printed in the session's +07:00 and says so.
	if row[0] != "1" || row[1] != "2026-01-01 06:15:00.123456" || row[2] != "2026-01-01 13:15:00.654321+07:00" || row[4] != "" || row[5] != "1.50" || row[6] != "1.1" || row[7] != "0.1" {
		t.Fatalf("text not kept: %#v", row)
	}
	if b, ok := row[3].([]byte); !ok || len(b) != 2 || b[1] != 0xff {
		t.Fatalf("varbinary: %#v", row[3])
	}
	if sel.Rows[1][4] != nil || sel.Rows[1][2] != nil {
		t.Fatalf("NULL: %#v", sel.Rows[1])
	}
	if e := rs[5].Err; e == nil || e.Code != "1054" || e.Offset != -1 {
		t.Fatalf("error %+v", e)
	}
	r := c.Execute(context.Background(), "select 1 union all select 2 union all select 3", 2, nil)[0]
	if len(r.Rows) != 2 || !r.Truncated {
		t.Fatalf("cap: %d rows, truncated %v", len(r.Rows), r.Truncated)
	}
}

func TestMySQLCancelKeepsSession(t *testing.T) {
	c := liveConn(t, model.MySQL)
	mustOK(t, run(t, c, "set @keep = 42"))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	rs := c.Execute(ctx, "select sleep(10); select 1", MaxRows, nil)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("cancel took %v", time.Since(start))
	}
	// SLEEP answers a KILL QUERY with 1 rather than an error.
	if len(rs) != 1 {
		t.Fatalf("the rest of the batch must not run, got %d results", len(rs))
	}
	if c.IsClosed() {
		t.Fatal("cancel closed the session")
	}
	rs = run(t, c, "select @keep")
	mustOK(t, rs)
	if rs[0].Rows[0][0] != "42" {
		t.Fatalf("session variable lost: %#v", rs[0].Rows)
	}
	// A statement that is not a sleep fails with 1317, and the session lives.
	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	rs = c.Execute(ctx, "select count(*) from information_schema.columns a, information_schema.columns b, information_schema.columns c", MaxRows, nil)
	if len(rs) != 1 || rs[0].Err == nil || rs[0].Err.Code != "1317" {
		t.Fatalf("want interrupted, got %+v", rs)
	}
	if c.IsClosed() {
		t.Fatal("cancel closed the session")
	}
	mustOK(t, run(t, c, "select 1"))
}

// TestMySQLQueryDatabase: a session opened in a database resolves
// unqualified names there, as recall's mysqlQueryDatabase test checks.
func TestMySQLQueryDatabase(t *testing.T) {
	p, pw, db := liveProfile(t, model.MySQL)
	admin := liveConn(t, model.MySQL)
	mustOK(t, run(t, admin, "create database if not exists seeker_other; drop table if exists seeker_other.t; create table seeker_other.t (v varchar(16)); insert into seeker_other.t values ('other');"+
		"drop table if exists "+db+".t; create table "+db+".t (v varchar(16)); insert into "+db+".t values ('default')"))
	t.Cleanup(func() { run(t, admin, "drop database if exists seeker_other; drop table if exists "+db+".t") })
	for _, want := range []struct{ db, marker string }{{"seeker_other", "other"}, {db, "default"}} {
		c, err := Open(context.Background(), p, pw, want.db)
		if err != nil {
			t.Fatal(err)
		}
		rs := run(t, c, "select v from t")
		c.Close()
		mustOK(t, rs)
		if rs[0].Rows[0][0] != want.marker {
			t.Fatalf("%s: got %v", want.db, rs[0].Rows)
		}
	}
}

// TestMySQLCachingSha2 logs in a caching_sha2 account over plain TCP,
// which needs the server's RSA key (recall's mysqlPublicKeyRetrieval).
func TestMySQLCachingSha2(t *testing.T) {
	p, _, db := liveProfile(t, model.MySQL)
	admin := liveConn(t, model.MySQL)
	mustOK(t, run(t, admin, "drop user if exists seeker_sha2@'%'; create user seeker_sha2@'%' identified with caching_sha2_password by 'pw';"+
		"grant all on "+db+".* to seeker_sha2@'%'; flush privileges"))
	t.Cleanup(func() { run(t, admin, "drop user if exists seeker_sha2@'%'") })
	p.Username = "seeker_sha2"
	for _, database := range []string{db, ""} {
		c, err := Open(context.Background(), p, "pw", database)
		if err != nil {
			t.Fatalf("login in %q: %v", database, err)
		}
		rs := run(t, c, "select current_user()")
		c.Close()
		mustOK(t, rs)
		if rs[0].Rows[0][0] != "seeker_sha2@%" {
			t.Fatalf("user %v", rs[0].Rows)
		}
	}
}

func TestMySQLCatalog(t *testing.T) {
	c := liveConn(t, model.MySQL)
	db := c.Database()
	mustOK(t, run(t, c, `drop table if exists seeker_child; drop table if exists seeker_parent;
create table seeker_parent (id int primary key, code varchar(8) not null) comment 'parents';
create table seeker_child (
  id int auto_increment primary key,
  parent_id int comment 'who''s the parent',
  name varchar(20) default 'x',
  created timestamp not null default current_timestamp on update current_timestamp,
  total int as (char_length(name)) stored,
  unique key child_name (name, parent_id),
  constraint child_parent foreign key (parent_id) references seeker_parent(id) on delete cascade
);
create trigger seeker_child_touch before insert on seeker_child for each row set new.name = coalesce(new.name, 'y')`))
	t.Cleanup(func() { run(t, c, "drop table if exists seeker_child; drop table if exists seeker_parent") })
	ctx := context.Background()

	if s, err := c.ListSchemas(ctx, true); err != nil || len(s) != 0 {
		t.Fatalf("mysql has no schema level: %v %v", s, err)
	}
	dbs, err := c.ListDatabases(ctx, "")
	if err != nil || !contains(names(dbs), db) || contains(names(dbs), "mysql") {
		t.Fatalf("databases %+v %v", dbs, err)
	}
	tables, err := c.ListTables(ctx, db, "seeker_")
	if err != nil || len(tables) != 2 || tables[1].Name != "seeker_parent" || tables[1].Comment != "parents" || tables[1].Type != "table" {
		t.Fatalf("tables %+v %v", tables, err)
	}
	cols, err := c.ListColumns(ctx, db, "seeker_child")
	if err != nil || len(cols) != 5 || !cols[0].PrimaryKey || cols[1].Comment != "who's the parent" {
		t.Fatalf("columns %+v %v", cols, err)
	}
	if !strings.Contains(cols[0].DDL, "AUTO_INCREMENT") || !strings.Contains(cols[2].DDL, "DEFAULT 'x'") ||
		!strings.Contains(cols[3].DDL, "DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP") || !strings.Contains(cols[4].DDL, "GENERATED ALWAYS AS") {
		t.Fatalf("column ddl:\n%s\n%s\n%s\n%s", cols[0].DDL, cols[2].DDL, cols[3].DDL, cols[4].DDL)
	}
	idx, err := c.ListIndexes(ctx, db, "seeker_child")
	if err != nil || len(idx) != 3 {
		t.Fatalf("indexes %+v %v", idx, err)
	}
	byName := map[string]IndexInfo{}
	for _, ix := range idx {
		byName[ix.Name] = ix
	}
	if ix := byName["child_name"]; !ix.Unique || strings.Join(ix.Columns, ",") != "name,parent_id" || !strings.HasPrefix(ix.DDL, "CREATE UNIQUE INDEX") {
		t.Fatalf("child_name %+v", ix)
	}
	if !byName["PRIMARY"].Primary {
		t.Fatal("PRIMARY")
	}
	fks, err := c.ListForeignKeys(ctx, db, "seeker_child")
	if err != nil || len(fks) != 1 || fks[0].ReferencedTable != "seeker_parent" || fks[0].OnDelete != "CASCADE" {
		t.Fatalf("fks %+v %v", fks, err)
	}
	trg, err := c.ListTriggers(ctx, db, "seeker_child")
	if err != nil || len(trg) != 1 || trg[0].Timing != "BEFORE" || trg[0].Event != "INSERT" {
		t.Fatalf("triggers %+v %v", trg, err)
	}
	ddl, err := c.TableDDL(ctx, db, "seeker_child")
	if err != nil || !strings.HasPrefix(ddl, "CREATE TABLE `seeker_child`") {
		t.Fatalf("ddl %q %v", ddl, err)
	}
	mustOK(t, run(t, c, "insert into seeker_parent values (1, 'a'), (2, 'b')"))
	if n, err := c.CountRows(ctx, db, "seeker_parent", "code = 'b'"); err != nil || n != 1 {
		t.Fatalf("count %d %v", n, err)
	}
	n, err := c.UpdateRows(ctx, db, "seeker_parent", []string{"id"}, []RowChange{{KeyValues: []any{"1"}, Changes: []CellChange{{"code", "A"}}}})
	if err != nil || n != 1 {
		t.Fatalf("update %d %v", n, err)
	}
	if _, err := c.UpdateRows(ctx, db, "seeker_parent", []string{"id"}, []RowChange{
		{KeyValues: []any{"1"}, Changes: []CellChange{{"code", "Z"}}},
		{KeyValues: []any{"2"}, Changes: []CellChange{{"code", nil}}},
	}); err == nil {
		t.Fatal("NOT NULL violation should fail")
	}
	if rs := run(t, c, "select code from seeker_parent where id = 1"); rs[0].Rows[0][0] != "A" {
		t.Fatal("earlier row was not rolled back")
	}
	p, err := c.Explain(ctx, "select * from seeker_parent where id = 1")
	if err != nil || !strings.Contains(p.Raw, "query_block") {
		t.Fatalf("explain %+v %v", p, err)
	}
	info, err := c.Ping(ctx)
	if err != nil || info.ProductName != "MySQL" || info.CurrentDatabase != db {
		t.Fatalf("ping %+v %v", info, err)
	}
}
