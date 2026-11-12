package sqltext

import (
	"reflect"
	"testing"
)

func sqls(ss []Statement) []string {
	out := []string{}
	for _, s := range ss {
		out = append(out, s.SQL)
	}
	return out
}

func TestSplit(t *testing.T) {
	cases := []struct {
		name string
		d    Dialect
		sql  string
		want []string
	}{
		{"basic", Postgres, "select 1; select 2;", []string{"select 1", "select 2"}},
		{"no trailing semicolon", Postgres, "select 1;\n  select 2  \n", []string{"select 1", "select 2"}},
		{"empty fragments", Postgres, ";;  ; select 1;;", []string{"select 1"}},
		{"semicolon in string", Postgres, "select 'a;b'; select 2", []string{"select 'a;b'", "select 2"}},
		{"doubled quote", Postgres, "select 'it''s;'; x", []string{"select 'it''s;'", "x"}},
		{"quoted identifier", Postgres, `select "a;b" from t; x`, []string{`select "a;b" from t`, "x"}},
		{"line comment", Postgres, "select 1 -- a;b\n; x", []string{"select 1", "x"}},
		{"block comment", Postgres, "select /* ; */ 1; x", []string{"select /* ; */ 1", "x"}},
		{"leading comments are not sent", Postgres, "-- report\n/* v2 */ select 1;", []string{"select 1"}},
		{"comment-only tail dropped", MySQL, "select 1;\n-- done", []string{"select 1"}},
		{"dollar quote", Postgres, "create function f() returns int as $$ select 1; $$ language sql; x",
			[]string{"create function f() returns int as $$ select 1; $$ language sql", "x"}},
		{"tagged dollar quote", Postgres, "do $body$ begin; $$ end $body$; x", []string{"do $body$ begin; $$ end $body$", "x"}},
		{"positional parameter is not a tag", Postgres, "select $1; select $2", []string{"select $1", "select $2"}},
		{"dollar is plain in MySQL", MySQL, "select '$$'; select 2", []string{"select '$$'", "select 2"}},
		{"mysql hash comment", MySQL, "select 1 # a;b\n; x", []string{"select 1", "x"}},
		{"hash is code in Postgres", Postgres, "select '{a}'::jsonb #> '{a}'; x", []string{"select '{a}'::jsonb #> '{a}'", "x"}},
		{"mysql backslash escape", MySQL, `select 'it\'s; x'; y`, []string{`select 'it\'s; x'`, "y"}},
		{"backslash is plain in Postgres", Postgres, `select 'a\'; select 2`, []string{`select 'a\'`, "select 2"}},
		{"backtick", MySQL, "select `a;b`; x", []string{"select `a;b`", "x"}},
		{"unterminated string swallows the rest", Postgres, "select 'abc; select 2", []string{"select 'abc; select 2"}},
		{"multibyte", Postgres, "select 'ồ;'; select 'ế'", []string{"select 'ồ;'", "select 'ế'"}},
	}
	for _, c := range cases {
		if got := sqls(Split(c.sql, c.d)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Split(%q) = %q, want %q", c.name, c.sql, got, c.want)
		}
	}
}

func TestSplitRanges(t *testing.T) {
	sql := "-- head\nselect 1;  \n\n  select 2"
	got := Split(sql, Postgres)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	// The range starts at the code and takes the `;`.
	if s := sql[got[0].Start:got[0].End]; s != "select 1;" {
		t.Errorf("first range %q", s)
	}
	if s := sql[got[1].Start:got[1].End]; s != "select 2" {
		t.Errorf("second range %q", s)
	}
}

func TestAt(t *testing.T) {
	sql := "select 1;select 2;\n\nselect 3"
	at := func(pos int) string {
		s, ok := At(sql, Postgres, pos)
		if !ok {
			return "<none>"
		}
		return s.SQL
	}
	cases := map[int]string{
		0:        "select 1",
		8:        "select 1",
		9:        "select 2", // right after `;`, where the next starts
		18:       "select 2", // after the second `;`, before the blank line
		19:       "<none>",
		len(sql): "select 3",
		-1:       "<none>",
	}
	for pos, want := range cases {
		if got := at(pos); got != want {
			t.Errorf("At(%d) = %q, want %q", pos, got, want)
		}
	}
}
