package driver

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"

	"seeker/internal/model"
	"seeker/internal/sqltext"
)

type pgConn struct {
	conn     *pgx.Conn
	database string
	// typeNames caches format_type per OID: a user enum's OID is only
	// meaningful in this database, so the cache lives with the session.
	typeNames map[uint32]string
}

// cancelDeadline is how long a cancelled statement may take to stop
// before the socket is cut. The cancel request keeps the session; cutting
// loses it, so it is the fallback for a server that does not answer.
const cancelDeadline = 5 * time.Second

func openPostgres(ctx context.Context, p model.Profile, password, database string) (Conn, error) {
	cfg, err := pgx.ParseConfig(postgresURL(p, database))
	if err != nil {
		return nil, &Error{Code: "ERR_CONFIG", Message: err.Error(), Offset: -1}
	}
	cfg.Password = password
	// pgconn's default cuts the socket on cancel; a cancel request stops
	// the statement and leaves the session (SET, temp tables) alive.
	cfg.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: c, DeadlineDelay: cancelDeadline}
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, pgError(err, sqltext.Statement{})
	}
	return &pgConn{conn: conn, database: database, typeNames: map[uint32]string{}}, nil
}

// postgresURL maps the profile onto a libpq URL. The SSL flag is
// sslmode=require unless urlParams names a mode; every other parameter
// goes through, so pgx reads the libpq ones and sends the rest as runtime
// parameters (application_name, search_path, …).
func postgresURL(p model.Profile, database string) string {
	q := url.Values{}
	for _, kv := range ParseURLParams(p.URLParams) {
		q.Set(kv[0], kv[1])
	}
	if q.Get("sslmode") == "" {
		if p.SSL {
			q.Set("sslmode", "require")
		} else {
			q.Set("sslmode", "disable")
		}
	}
	if q.Get("connect_timeout") == "" {
		secs := p.ConnectTimeoutSecs
		if secs <= 0 {
			secs = 30
		}
		q.Set("connect_timeout", strconv.Itoa(secs))
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.User(p.Username),
		Host:     hostPort(p.Host, p.Port),
		Path:     "/" + database,
		RawQuery: q.Encode(),
	}
	return u.String()
}

func hostPort(host string, port int) string {
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return host + ":" + strconv.Itoa(port)
}

func (c *pgConn) Type() model.DBType { return model.Postgres }
func (c *pgConn) Database() string   { return c.database }
func (c *pgConn) Close() error       { return c.conn.Close(context.Background()) }
func (c *pgConn) IsClosed() bool     { return c.conn.IsClosed() }

var pgVersionRe = regexp.MustCompile(`(?i)^\s*PostgreSQL\s+([0-9][0-9A-Za-z.\-]*)`)

func (c *pgConn) Ping(ctx context.Context) (ServerInfo, error) {
	var version, db string
	if err := c.conn.QueryRow(ctx, "SELECT version(), current_database()").Scan(&version, &db); err != nil {
		return ServerInfo{}, pgError(err, sqltext.Statement{})
	}
	info := ServerInfo{ProductName: "Postgres-compatible server", ProductVersion: strings.TrimSpace(version), CurrentDatabase: db}
	if m := pgVersionRe.FindStringSubmatch(version); m != nil {
		info.ProductName, info.ProductVersion = "PostgreSQL", m[1]
	}
	return info, nil
}

// query runs catalog SQL with parameters and hands each row to scan.
func (c *pgConn) query(ctx context.Context, sql string, args []any, scan func(pgx.Rows) error) error {
	rows, err := c.conn.Query(ctx, sql, args...)
	if err != nil {
		return pgError(err, sqltext.Statement{})
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return pgError(err, sqltext.Statement{})
		}
	}
	if err := rows.Err(); err != nil {
		return pgError(err, sqltext.Statement{})
	}
	return nil
}

