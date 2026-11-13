package grid

import (
	"math"
	"reflect"
	"testing"

	"seeker/internal/sqltext"
)

func TestSumCells(t *testing.T) {
	cases := []struct {
		name  string
		cells []SumCell
		want  CellSumResult
	}{
		{"numbers and numeric text", []SumCell{{1, ""}, {"2.5", "numeric"}, {int64(3), ""}, {"1e2", ""}}, CellSumResult{106.5, 4, 0}},
		{"NULL and text are skipped", []SumCell{{nil, ""}, {"abc", ""}, {"", ""}, {"  ", ""}, {"7", ""}}, CellSumResult{7, 1, 4}},
		{"NaN and Infinity never count, as text or number", []SumCell{{"NaN", "numeric"}, {"Infinity", ""}, {"inf", ""}, {math.NaN(), ""}, {math.Inf(1), ""}}, CellSumResult{0, 0, 5}},
		{"surrounding spaces are fine", []SumCell{{" 4 ", ""}}, CellSumResult{4, 1, 0}},
		{"money strips its currency decoration", []SumCell{{"$5.00", "money"}, {"€2.50", "MONEY"}}, CellSumResult{7.5, 2, 0}},
		{"a currency sign outside money column is not a number", []SumCell{{"$5.00", "text"}}, CellSumResult{0, 0, 1}},
		{"hex and underscore forms are not numbers", []SumCell{{"0x10", ""}, {"1_000", ""}}, CellSumResult{0, 0, 2}},
		{"booleans and bytes are skipped", []SumCell{{true, ""}, {[]byte("5"), ""}}, CellSumResult{0, 0, 2}},
		{"empty selection", nil, CellSumResult{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SumCells(c.cells); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestBuildInsert(t *testing.T) {
	cases := []struct {
		name string
		in   InsertInput
		want string
	}{
		{"postgres quotes with double quotes and qualifies by schema",
			InsertInput{Dialect: sqltext.Postgres, Database: "db", Schema: "public", Table: "us\"ers", Columns: []string{"id", "na\"me"}, ColumnTypes: []string{"integer", "text"}, Row: []any{"1", "it's"}},
			`INSERT INTO "public"."us""ers" ("id", "na""me") VALUES (1, 'it''s');`},
		{"postgres leaves backslashes alone",
			InsertInput{Dialect: sqltext.Postgres, Schema: "s", Table: "t", Columns: []string{"a"}, ColumnTypes: []string{"text"}, Row: []any{`a\b`}},
			`INSERT INTO "s"."t" ("a") VALUES ('a\b');`},
		{"mysql uses backticks and escapes the backslash",
			InsertInput{Dialect: sqltext.MySQL, Schema: "shop", Table: "t`x", Columns: []string{"a`b"}, ColumnTypes: []string{"VARCHAR"}, Row: []any{`a\'b`}},
			"INSERT INTO `shop`.`t``x` (`a``b`) VALUES ('a\\\\''b');"},
		{"mysql falls back to the database when schema is empty",
			InsertInput{Dialect: sqltext.MySQL, Database: "shop", Table: "t", Columns: []string{"a"}, ColumnTypes: []string{"INT"}, Row: []any{"5"}},
			"INSERT INTO `shop`.`t` (`a`) VALUES (5);"},
		{"postgres without a schema is unqualified",
			InsertInput{Dialect: sqltext.Postgres, Table: "t", Columns: []string{"a"}, ColumnTypes: []string{"text"}, Row: []any{nil}},
			`INSERT INTO "t" ("a") VALUES (NULL);`},
		{"the declared type decides whether numeric text is bare",
			InsertInput{Dialect: sqltext.Postgres, Table: "t", Columns: []string{"a", "b", "c", "d", "e"}, ColumnTypes: []string{"numeric(10,2)", "text", "integer", "bigint", "UNSIGNED INT"}, Row: []any{"1.50", "1.50", "abc", "-12", "7"}},
			`INSERT INTO "t" ("a", "b", "c", "d", "e") VALUES (1.50, '1.50', 'abc', -12, 7);`},
		{"booleans, numbers and nested values",
			InsertInput{Dialect: sqltext.Postgres, Table: "t", Columns: []string{"a", "b", "c"}, ColumnTypes: []string{"", "", ""}, Row: []any{true, 2.5, map[string]any{"k": "it's"}}},
			`INSERT INTO "t" ("a", "b", "c") VALUES (TRUE, 2.5, '{"k":"it''s"}');`},
		{"binary is a hex literal per dialect",
			InsertInput{Dialect: sqltext.MySQL, Table: "t", Columns: []string{"a"}, ColumnTypes: []string{"BLOB"}, Row: []any{[]byte{0xde, 0xad}}},
			"INSERT INTO `t` (`a`) VALUES (X'dead');"},
		{"binary on postgres",
			InsertInput{Dialect: sqltext.Postgres, Table: "t", Columns: []string{"a"}, ColumnTypes: []string{"bytea"}, Row: []any{[]byte{0xde, 0xad}}},
			`INSERT INTO "t" ("a") VALUES ('\xdead');`},
		{"a short row reads as NULL",
			InsertInput{Dialect: sqltext.Postgres, Table: "t", Columns: []string{"a", "b"}, Row: []any{"x"}},
			`INSERT INTO "t" ("a", "b") VALUES ('x', NULL);`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BuildInsert(c.in); got != c.want {
				t.Errorf("got  %s\nwant %s", got, c.want)
			}
		})
	}
}

func TestResolveKind(t *testing.T) {
	cases := []struct {
		typ  string
		want Kind
	}{
		{"", KindUnknown},
		{"integer", KindInteger}, {"int4", KindInteger}, {"UNSIGNED INT", KindInteger}, {"BIGINT", KindInteger}, {"TINYINT", KindInteger},
		{"numeric(20,6)", KindNumeric}, {"DECIMAL", KindNumeric}, {"double precision", KindNumeric},
		{"character varying", KindString}, {"VARCHAR", KindString}, {"TEXT", KindString}, {"citext", KindString},
		{"boolean", KindBoolean},
		{"timestamp with time zone", KindTemporal}, {"TIMESTAMP", KindTemporal}, {"DATETIME", KindTemporal},
		{"jsonb", KindStructured}, {"JSON", KindStructured},
		{"integer[]", KindStructured}, {"_int4", KindStructured}, {"array(text)", KindStructured}, {"text[][]", KindStructured},
		{"uuid", KindIdentifier},
		{"bytea", KindBinary}, {"BLOB", KindBinary}, {"VARBINARY", KindBinary},
		{"geometry", KindSpatial},
		{"inet", KindUnknown}, {"_", KindUnknown}, {"constructor", KindUnknown}, {"  Integer  ", KindInteger},
	}
	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			if got := ResolveKind(c.typ); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestColumnWidth(t *testing.T) {
	t.Run("estimate counts wide code points double", func(t *testing.T) {
		if got := EstimateTextWidth("ab日", 8); got != 32 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("width is the wider of header and values plus padding", func(t *testing.T) {
		got := AutoColumnWidth(ColumnWidthInput{Header: "id", Values: []string{"1234567890"}, CharWidth: 8})
		if got != 10*8+40 {
			t.Errorf("got %d", got)
		}
	})
	t.Run("floors and caps", func(t *testing.T) {
		if got := AutoColumnWidth(ColumnWidthInput{CharWidth: 8, Min: 100}); got != 100 {
			t.Errorf("floor: got %d", got)
		}
		if got := AutoColumnWidth(ColumnWidthInput{Header: string(make([]byte, 500)), CharWidth: 8}); got != GridMaxAutoWidth {
			t.Errorf("cap: got %d", got)
		}
	})
	t.Run("a long value is cut at 60 runes before measuring", func(t *testing.T) {
		long := make([]rune, 10000)
		for i := range long {
			long[i] = 'x'
		}
		if got := AutoColumnWidth(ColumnWidthInput{Values: []string{string(long)}, CharWidth: 8}); got != 60*8+40 {
			t.Errorf("got %d", got)
		}
	})
	t.Run("rounds halves up and replaces a bad char width", func(t *testing.T) {
		if got := AutoColumnWidth(ColumnWidthInput{Header: "abc", CharWidth: 8.5}); got != 66 {
			t.Errorf("got %d", got)
		}
		if got := AutoColumnWidth(ColumnWidthInput{Header: "abc", CharWidth: -1}); got != 3*8+40 {
			t.Errorf("got %d", got)
		}
	})
	t.Run("samples head and tail", func(t *testing.T) {
		rows := make([][]any, 25)
		for i := range rows {
			rows[i] = []any{i}
		}
		got := SampleColumnValues(rows, 0, "", 4)
		if want := []string{"0", "1", "23", "24"}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v", got)
		}
		if got := SampleColumnValues(rows[:3], 0, "", 4); len(got) != 3 {
			t.Errorf("few rows: got %v", got)
		}
	})
	t.Run("a short row reads as empty and instants are measured as shown", func(t *testing.T) {
		rows := [][]any{{"2026-01-01 06:15:00+00"}, {}}
		got := SampleColumnValues(rows, 0, "timestamptz", 10)
		if want := []string{"2026-01-01 13:15:00+07", ""}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v", got)
		}
	})
	t.Run("char width from a measure function", func(t *testing.T) {
		if got := CharWidth(func(s string) float64 { return float64(len(s)) * 7.5 }); got != 7.5 {
			t.Errorf("got %v", got)
		}
		if CharWidth(nil) != GridCharWidth || CharWidth(func(string) float64 { return 0 }) != GridCharWidth {
			t.Errorf("fallback")
		}
	})
}

func TestEditsByRow(t *testing.T) {
	got := EditsByRow([]PendingEdit{{3, 2, "x"}, {1, 5, "a"}, {3, 0, "y"}, {1, 1, "b"}})
	want := []RowEdits{
		{1, []PendingEdit{{1, 1, "b"}, {1, 5, "a"}}},
		{3, []PendingEdit{{3, 0, "y"}, {3, 2, "x"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if EditKey(3, 2) != "3:2" {
		t.Errorf("key")
	}
}
