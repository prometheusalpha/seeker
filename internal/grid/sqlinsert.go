package grid

import (
	"encoding/hex"
	"regexp"
	"strings"

	"seeker/internal/sqltext"
)

// One INSERT statement for a result-grid row.
//
// What the user copies is the row the database returned, not the row the
// grid paints: a value is quoted as the server's own text, never re-parsed
// from the display formatting, and a hidden column is still inserted,
// because omitting it would silently change which row the statement writes.
//
// Identifiers follow the dialect: backticks for MySQL, double quotes
// otherwise, an embedded quote doubled. String literals double the
// apostrophe, and MySQL additionally escapes the backslash because
// backslash escapes are live there; Postgres leaves backslashes alone
// (`standard_conforming_strings` is on). The column's declared type decides
// whether a numeric-looking string is written bare: text in an `int` column
// reads as a number to the server, the same text in a `text` column stays
// quoted.

type InsertInput struct {
	Dialect                 sqltext.Dialect
	Database, Schema, Table string
	// Columns are the result column names, in render order; ColumnTypes the
	// declared types, parallel.
	Columns, ColumnTypes []string
	// Row is one result row exactly as the driver delivered it.
	Row []any
}

// Type names whose column holds a number, so a string that reads as one is
// written bare. Compared against the leading run of letters, which strips a
// length, precision or attribute (`numeric(10,2)` is `numeric`); MySQL's
// leading `UNSIGNED` is skipped.
var numericTypeWords = map[string]bool{
	"int": true, "integer": true, "bigint": true, "smallint": true, "tinyint": true,
	"mediumint": true, "numeric": true, "decimal": true, "dec": true, "real": true,
	"double": true, "float": true, "money": true, "serial": true, "bigserial": true,
	"fixed": true,
}

var (
	numericLiteral = regexp.MustCompile(`^-?\d+(\.\d+)?([eE][-+]?\d+)?$`)
	leadingLetters = regexp.MustCompile(`^[a-z]+`)
)

func quoteIdent(name string, d sqltext.Dialect) string {
	q := string(d.IdentQuote())
	return q + strings.ReplaceAll(name, q, q+q) + q
}

func quoteString(value string, d sqltext.Dialect) string {
	s := strings.ReplaceAll(value, "'", "''")
	if d == sqltext.MySQL {
		s = strings.ReplaceAll(s, `\`, `\\`)
	}
	return "'" + s + "'"
}

// qualifyTarget qualifies the table per dialect. Postgres uses its schema;
// MySQL has no schema dimension, so its Schema field carries the database
// and is the qualifier, falling back to Database when empty.
func qualifyTarget(in InsertInput) string {
	table := quoteIdent(in.Table, in.Dialect)
	qualifier := in.Schema
	if in.Dialect == sqltext.MySQL && qualifier == "" {
		qualifier = in.Database
	}
	if qualifier == "" {
		return table
	}
	return quoteIdent(qualifier, in.Dialect) + "." + table
}

func literal(value any, columnType string, d sqltext.Dialect) string {
	switch v := value.(type) {
	case nil:
		return "NULL"
	case bool:
		if v {
			return "TRUE"
		}
		return "FALSE"
	case string:
		word := leadingLetters.FindString(strings.TrimPrefix(strings.ToLower(columnType), "unsigned "))
		if numericTypeWords[word] && numericLiteral.MatchString(v) {
			return v
		}
		return quoteString(v, d)
	case []byte:
		// Binary is a hex literal. Recall quoted the base64 text its
		// drivers delivered, which inserts the wrong bytes.
		if d == sqltext.MySQL {
			return "X'" + hex.EncodeToString(v) + "'"
		}
		return `'\x` + hex.EncodeToString(v) + `'`
	}
	if s, ok := numberText(value); ok {
		return s
	}
	if s, ok := jsonText(value); ok {
		return quoteString(s, d)
	}
	return quoteString(FormatCell(value, ""), d)
}

// BuildInsert is one `INSERT INTO ... VALUES (...)` for a single row,
// `;`-terminated.
func BuildInsert(in InsertInput) string {
	cols := make([]string, len(in.Columns))
	vals := make([]string, len(in.Columns))
	for i, name := range in.Columns {
		cols[i] = quoteIdent(name, in.Dialect)
		var v any
		if i < len(in.Row) {
			v = in.Row[i]
		}
		var typ string
		if i < len(in.ColumnTypes) {
			typ = in.ColumnTypes[i]
		}
		vals[i] = literal(v, typ, in.Dialect)
	}
	return "INSERT INTO " + qualifyTarget(in) + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(vals, ", ") + ");"
}