func (c *pgConn) ListDatabases(ctx context.Context, filter string) ([]DatabaseInfo, error) {
	var out []DatabaseInfo
	err := c.query(ctx, "SELECT datname, COALESCE(shobj_description(oid, 'pg_database'), '') "+
		"FROM pg_database WHERE datallowconn AND NOT datistemplate "+
		"AND ($1 = '' OR datname ILIKE $1) ORDER BY datname",
		[]any{"%" + filter + "%"}, func(r pgx.Rows) error {
			var d DatabaseInfo
			err := r.Scan(&d.Name, &d.Comment)
			out = append(out, d)
			return err
		})
	return out, err
}

func (c *pgConn) ListSchemas(ctx context.Context, showSystem bool) ([]string, error) {
	// Temp namespaces are per-session scratch that vanish with their
	// connection, so they stay hidden even when system schemas are shown.
	sql := "SELECT schema_name FROM information_schema.schemata " +
		"WHERE schema_name NOT LIKE 'pg_%' AND schema_name <> 'information_schema' ORDER BY 1"
	if showSystem {
		sql = "SELECT schema_name FROM information_schema.schemata " +
			"WHERE schema_name NOT LIKE 'pg_temp_%' AND schema_name NOT LIKE 'pg_toast_temp_%' ORDER BY 1"
	}
	var out []string
	err := c.query(ctx, sql, nil, func(r pgx.Rows) error {
		var s string
		err := r.Scan(&s)
		out = append(out, s)
		return err
	})
	return out, err
}

var pgTableTypes = map[string]string{"r": "table", "p": "partitioned table", "v": "view", "m": "materialized view", "f": "foreign table"}

func (c *pgConn) ListTables(ctx context.Context, schema, filter string) ([]TableInfo, error) {
	// A partition is listed under nothing but its partitioned parent.
	// pg_inherits rather than relispartition keeps 9.x working; plain
	// INHERITS children have a non-partitioned parent and stay listed.
	var out []TableInfo
	err := c.query(ctx, "SELECT c.relname, c.relkind::text, COALESCE(obj_description(c.oid, 'pg_class'), ''), c.reltuples::bigint "+
		"FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
		"WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'v', 'm', 'f') "+
		"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_inherits pi JOIN pg_catalog.pg_class pp ON pp.oid = pi.inhparent "+
		"  WHERE pi.inhrelid = c.oid AND pp.relkind = 'p') "+
		"AND ($2 = '' OR c.relname ILIKE $2) ORDER BY c.relname",
		[]any{schema, "%" + filter + "%"}, func(r pgx.Rows) error {
			var t TableInfo
			var kind string
			var rows *int64
			err := r.Scan(&t.Name, &kind, &t.Comment, &rows)
			t.Type = pgTableTypes[kind]
			if t.Type == "" {
				t.Type = kind
			}
			// reltuples is -1 until the relation is analyzed: unknown, not empty.
			t.RowCount = -1
			if rows != nil && *rows >= 0 {
				t.RowCount = *rows
			}
			out = append(out, t)
			return err
		})
	return out, err
}

type pgColumnRow struct {
	name, dataType, identity, generated string
	def, comment                        *string
	notNull, pk                         bool
}

