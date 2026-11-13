package sqltext

import (
	"reflect"
	"testing"
)

const pgQuote = '"'

func at(kind ClauseKind, value string, quote byte) ClauseTarget {
	return ClauseTargetAt(kind, value, len(value), len(value), quote)
}

func TestClauseTarget(t *testing.T) {
	cases := []struct {
		name           string
		kind           ClauseKind
		value          string
		caret, selEnd  int
		quote          byte
		token          string
		from, to       int
		quoted, inside bool
	}{
		{"reads the identifier run the caret sits in", ClauseWhere, "id = na", 7, 7, pgQuote, "na", 5, 7, false, false},
		{"extends the run past the caret, so a pick replaces the whole name", ClauseWhere, "id = name", 7, 7, pgQuote, "na", 5, 9, false, false},
		{"ends the run at the operator that precedes it", ClauseWhere, "a=na", 4, 4, pgQuote, "na", 2, 4, false, false},
		{"keeps dashes inside an ORDER BY name", ClauseOrderBy, "created-at na", 13, 13, 0, "na", 11, 13, false, false},
		{"reports a caret inside a string literal, where nothing is suggested", ClauseWhere, "name = 'Ali", 11, 11, pgQuote, "", 11, 11, false, true},
		// The closing quote is inside the value, so the name is replaced
		// alone and the quotes the user typed survive.
		{"reads only the content of the quoted identifier under the caret", ClauseWhere, `name = "OrderId"`, 11, 11, pgQuote, "Ord", 8, 15, true, false},
		{"replaces the selection when there is one", ClauseWhere, "id = old", 5, 8, pgQuote, "old", 5, 8, false, false},
		{"leaves ORDER BY unquoted: no dialect quote is read there", ClauseOrderBy, `"Ord`, 4, 4, pgQuote, `"Ord`, 0, 4, false, false},
		{"multibyte names are one run", ClauseWhere, "id = tên", 9, 9, pgQuote, "tên", 5, 9, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClauseTargetAt(c.kind, c.value, c.caret, c.selEnd, c.quote)
			want := ClauseTarget{c.value, c.token, c.from, c.to, c.quoted, c.inside}
			if got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestClauseRole(t *testing.T) {
	// The caret is always at the end of the value, which is where typing
	// puts it.
	cases := []struct {
		name, value string
		want        ClauseRole
	}{
		{"a field is asked for by an empty fragment", "", RoleField},
		{"a field follows a connector", "status = 'active' AND ", RoleField},
		{"a field follows a negation", "id = 1 AND NOT ", RoleField},
		{"a field follows an operator", "id = a", RoleField},
		{"a field follows a quoted identifier", `name = "OrderId" AND `, RoleField},
		{"a connector follows a complete expression", "owner_id = 1 a", RoleConnector},
		{"a connector follows a finished literal", "id = 1 ", RoleConnector},
		{"a value operand has nothing to suggest", "name = 'Alice a", RoleNone},
		{"IS introduces a value", "deleted_at IS a", RoleNone},
		{"IS NOT introduces a value", "deleted_at IS NOT o", RoleNone},
		{"LIKE introduces a value", "name LIKE o", RoleNone},
		{"IN introduces a value", "id IN a", RoleNone},
		{"BETWEEN introduces a value", "score BETWEEN o", RoleNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClauseRoleOf(ClauseWhere, at(ClauseWhere, c.value, pgQuote)); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}

	t.Run("ORDER BY is a field list: it has no connectors", func(t *testing.T) {
		if got := ClauseRoleOf(ClauseOrderBy, at(ClauseOrderBy, "name AND a", 0)); got != RoleField {
			t.Errorf("got %v", got)
		}
	})
	t.Run("reads ` as the identifier quote on MySQL, so `\"` opens a string", func(t *testing.T) {
		if got := ClauseRoleOf(ClauseWhere, at(ClauseWhere, `name = "Ali`, MySQL.IdentQuote())); got != RoleNone {
			t.Errorf("got %v", got)
		}
	})
}

func TestClauseConnectors(t *testing.T) {
	cases := []struct {
		name, token string
		want        []string
	}{
		{"offers both connectors once nothing of one is typed", "", []string{"AND", "OR"}},
		{"narrows by the typed prefix: a", "a", []string{"AND"}},
		{"narrows by the typed prefix: o", "o", []string{"OR"}},
		{"narrows case-insensitively", "aN", []string{"AND"}},
		{"drops a connector fully typed: and", "and", []string{}},
		{"drops a connector fully typed: OR", "OR", []string{}},
		{"offers nothing for a token neither starts with", "x", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClauseConnectors(c.token); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func columnsOf(rows []ClauseFieldSuggestion) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Column)
	}
	return out
}

func TestClauseFieldSuggestions(t *testing.T) {
	pg, my := Postgres, MySQL
	cases := []struct {
		name string
		opts ClauseFieldOptions
		want []string
	}{
		{"ranks a prefix match, shortest name first", ClauseFieldOptions{Columns: []string{"namespace", "name", "id"}, Token: "na"}, []string{"name", "namespace"}},
		{"keeps fuzzy matches and drops what the token cannot reach", ClauseFieldOptions{Columns: []string{"customer_id", "order_id", "category_id"}, Token: "cid"}, []string{"customer_id", "category_id"}},
		{"offers nothing when no column matches the token", ClauseFieldOptions{Columns: []string{"alpha", "beta"}, Token: "zzz"}, []string{}},
		{"lists every column, in order, for an empty token", ClauseFieldOptions{Columns: []string{"beta", "alpha"}}, []string{"beta", "alpha"}},
		{"ends the list once the token IS a column: the name is complete", ClauseFieldOptions{Columns: []string{"id", "name"}, Token: "ID"}, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := columnsOf(ClauseFieldSuggestions(c.opts)); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}

	// `lifetime_spent > 10` with the caret at the end: `10` is the operand,
	// and a column list here is what made Enter replace it.
	for _, value := range []string{"lifetime_spent > 10", "name = 'Alice'"} {
		t.Run("offers nothing for the operand just typed: "+value, func(t *testing.T) {
			tg := at(ClauseWhere, value, pgQuote)
			got := ClauseFieldSuggestions(ClauseFieldOptions{Columns: []string{"lifetime_spent", "user_id", "id", "name"}, Token: tg.Token, QuotedIdentifier: tg.QuotedIdentifier})
			if len(got) != 0 {
				t.Errorf("got %v", got)
			}
		})
	}

	t.Run("inserts the bare name inside quotes the user already typed", func(t *testing.T) {
		got := ClauseFieldSuggestions(ClauseFieldOptions{Columns: []string{"OrderId"}, Token: "Ord", QuotedIdentifier: true, Dialect: &pg})
		if want := []ClauseFieldSuggestion{{"OrderId", "OrderId"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v", got)
		}
	})

	for _, c := range []struct {
		d            *Dialect
		column, want string
	}{
		{&pg, "order_id", "order_id"},
		{&pg, "OrderId", `"OrderId"`},
		{&my, "OrderId", "OrderId"},
	} {
		t.Run("quotes the insert when the dialect cannot take the name bare: "+c.column, func(t *testing.T) {
			got := ClauseFieldSuggestions(ClauseFieldOptions{Columns: []string{c.column}, Dialect: c.d})
			if want := []ClauseFieldSuggestion{{c.column, c.want}}; !reflect.DeepEqual(got, want) {
				t.Errorf("got %v", got)
			}
		})
	}
}

func TestClauseInsertText(t *testing.T) {
	pg, my := Postgres, MySQL
	cases := []struct {
		name   string
		column string
		d      *Dialect
		want   string
	}{
		{"bare name the dialect resolves: postgres", "order_id", &pg, "order_id"},
		{"bare name the dialect resolves: mysql", "OrderId", &my, "OrderId"},
		{"keyword is quoted: postgres", "order", &pg, `"order"`},
		{"keyword is quoted: mysql", "order", &my, "`order`"},
		{"mixed case is quoted on postgres", "OrderId", &pg, `"OrderId"`},
		{"space is quoted", "has space", &pg, `"has space"`},
		{"embedded quote is doubled: postgres", `has"quote`, &pg, `"has""quote"`},
		{"space is quoted: mysql", "has space", &my, "`has space`"},
		{"embedded quote is doubled: mysql", "weird`name", &my, "`weird``name`"},
		{"adds nothing without a dialect", "OrderId", nil, "OrderId"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClauseInsertText(c.column, c.d); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestClausePick(t *testing.T) {
	cases := []struct {
		name string
		opts ClausePickOptions
		want ClausePick
	}{
		{"writes the space the operator will follow when the tail is empty",
			ClausePickOptions{Kind: ClauseWhere, Value: "id = ", Caret: 5, SelectionEnd: 5, InsertText: "name"}, ClausePick{"id = name ", 10}},
		{"replaces the name being typed and keeps the text around it",
			ClausePickOptions{Kind: ClauseWhere, Value: "id = na AND active", Caret: 7, SelectionEnd: 7, InsertText: "name"}, ClausePick{"id = name AND active", 9}},
		{"keys off the caret, not the end of the fragment",
			ClausePickOptions{Kind: ClauseWhere, Value: "status = cus AND enabled = 1", Caret: 12, SelectionEnd: 12, InsertText: "customer_id"}, ClausePick{"status = customer_id AND enabled = 1", 20}},
		{"keeps the quotes the user typed around a quoted identifier",
			ClausePickOptions{Kind: ClauseWhere, Value: `name = "Ord"`, Caret: 11, SelectionEnd: 11, InsertText: "OrderId", IdentQuote: pgQuote}, ClausePick{`name = "OrderId"`, 15}},
		{"writes over a selection instead of appending to it",
			ClausePickOptions{Kind: ClauseWhere, Value: "id = old", Caret: 5, SelectionEnd: 8, InsertText: "name"}, ClausePick{"id = name", 9}},
		{"adds no space when the pick lands inside a literal",
			ClausePickOptions{Kind: ClauseWhere, Value: "name = 'Ali", Caret: 11, SelectionEnd: 11, InsertText: "Alice", IdentQuote: pgQuote}, ClausePick{"name = 'AliAlice", 16}},
		{"replaces the ORDER BY name being typed",
			ClausePickOptions{Kind: ClauseOrderBy, Value: "name na", Caret: 7, SelectionEnd: 7, InsertText: "namespace"}, ClausePick{"name namespace", 14}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClausePickAt(c.opts); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}
