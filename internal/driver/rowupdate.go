package driver

import (
	"strconv"
	"strings"

	"seeker/internal/model"
)

type statement struct {
	SQL  string
	Args []any
}

// buildRowUpdates is one UPDATE per row with every edited column of it in
// one SET; rows without changes are left out. Arguments follow the text
// order, SET values then key values, since MySQL's `?` binds by
// appearance.
func buildRowUpdates(t model.DBType, schema, table string, keyColumns []string, rows []RowChange) []statement {
	var out []statement
	for _, r := range rows {
		if len(r.Changes) > 0 {
			out = append(out, buildRowUpdate(t, schema, table, keyColumns, r))
		}
	}
	return out
}

func buildRowUpdate(t model.DBType, schema, table string, keyColumns []string, r RowChange) statement {
	n := 0
	placeholder := func() string {
		n++
		if t == model.MySQL {
			return "?"
		}
		return "$" + strconv.Itoa(n)
	}
	set := make([]string, len(r.Changes))
	args := make([]any, 0, len(r.Changes)+len(r.KeyValues))
	for i, c := range r.Changes {
		set[i] = QuoteIdent(t, c.Column) + " = " + placeholder()
		args = append(args, c.Value)
	}
	where := make([]string, len(keyColumns))
	for i, k := range keyColumns {
		where[i] = QuoteIdent(t, k) + " = " + placeholder()
	}
	args = append(args, r.KeyValues...)
	return statement{
		SQL:  "UPDATE " + Qualify(t, schema, table) + " SET " + strings.Join(set, ", ") + " WHERE " + strings.Join(where, " AND "),
		Args: args,
	}
}