func (c *pgConn) ListColumns(ctx context.Context, schema, table string) ([]ColumnInfo, error) {
	var out []ColumnInfo
	err := c.query(ctx, "SELECT a.attname, pg_catalog.format_type(a.atttypid, a.atttypmod), a.attnotnull, "+
		"a.attidentity::text, a.attgenerated::text, pg_catalog.pg_get_expr(d.adbin, d.adrelid), "+
		"pg_catalog.col_description(a.attrelid, a.attnum), "+
		"EXISTS (SELECT 1 FROM pg_catalog.pg_index i WHERE i.indrelid = a.attrelid AND i.indisprimary AND a.attnum = ANY(i.indkey)) "+
		"FROM pg_catalog.pg_attribute a "+
		"JOIN pg_catalog.pg_class c ON c.oid = a.attrelid "+
		"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
		"LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum "+
		"WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum",
		[]any{schema, table}, func(r pgx.Rows) error {
			var col pgColumnRow
			if err := r.Scan(&col.name, &col.dataType, &col.notNull, &col.identity, &col.generated, &col.def, &col.comment, &col.pk); err != nil {
				return err
			}
			info := ColumnInfo{Name: col.name, DataType: col.dataType, Nullable: !col.notNull, PrimaryKey: col.pk}
			if col.def != nil && *col.def != "" {
				info.HasDefault, info.Default = true, *col.def
			}
			if col.comment != nil {
				info.Comment = *col.comment
			}
			stmts := []string{"ALTER TABLE " + Qualify(model.Postgres, schema, table) + " ADD COLUMN " + pgColumnDefinition(col)}
			stmts = append(stmts, pgCommentOn(col.name, col.comment, schema, table)...)
			info.DDL = strings.Join(stmts, ";\n") + ";"
			out = append(out, info)
			return nil
		})
	return out, err
}

func (c *pgConn) ListIndexes(ctx context.Context, schema, table string) ([]IndexInfo, error) {
	// One row per key column, in (index, position) order. indkey is an
	// int2vector, not an array on every server version, so it is split
	// through its text form.
	var out []IndexInfo
	err := c.query(ctx, "SELECT ic.relname, i.indisunique, i.indisprimary, am.amname, "+
		"pg_catalog.pg_get_indexdef(i.indexrelid), COALESCE(pg_catalog.pg_get_expr(i.indpred, i.indrelid), ''), "+
		"COALESCE(a.attname, pg_catalog.pg_get_indexdef(i.indexrelid, k.position::int, true)) "+
		"FROM pg_catalog.pg_index i "+
		"JOIN pg_catalog.pg_class ic ON ic.oid = i.indexrelid "+
		"JOIN pg_catalog.pg_class tc ON tc.oid = i.indrelid "+
		"JOIN pg_catalog.pg_namespace n ON n.oid = tc.relnamespace "+
		"JOIN pg_catalog.pg_am am ON am.oid = ic.relam "+
		"CROSS JOIN LATERAL unnest(string_to_array(i.indkey::text, ' ')::int2[]) WITH ORDINALITY AS k(attnum, position) "+
		"LEFT JOIN pg_catalog.pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum "+
		"WHERE n.nspname = $1 AND tc.relname = $2 ORDER BY ic.relname, k.position",
		[]any{schema, table}, func(r pgx.Rows) error {
			var ix IndexInfo
			var col *string
			if err := r.Scan(&ix.Name, &ix.Unique, &ix.Primary, &ix.Method, &ix.DDL, &ix.Condition, &col); err != nil {
				return err
			}
			if len(out) == 0 || out[len(out)-1].Name != ix.Name {
				out = append(out, ix)
			}
			if col != nil {
				last := &out[len(out)-1]
				last.Columns = append(last.Columns, *col)
			}
			return nil
		})
	return out, err
}

