package sqltext

import (
	"slices"
	"testing"
)

func TestBlankOut(t *testing.T) {
	in := "select 'a;b' -- from x\n, \"My T\" /* where */ from t"
	got := BlankOut(in)
	if len(got) != len(in) {
		t.Fatalf("length changed: %d vs %d", len(got), len(in))
	}
	for _, bad := range []string{"a;b", "x", "My T", "where"} {
		if contains(got, bad) {
			t.Errorf("%q survived in %q", bad, got)
		}
	}
	if !contains(got, "from t") || !contains(got, "\n") {
		t.Errorf("code or newline lost: %q", got)
	}
}

func contains(s, sub string) bool { return len(sub) > 0 && indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestStatementScope(t *testing.T) {
	cases := []struct {
		sql     string
		tables  []string
		aliases map[string]string
		ctes    []string
	}{
		{"select * from users u join orders as o on o.uid = u.id where u.x = 1",
			[]string{"users", "orders"}, map[string]string{"u": "users", "o": "orders", "users": "users", "orders": "orders"}, nil},
		{"select * from public.users where 1=1", []string{"users"}, map[string]string{"users": "users"}, nil},
		{"select * from users where a", []string{"users"}, map[string]string{"users": "users"}, nil},
		{"select * from users order by a", []string{"users"}, map[string]string{"users": "users"}, nil},
		{"select * from public. ", nil, map[string]string{}, nil},
		{"select 'from fake' from t -- join nope\n", []string{"t"}, map[string]string{"t": "t"}, nil},
		{"with x as (select 1) select * from x", []string{"x"}, map[string]string{"x": "x"}, nil},
		{"with recent as (select 1) select * from users", []string{"users"}, map[string]string{"users": "users"}, []string{"recent"}},
		{"update users set a = 1", []string{"users"}, map[string]string{"users": "users"}, nil},
		{"select a.from_x from t", []string{"t"}, map[string]string{"t": "t"}, nil},
	}
	for _, c := range cases {
		sc := ParseStatementScope(c.sql)
		if !slices.Equal(sc.Tables, c.tables) {
			t.Errorf("%q: tables %v, want %v", c.sql, sc.Tables, c.tables)
		}
		if len(sc.Aliases) != len(c.aliases) {
			t.Errorf("%q: aliases %v, want %v", c.sql, sc.Aliases, c.aliases)
		}
		for k, v := range c.aliases {
			if sc.Aliases[k] != v {
				t.Errorf("%q: alias %q = %q, want %q", c.sql, k, sc.Aliases[k], v)
			}
		}
		if !slices.Equal(sc.CTEs, c.ctes) {
			t.Errorf("%q: ctes %v, want %v", c.sql, sc.CTEs, c.ctes)
		}
	}
}

func TestSchemaClauseFor(t *testing.T) {
	cases := []struct {
		before string
		want   SchemaClause
	}{
		{"select cre", ClauseColumns},
		{"select a from ", ClauseTables},
		{"select a from t where b", ClauseColumns},
		{"select a from t order by ", ClauseColumns},
		{"select a from t group  by x", ClauseColumns},
		{"select a from t join ", ClauseTables},
		{"update t set ", ClauseColumns},
		{"insert into ", ClauseTables},
		{"", ClauseNone},
		{"delete ", ClauseNone},
		{"select t.order", ClauseColumns},
		{"select * from t where a = 1 and created_at", ClauseColumns},
		{"select * from orders_x", ClauseTables},
	}
	for _, c := range cases {
		if got := SchemaClauseFor(c.before); got != c.want {
			t.Errorf("%q: %v, want %v", c.before, got, c.want)
		}
	}
}

func TestQualifiedAt(t *testing.T) {
	q, p, ok := QualifiedAt("select u.na")
	if !ok || q != "u" || p != "na" {
		t.Errorf("got %q %q %v", q, p, ok)
	}
	if _, _, ok := QualifiedAt("select na"); ok {
		t.Error("a bare word is not qualified")
	}
	if q, p, ok := QualifiedAt("from public. "); !ok || q != "public" || p != "" {
		t.Errorf("space after the dot: %q %q %v", q, p, ok)
	}
}

func TestStatementBounds(t *testing.T) {
	doc := "select 1; select 2; select 3"
	if s, e := StatementBounds(doc, 12); s != 9 || e != 18 {
		t.Errorf("got %d %d", s, e)
	}
	if s, e := StatementBounds(doc, len(doc)); s != 19 || e != len(doc) {
		t.Errorf("end: %d %d", s, e)
	}
}
