// Package driver talks to Postgres and MySQL: one Conn is one server
// session. Quoting, catalog SQL and anything else that branches on the
// engine lives here and nowhere else.
//
// A Conn is not safe for concurrent use; package pool hands it to one
// caller at a time.
package driver

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"seeker/internal/model"
	"seeker/internal/sqltext"
)

// MaxRows is how many rows a statement keeps by default (recall's
// MAX_ROWS); the rest are read and dropped, and the result says so.
const MaxRows = 20000

type Conn interface {
	Type() model.DBType
	// Database is the one the session was opened in; "" for a MySQL
	// session with no default database.
	Database() string

	Ping(ctx context.Context) (ServerInfo, error)
	ListDatabases(ctx context.Context, filter string) ([]DatabaseInfo, error)
	// ListSchemas is empty for MySQL, where the database is the schema.
	ListSchemas(ctx context.Context, showSystem bool) ([]string, error)
	ListTables(ctx context.Context, schema, filter string) ([]TableInfo, error)
	ListColumns(ctx context.Context, schema, table string) ([]ColumnInfo, error)
	ListIndexes(ctx context.Context, schema, table string) ([]IndexInfo, error)
	ListTriggers(ctx context.Context, schema, table string) ([]TriggerInfo, error)
	ListForeignKeys(ctx context.Context, schema, table string) ([]ForeignKeyInfo, error)
	TableDDL(ctx context.Context, schema, table string) (string, error)
	// CountRows counts the table narrowed by where, raw SQL the user typed.
	CountRows(ctx context.Context, schema, table, where string) (int64, error)

	// Execute runs each statement of script in turn, one Result each, and
	// calls progress after each. SQL errors are in the results, never
	// returned. Cancelling ctx interrupts the running statement and sends
	// none of the rest.
	Execute(ctx context.Context, script string, maxRows int, progress func(Result)) []Result
	// Explain asks for an estimated plan; it never runs the statement.
	Explain(ctx context.Context, sql string) (Plan, error)
	// UpdateRows applies rows in one transaction: all or nothing.
	UpdateRows(ctx context.Context, schema, table string, keyColumns []string, rows []RowChange) (int, error)

	Close() error
	// IsClosed reports a session the server or a cancel ended; it has to be
	// replaced, and its session state (SET, temp tables) is gone.
	IsClosed() bool
}

// Open opens a session in database, "" meaning the server's default.
func Open(ctx context.Context, p model.Profile, password, database string) (Conn, error) {
	switch p.DBType {
	case model.Postgres:
		return openPostgres(ctx, p, password, database)
	case model.MySQL:
		return openMySQL(ctx, p, password, database)
	}
	return nil, fmt.Errorf("driver: unsupported database type %q", p.DBType)
}

// DatabaseScoped reports whether a session is pinned to one database.
// Postgres binds a socket to a database at connect time and cannot reach
// another; MySQL qualifies across databases from one session.
func DatabaseScoped(t model.DBType) bool { return t == model.Postgres }