func (c *pgConn) ListTriggers(ctx context.Context, schema, table string) ([]TriggerInfo, error) {
	// tgtype is a bitmask (TRIGGER_TYPE_* in the server headers): 2 BEFORE,
	// 64 INSTEAD, 4/8/16/32 the events.
	var out []TriggerInfo
	err := c.query(ctx, "SELECT t.tgname, COALESCE(pg_catalog.pg_get_triggerdef(t.oid, true), ''), t.tgtype::int "+
		"FROM pg_catalog.pg_trigger t "+
		"JOIN pg_catalog.pg_class c ON c.oid = t.tgrelid "+
		"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
		"WHERE n.nspname = $1 AND c.relname = $2 AND NOT t.tgisinternal ORDER BY t.tgname",
		[]any{schema, table}, func(r pgx.Rows) error {
			var t TriggerInfo
			var bits int32
			if err := r.Scan(&t.Name, &t.Statement, &bits); err != nil {
				return err
			}
			var events []string
			for _, e := range []struct {
				bit  int32
				name string
			}{{4, "INSERT"}, {8, "DELETE"}, {16, "UPDATE"}, {32, "TRUNCATE"}} {
				if bits&e.bit != 0 {
					events = append(events, e.name)
				}
			}
			switch {
			case bits&64 != 0:
				t.Timing = "INSTEAD OF"
			case bits&2 != 0:
				t.Timing = "BEFORE"
			case len(events) > 0:
				t.Timing = "AFTER"
			}
			// Postgres spells several events `INSERT OR UPDATE`.
			t.Event = strings.Join(events, " OR ")
			out = append(out, t)
			return nil
		})
	return out, err
}

// pgReferentialActions maps confupdtype/confdeltype; a blank one means
// the clause was absent and stays "".
var pgReferentialActions = map[string]string{"a": "NO ACTION", "r": "RESTRICT", "c": "CASCADE", "n": "SET NULL", "d": "SET DEFAULT"}

func (c *pgConn) ListForeignKeys(ctx context.Context, schema, table string) ([]ForeignKeyInfo, error) {
	// conkey and confkey keep their ordinality and only equal positions are
	// joined; a plain cross join would mis-pair a multi-column key.
	var out []ForeignKeyInfo
	err := c.query(ctx, "SELECT con.conname, rn.nspname, rc.relname, con.confupdtype::text, con.confdeltype::text, a.attname, ra.attname "+
		"FROM pg_catalog.pg_constraint con "+
		"JOIN pg_catalog.pg_class c ON c.oid = con.conrelid "+
		"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
		"JOIN pg_catalog.pg_class rc ON rc.oid = con.confrelid "+
		"JOIN pg_catalog.pg_namespace rn ON rn.oid = rc.relnamespace "+
		"CROSS JOIN LATERAL unnest(con.conkey) WITH ORDINALITY AS k(attnum, position) "+
		"CROSS JOIN LATERAL unnest(con.confkey) WITH ORDINALITY AS rk(ref_attnum, ref_position) "+
		"JOIN pg_catalog.pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum "+
		"JOIN pg_catalog.pg_attribute ra ON ra.attrelid = con.confrelid AND ra.attnum = rk.ref_attnum "+
		"WHERE con.contype = 'f' AND n.nspname = $1 AND c.relname = $2 AND k.position = rk.ref_position "+
		"ORDER BY con.conname, k.position",
		[]any{schema, table}, func(r pgx.Rows) error {
			var name, refSchema, refTable, onUpdate, onDelete, col, refCol string
			if err := r.Scan(&name, &refSchema, &refTable, &onUpdate, &onDelete, &col, &refCol); err != nil {
				return err
			}
			if len(out) == 0 || out[len(out)-1].Name != name {
				ref := refTable
				if refSchema != schema {
					ref = refSchema + "." + refTable
				}
				out = append(out, ForeignKeyInfo{Name: name, ReferencedTable: ref,
					OnUpdate: pgReferentialActions[onUpdate], OnDelete: pgReferentialActions[onDelete]})
			}
			last := &out[len(out)-1]
			last.Columns = append(last.Columns, col)
			last.ReferencedColumns = append(last.ReferencedColumns, refCol)
			return nil
		})
	return out, err
}

