package rank

import (
	"reflect"
	"testing"
)

func TestTableDemotion(t *testing.T) {
	cases := []struct {
		name          string
		schema, table string
		want          int
	}{
		{"ordinary tables are not demoted", "public", "users", 0},
		{"underscore-prefixed tables sit below ordinary ones", "public", "_migrations", 1},
		{"system schema pg_catalog", "pg_catalog", "pg_class", 2},
		{"system schema information_schema", "information_schema", "tables", 2},
		{"system schema beats underscore", "pg_toast", "_x", 2},
		{"MySQL system database mysql arrives as the schema", "mysql", "user", 2},
		{"MySQL system database sys", "sys", "sys_config", 2},
		{"schema is case-insensitive", "PG_Catalog", "x", 2},
		{"pg_ prefix covers temp schemas", "pg_temp_3", "x", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TableDemotion(c.schema, c.table); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestMatchFuzzy(t *testing.T) {
	cases := []struct {
		name, q, text string
		ok            bool
		kind          Kind
		score         int
		idx           []int
	}{
		{"exact", "Users", "users", true, Exact, 0, []int{0, 1, 2, 3, 4}},
		{"prefix, penalised by length", "user", "users", true, Prefix, 101, []int{0, 1, 2, 3}},
		{"word prefix after separator", "login", "user_login_table", true, WordPrefix, 211, []int{5, 6, 7, 8, 9}},
		{"word prefix on camel hump", "login", "userLogin", true, WordPrefix, 204, []int{4, 5, 6, 7, 8}},
		{"substring mid-word", "ser", "users", true, Substring, 302, []int{1, 2, 3}},
		{"subsequence", "ue", "users", true, Fuzzy, 403, []int{0, 2}},
		{"no match", "zz", "users", false, 0, 0, nil},
		{"empty query matches nothing", "", "users", false, 0, 0, nil},
		{"penalty is capped", "a", "a" + string(make([]rune, 200)), true, Prefix, 199, []int{0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ok := MatchFuzzy(c.q, c.text)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if !ok {
				return
			}
			if m.Kind != c.kind || m.Score != c.score || !reflect.DeepEqual(m.Indices, c.idx) {
				t.Errorf("got %+v, want kind %v score %d indices %v", m, c.kind, c.score, c.idx)
			}
		})
	}
	a, _ := MatchFuzzy("users", "users")
	b, _ := MatchFuzzy("users", "users_archive_2019")
	if a.Score >= b.Score {
		t.Errorf("shorter text must win in a tier: %d vs %d", a.Score, b.Score)
	}
}