func QuoteIdent(t model.DBType, name string) string {
	if t == model.MySQL {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Qualify is the quoted table reference. Postgres has no cross-database
// syntax and for MySQL the schema is the database, so the database is
// never part of it.
func Qualify(t model.DBType, schema, table string) string {
	if schema == "" {
		return QuoteIdent(t, table)
	}
	return QuoteIdent(t, schema) + "." + QuoteIdent(t, table)
}

func dialect(t model.DBType) sqltext.Dialect {
	if t == model.MySQL {
		return sqltext.MySQL
	}
	return sqltext.Postgres
}

type ServerInfo struct {
	ProductName     string
	ProductVersion  string
	CurrentDatabase string
}

type DatabaseInfo struct {
	Name    string
	Comment string
}

type TableInfo struct {
	Name    string
	Type    string // "table", "view", "materialized view", …
	Comment string
	// RowCount is the engine's estimate, -1 when it has none.
	RowCount int64
}

type ColumnInfo struct {
	Name       string
	DataType   string
	Nullable   bool
	PrimaryKey bool
	HasDefault bool
	Default    string
	Comment    string
	DDL        string // ALTER TABLE … ADD COLUMN …, ready to copy
}

type IndexInfo struct {
	Name    string
	Columns []string
	Unique  bool
	Primary bool
	Method  string // "btree", "hash", …; "" when unreported
	// Condition is the WHERE of a partial index; MySQL has none.
	Condition string
	DDL       string
}

type ForeignKeyInfo struct {
	Name              string
	Columns           []string
	ReferencedTable   string // schema-qualified only when it is another schema
	ReferencedColumns []string
	OnUpdate          string // "" when the constraint does not say
	OnDelete          string
}

type TriggerInfo struct {
	Name      string
	Timing    string // BEFORE, AFTER, INSTEAD OF
	Event     string // INSERT OR UPDATE, …
	Statement string
}

type Column struct {
	Name string
	// Type is the server's name: format_type's for Postgres (as pg_typeof
	// spells it), the protocol's for MySQL ("TIMESTAMP", "VARCHAR", …).
	Type string
}

// Result is one statement's outcome. A cell is nil for NULL, []byte for
// binary columns and the server's text otherwise. MySQL TIMESTAMP text gets
// the session's UTC offset appended, so it reads as an instant like
// Postgres timestamptz.
type Result struct {
	Index     int
	Statement sqltext.Statement
	Columns   []Column
	Rows      [][]any
	Affected  int64
	Duration  time.Duration
	Truncated bool
	Err       *Error
}

type Plan struct {
	SQL string // what was sent
	Raw string // the server's answer, JSON unless the user wrote their own EXPLAIN
}

// RowChange is one row's edits: the key values as the grid last read
// them, then the new values.
type RowChange struct {
	KeyValues []any
	Changes   []CellChange
}

type CellChange struct {
	Column string
	Value  any
}

// Error is a server error as data.
type Error struct {
	Code    string
	Message string
	Detail  string
	// Offset is the byte offset in the script Execute ran, -1 when the
	// server gave no position (MySQL never does).
	Offset int
}

func (e *Error) Error() string { return e.Message }

// statementRunner is what Execute needs of an engine.
type statementRunner interface {
	run(ctx context.Context, sql string, maxRows int) (cols []Column, rows [][]any, affected int64, truncated bool, err error)
	errorOf(err error, stmt sqltext.Statement) *Error
}

func execute(ctx context.Context, r statementRunner, d sqltext.Dialect, script string, maxRows int, progress func(Result)) []Result {
	var out []Result
	for i, st := range sqltext.Split(script, d) {
		if ctx.Err() != nil {
			break
		}
		start := time.Now()
		res := Result{Index: i, Statement: st}
		cols, rows, affected, truncated, err := r.run(ctx, st.SQL, maxRows)
		res.Duration = time.Since(start)
		if err != nil {
			res.Err = r.errorOf(err, st)
		} else {
			res.Columns, res.Rows, res.Affected, res.Truncated = cols, rows, affected, truncated
		}
		out = append(out, res)
		if progress != nil {
			progress(res)
		}
	}
	return out
}

var explainRe = regexp.MustCompile(`(?i)^\s*explain\b`)

// alreadyExplains: such a statement goes as written. Nesting is a syntax
// error, and a hand-written EXPLAIN ANALYZE is a plan the user asked for.
func alreadyExplains(sql string) bool { return explainRe.MatchString(sql) }

// singleStatement guards SQL the user typed into a fragment (a WHERE)
// against carrying a second statement after a `;`.
func singleStatement(sql string, d sqltext.Dialect) error {
	if n := len(sqltext.Split(sql, d)); n != 1 {
		return &Error{Code: "ERR_STATEMENTS", Message: fmt.Sprintf("expected one statement, got %d", n), Offset: -1}
	}
	return nil
}

// ParseURLParams reads a profile's raw query string into ordered pairs.
// `+` is a space and a value that does not decode is kept as typed, like
// recall's parseUrlParams and the connection dialog's readUrlParam.
func ParseURLParams(raw string) [][2]string {
	raw = strings.TrimLeft(strings.TrimSpace(raw), "?&")
	var out [][2]string
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		out = append(out, [2]string{decodeParam(k), decodeParam(v)})
	}
	return out
}

func decodeParam(s string) string {
	s2 := strings.ReplaceAll(s, "+", " ")
	if d, err := url.PathUnescape(s2); err == nil {
		return d
	}
	return s
}
