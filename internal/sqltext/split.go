// Package sqltext reads SQL text as the editor and the drivers need it:
// where statements start and end, and which one the caret is in.
package sqltext

import "strings"

type Dialect int

const (
	Postgres Dialect = iota
	MySQL
)

// Statement is a top-level statement of a script. Offsets are bytes, as the
// editor's.
type Statement struct {
	// Start is the first byte of its code, past the comments and blanks
	// before it; End is past its last byte, its `;` included when it has one.
	// The editor outlines and runs this range.
	Start, End int
	// SQL is what the server receives: Start to End without the `;`.
	SQL string
}

// Split splits sql into its top-level statements. A `;` ends a statement
// only outside comments, strings, quoted identifiers and, for Postgres,
// dollar-quoted bodies; MySQL adds `#` comments and backslash escapes in
// strings, as its server reads them by default. Fragments without code,
// as a comment after the last statement, are not statements: MySQL fails
// on an empty query.
//
// Recall had two splitters, the backend's and the editor's, which
// disagreed on those MySQL rules; this is the backend's scanning with the
// editor's ranges.
func Split(sql string, d Dialect) []Statement {
	var out []Statement
	start := -1 // first byte of code of the statement being read
	last := -1  // past its last byte of code
	code := func(from, to int) {
		if start < 0 {
			start = from
		}
		last = to
	}
	n := len(sql)
	for i := 0; i < n; {
		c := sql[i]
		switch {
		case c == '-' && i+1 < n && sql[i+1] == '-', c == '#' && d == MySQL:
			for i < n && sql[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && sql[i+1] == '*':
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				i = n
			} else {
				i += 2 + end + 2
			}
		case c == '\'' || c == '"':
			end := skipQuoted(sql, i, c, d == MySQL)
			code(i, end)
			i = end
		case c == '`':
			end := skipQuoted(sql, i, c, false)
			code(i, end)
			i = end
		case c == '$' && d == Postgres && dollarTag(sql, i) != "":
			tag := dollarTag(sql, i)
			end := n
			if j := strings.Index(sql[i+len(tag):], tag); j >= 0 {
				end = i + len(tag) + j + len(tag)
			}
			code(i, end)
			i = end
		case c == ';':
			if start >= 0 {
				out = append(out, Statement{Start: start, End: i + 1, SQL: sql[start:last]})
			}
			start, last = -1, -1
			i++
		case isSpace(c):
			i++
		default:
			code(i, i+1)
			i++
		}
	}
	if start >= 0 {
		out = append(out, Statement{Start: start, End: last, SQL: sql[start:last]})
	}
	return out
}

// At returns the statement the caret at offset pos is in: the last one
// starting at or before it and ending at or after it, so a caret right
// after a `;` that the next statement starts at belongs to the next.
func At(sql string, d Dialect, pos int) (Statement, bool) {
	var found Statement
	ok := false
	if pos < 0 || pos > len(sql) {
		return found, false
	}
	for _, s := range Split(sql, d) {
		if pos < s.Start {
			break
		}
		if pos <= s.End {
			found, ok = s, true
		}
	}
	return found, ok
}

// skipQuoted returns the offset past the quoted run opening at sql[at],
// or len(sql) when it never closes. A doubled quote is an escaped one;
// with backslashes, so is one after a backslash.
func skipQuoted(sql string, at int, quote byte, backslashes bool) int {
	for i := at + 1; i < len(sql); i++ {
		switch c := sql[i]; {
		case backslashes && c == '\\':
			i++
		case c == quote:
			if i+1 < len(sql) && sql[i+1] == quote {
				i++
				continue
			}
			return i + 1
		}
	}
	return len(sql)
}

// dollarTag returns the opener of a dollar-quoted body at sql[at], as
// `$$` or `$body$`, or "" where the `$` opens none, as in `$1`.
func dollarTag(sql string, at int) string {
	i := at + 1
	for i < len(sql) && isTagByte(sql[i]) {
		i++
	}
	if i < len(sql) && sql[i] == '$' && !(i > at+1 && sql[at+1] >= '0' && sql[at+1] <= '9') {
		return sql[at : i+1]
	}
	return ""
}

func isTagByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c >= 0x80
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
