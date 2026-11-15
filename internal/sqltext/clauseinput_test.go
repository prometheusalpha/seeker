package sqltext

import (
	"slices"
	"testing"
)

func TestClauseCompletions(t *testing.T) {
	cols := []string{"id", "user_name", "Created At"}
	cases := []struct {
		name  string
		kind  ClauseKind
		value string
		d     Dialect
		want  []string
	}{
		{"column at the start", ClauseWhere, "us", Postgres, []string{"user_name"}},
		{"column after a connector", ClauseWhere, "id = 1 AND cr", Postgres, []string{`id = 1 AND "Created At"`}},
		{"connector after a value", ClauseWhere, "id = 1 a", Postgres, []string{"id = 1 AND "}},
		{"order by", ClauseOrderBy, "id, us", MySQL, []string{"id, user_name"}},
		{"inside a string", ClauseWhere, "user_name = 'us", Postgres, nil},
		{"a complete column", ClauseWhere, "id", Postgres, []string{}},
	}
	for _, tc := range cases {
		got := ClauseCompletions(tc.kind, tc.value, cols, tc.d)
		if len(got) == 0 && len(tc.want) == 0 {
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: ClauseCompletions(%q) = %q, want %q", tc.name, tc.value, got, tc.want)
		}
	}
}