func (c *pgConn) TableDDL(ctx context.Context, schema, table string) (string, error) {
	var oid uint32
	var name, kind string
	var comment *string
	err := c.conn.QueryRow(ctx, "SELECT c.oid, c.relname, c.relkind::text, obj_description(c.oid, 'pg_class') "+
		"FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
		"WHERE n.nspname = $1 AND c.relname = $2", schema, table).Scan(&oid, &name, &kind, &comment)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", &Error{Code: "ERR_NOT_FOUND", Message: fmt.Sprintf("Table %q.%q does not exist.", schema, table), Offset: -1}
	}
	if err != nil {
		return "", pgError(err, sqltext.Statement{})
	}
	target := Qualify(model.Postgres, schema, name)
	var out []string

	if kind == "v" || kind == "m" {
		var def string
		if err := c.conn.QueryRow(ctx, "SELECT pg_catalog.pg_get_viewdef($1::oid, true)", oid).Scan(&def); err != nil {
			return "", pgError(err, sqltext.Statement{})
		}
		keyword := "VIEW"
		if kind == "m" {
			keyword = "MATERIALIZED VIEW"
		}
		// pg_get_viewdef ends with its own `;`.
		def = strings.TrimRight(strings.TrimSpace(def), ";")
		out = append(out, "CREATE "+keyword+" "+target+" AS\n"+def)
		out = append(out, pgCommentOn(name, comment, "", "")...)
		return strings.Join(out, ";\n\n") + ";", nil
	}

	var cols []pgColumnRow
	err = c.query(ctx, "SELECT a.attname, pg_catalog.format_type(a.atttypid, a.atttypmod), a.attnotnull, "+
		"a.attidentity::text, a.attgenerated::text, pg_catalog.pg_get_expr(d.adbin, d.adrelid), "+
		"pg_catalog.col_description(a.attrelid, a.attnum) "+
		"FROM pg_catalog.pg_attribute a LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum "+
		"WHERE a.attrelid = $1::oid AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum",
		[]any{oid}, func(r pgx.Rows) error {
			var col pgColumnRow
			err := r.Scan(&col.name, &col.dataType, &col.notNull, &col.identity, &col.generated, &col.def, &col.comment)
			cols = append(cols, col)
			return err
		})
	if err != nil {
		return "", err
	}
	var body []string
	for _, col := range cols {
		body = append(body, pgColumnDefinition(col))
	}
	err = c.query(ctx, "SELECT con.conname, pg_catalog.pg_get_constraintdef(con.oid) FROM pg_catalog.pg_constraint con "+
		"WHERE con.conrelid = $1::oid AND con.contype IN ('p', 'u', 'f', 'c') ORDER BY con.contype, con.conname",
		[]any{oid}, func(r pgx.Rows) error {
			var cname, def string
			err := r.Scan(&cname, &def)
			body = append(body, "CONSTRAINT "+QuoteIdent(model.Postgres, cname)+" "+def)
			return err
		})
	if err != nil {
		return "", err
	}
	closing := ")"
	if kind == "p" {
		var key string
		if err := c.conn.QueryRow(ctx, "SELECT pg_catalog.pg_get_partkeydef($1::oid)", oid).Scan(&key); err != nil {
			return "", pgError(err, sqltext.Statement{})
		}
		closing = ") " + key
	}
	if kind == "f" {
		out = append(out, fmt.Sprintf("-- Foreign table %s.%s; its server definition lives on the remote database.", schema, name))
	}
	out = append(out, "CREATE TABLE "+target+" (\n  "+strings.Join(body, ",\n  ")+"\n"+closing)
	out = append(out, pgCommentOn(name, comment, "", "")...)
	for _, col := range cols {
		out = append(out, pgCommentOn(col.name, col.comment, schema, name)...)
	}
	return strings.Join(out, ";\n\n") + ";", nil
}

// pgColumnDefinition is shared by TableDDL and a column's own DDL so the
// two never disagree.
func pgColumnDefinition(col pgColumnRow) string {
	parts := []string{QuoteIdent(model.Postgres, col.name) + " " + col.dataType}
	switch {
	case col.generated == "s":
		parts = append(parts, "GENERATED ALWAYS AS ("+deref(col.def)+") STORED")
	case col.identity == "a":
		parts = append(parts, "GENERATED ALWAYS AS IDENTITY")
	case col.identity == "d":
		parts = append(parts, "GENERATED BY DEFAULT AS IDENTITY")
	case col.def != nil && *col.def != "":
		parts = append(parts, "DEFAULT "+*col.def)
	}
	if col.notNull {
		parts = append(parts, "NOT NULL")
	}
	return strings.Join(parts, " ")
}

// pgCommentOn is a COMMENT statement, since Postgres has no inline form:
// on the column when table is set, on the table otherwise. The table's is
// unqualified, as recall wrote it.
func pgCommentOn(name string, comment *string, schema, table string) []string {
	if comment == nil || *comment == "" {
		return nil
	}
	literal := "'" + strings.ReplaceAll(*comment, "'", "''") + "'"
	if table != "" {
		return []string{"COMMENT ON COLUMN " + Qualify(model.Postgres, schema, table) + "." + QuoteIdent(model.Postgres, name) + " IS " + literal}
	}
	return []string{"COMMENT ON TABLE " + QuoteIdent(model.Postgres, name) + " IS " + literal}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (c *pgConn) CountRows(ctx context.Context, schema, table, where string) (int64, error) {
	sql := "SELECT COUNT(*) FROM " + Qualify(model.Postgres, schema, table)
	if w := strings.TrimSpace(where); w != "" {
		sql += " WHERE " + w
	}
	if err := singleStatement(sql, sqltext.Postgres); err != nil {
		return 0, err
	}
	var n int64
	if err := c.conn.QueryRow(ctx, sql, pgx.QueryExecModeSimpleProtocol).Scan(&n); err != nil {
		return 0, pgError(err, sqltext.Statement{})
	}
	return n, nil
}

func (c *pgConn) Execute(ctx context.Context, script string, maxRows int, progress func(Result)) []Result {
	return execute(ctx, c, sqltext.Postgres, script, maxRows, progress)
}

// byteaOID is the one type whose text form is an encoding, not the value.
const byteaOID = 17

// run sends the user's SQL over the simple protocol: no statement cache
// to go stale after DDL (`cached plan must not change result type`), and
// every value comes back as the server's text, microseconds and odd types
// (enum, range, domain) included.
func (c *pgConn) run(ctx context.Context, sql string, maxRows int) ([]Column, [][]any, int64, bool, error) {
	mrr := c.conn.PgConn().Exec(ctx, sql)
	var (
		cols      []Column
		oids      []uint32
		rows      [][]any
		affected  int64
		truncated bool
	)
	for mrr.NextResult() {
		rr := mrr.ResultReader()
		fields := rr.FieldDescriptions()
		// The split hands one statement at a time, but a server-side
		// function or a rule can still answer twice; the last answer
		// with columns wins, as a psql user sees it.
		if len(fields) > 0 {
			cols, oids, rows, truncated = make([]Column, len(fields)), make([]uint32, len(fields)), nil, false
			for i, f := range fields {
				cols[i].Name, oids[i] = f.Name, f.DataTypeOID
			}
		}
		for rr.NextRow() {
			if maxRows > 0 && len(rows) >= maxRows {
				// The rest is still read off the socket by Close.
				truncated = true
				continue
			}
			vals := rr.Values()
			row := make([]any, len(vals))
			for i, v := range vals {
				row[i] = pgCell(v, oids[i])
			}
			rows = append(rows, row)
		}
		tag, err := rr.Close()
		if err != nil {
			mrr.Close()
			return nil, nil, 0, false, err
		}
		affected = tag.RowsAffected()
	}
	if err := mrr.Close(); err != nil {
		return nil, nil, 0, false, err
	}
	if cols != nil {
		c.nameTypes(ctx, cols, oids)
	}
	return cols, rows, affected, truncated, nil
}

func pgCell(v []byte, oid uint32) any {
	if v == nil {
		return nil
	}
	if oid == byteaOID && len(v) >= 2 && v[0] == '\\' && v[1] == 'x' {
		if b, err := hex.DecodeString(string(v[2:])); err == nil {
			return b
		}
	}
	return string(v)
}

// nameTypes fills in Column.Type, asking the server about OIDs this
// session has not seen. A failed lookup leaves the type empty rather than
// failing the statement the user ran.
func (c *pgConn) nameTypes(ctx context.Context, cols []Column, oids []uint32) {
	var missing []uint32
	for _, oid := range oids {
		if _, ok := c.typeNames[oid]; !ok {
			missing = append(missing, oid)
		}
	}
	if len(missing) > 0 && ctx.Err() == nil {
		_ = c.query(ctx, "SELECT t.oid, format_type(t.oid, NULL) FROM unnest($1::oid[]) AS t(oid)", []any{missing}, func(r pgx.Rows) error {
			var oid uint32
			var name string
			err := r.Scan(&oid, &name)
			c.typeNames[oid] = name
			return err
		})
	}
	for i, oid := range oids {
		cols[i].Type = c.typeNames[oid]
	}
}

func (c *pgConn) errorOf(err error, st sqltext.Statement) *Error { return pgError(err, st) }

func (c *pgConn) Explain(ctx context.Context, sql string) (Plan, error) {
	stmt := sql
	if !alreadyExplains(sql) {
		stmt = "EXPLAIN (FORMAT JSON) " + sql
	}
	_, rows, _, _, err := c.run(ctx, stmt, 0)
	if err != nil {
		return Plan{SQL: stmt}, pgError(err, sqltext.Statement{SQL: stmt})
	}
	return Plan{SQL: stmt, Raw: firstColumnText(rows)}, nil
}

// firstColumnText joins the first cell of each row: one JSON document for
// a FORMAT JSON plan, the lines of a text plan the user asked for.
func firstColumnText(rows [][]any) string {
	var lines []string
	for _, r := range rows {
		if len(r) > 0 {
			if s, ok := r[0].(string); ok {
				lines = append(lines, s)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (c *pgConn) UpdateRows(ctx context.Context, schema, table string, keyColumns []string, rows []RowChange) (int, error) {
	stmts := buildRowUpdates(model.Postgres, schema, table, keyColumns, rows)
	tx, err := c.conn.Begin(ctx)
	if err != nil {
		return 0, pgError(err, sqltext.Statement{})
	}
	defer tx.Rollback(context.Background())
	for _, s := range stmts {
		// Simple protocol sends each value as an untyped literal, so the
		// server coerces the grid's text into the column's type, as it
		// would if the user typed it.
		args := append([]any{pgx.QueryExecModeSimpleProtocol}, s.Args...)
		if _, err := tx.Exec(ctx, s.SQL, args...); err != nil {
			return 0, pgError(err, sqltext.Statement{})
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, pgError(err, sqltext.Statement{})
	}
	return len(stmts), nil
}

// pgError turns err into an Error. The server's position is 1-based in
// characters of the statement sent; the editor counts bytes of the whole
// script.
func pgError(err error, st sqltext.Statement) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	out := &Error{Code: "ERR_UNKNOWN", Message: err.Error(), Offset: -1}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		out.Code, out.Message = pe.Code, pe.Message
		var parts []string
		for _, s := range []string{pe.Detail, pe.Hint, pe.Where, pe.SchemaName, pe.TableName} {
			if s != "" {
				parts = append(parts, s)
			}
		}
		out.Detail = strings.Join(parts, " · ")
		if pe.Position > 0 && st.SQL != "" {
			out.Offset = st.Start + runeOffset(st.SQL, int(pe.Position)-1)
		}
	}
	return out
}

// runeOffset is the byte offset of the n-th character of s, clamped to s.
func runeOffset(s string, n int) int {
	i := 0
	for b := range s {
		if i == n {
			return b
		}
		i++
	}
	return len(s)
}
