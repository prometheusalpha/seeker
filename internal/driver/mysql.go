package driver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"

	"seeker/internal/model"
	"seeker/internal/sqltext"
)

type myConn struct {
	// db is the session's own pool: conn is the session, and db opens a
	// second connection only to KILL a statement running on it.
	db       *sql.DB
	conn     *sql.Conn
	database string
	threadID int64
	// zone is the session's UTC offset, as +07:00, appended to TIMESTAMP
	// text. Read on open and again after a statement naming time_zone.
	zone string

	mu     sync.Mutex
	closed bool
}

var mysqlSystemSchemas = []any{"information_schema", "mysql", "performance_schema", "sys"}

func openMySQL(ctx context.Context, p model.Profile, password, database string) (Conn, error) {
	cfg, err := mysqlConfig(p, password, database)
	if err != nil {
		return nil, &Error{Code: "ERR_CONFIG", Message: err.Error(), Offset: -1}
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, &Error{Code: "ERR_CONFIG", Message: err.Error(), Offset: -1}
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(0)
	conn, err := db.Conn(ctx)
	if err == nil {
		c := &myConn{db: db, conn: conn, database: database}
		if err = conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&c.threadID); err == nil {
			c.readZone(ctx)
			return c, nil
		}
		conn.Close()
	}
	db.Close()
	return nil, myError(err)
}

// mysqlConfig maps the profile onto go-sql-driver's config.
//
// parseTime stays off and the text protocol is used for user SQL, so a
// value arrives as the server's text: no lost microseconds, no timezone
// shift. multiStatements stays off because Execute splits the script
// itself.
//
// recall's allowPublicKeyRetrieval=false refuses a caching_sha2 login that
// would fetch the server's RSA key over plain TCP, where a man in the
// middle can hand over its own key and read the password. go-sql-driver
// always fetches it and has no switch, so the opt-out becomes TLS that
// verifies the server: the login then never asks for the key, and an
// unverified TLS would leave the same man in the middle. A verify-ca the
// profile chose is kept.
func mysqlConfig(p model.Profile, password, database string) (*mysql.Config, error) {
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd = p.Username, password
	cfg.Net, cfg.Addr = "tcp", hostPort(p.Host, p.Port)
	// An empty database connects with no default schema; naming one
	// would fall back to `mysql`, which app accounts are denied.
	cfg.DBName = strings.TrimSpace(database)
	cfg.AllowNativePasswords = true
	secs := p.ConnectTimeoutSecs
	if secs <= 0 {
		secs = 30
	}
	cfg.Timeout = time.Duration(secs) * time.Second

	tlsMode := ""
	if p.SSL {
		tlsMode = "preferred"
	}
	keyRetrieval := true
	var ca, cert, key string
	for _, kv := range ParseURLParams(p.URLParams) {
		k, v := kv[0], strings.TrimSpace(kv[1])
		switch k {
		case "allowPublicKeyRetrieval":
			keyRetrieval = mysqlTrue[strings.ToLower(v)]
		case "ssl", "ssl-mode", "sslmode", "tls":
			tlsMode = strings.ToLower(v)
		case "ssl-ca":
			ca = v
		case "ssl-cert":
			cert = v
		case "ssl-key":
			key = v
		case "parseTime", "multiStatements", "interpolateParams", "clientFoundRows", "columnsWithAlias":
			// What Execute relies on is not the profile's to change.
		case "charset", "collation", "loc", "maxAllowedPacket", "readTimeout", "writeTimeout":
			if err := cfg.Apply(mysqlParam(k, v)); err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
		default:
			// Anything else is a system variable the driver SETs on connect.
			if cfg.Params == nil {
				cfg.Params = map[string]string{}
			}
			cfg.Params[k] = v
		}
	}
	if !keyRetrieval && tlsMode != "verify-ca" && tlsMode != "verify_ca" {
		tlsMode = "verify-full"
	}
	return cfg, applyMySQLTLS(cfg, tlsMode, ca, cert, key)
}

var mysqlTrue = map[string]bool{"1": true, "true": true, "yes": true, "on": true}

func mysqlParam(k, v string) mysql.Option {
	return func(c *mysql.Config) error {
		switch k {
		case "charset":
			c.Apply(mysql.Charset(v, ""))
		case "collation":
			c.Collation = v
		case "loc":
			loc, err := time.LoadLocation(v)
			if err != nil {
				return err
			}
			c.Loc = loc
		case "maxAllowedPacket":
			n, err := strconv.Atoi(v)
			if err != nil {
				return err
			}
			c.MaxAllowedPacket = n
		case "readTimeout", "writeTimeout":
			d, err := time.ParseDuration(v)
			if err != nil {
				return err
			}
			if k == "readTimeout" {
				c.ReadTimeout = d
			} else {
				c.WriteTimeout = d
			}
		}
		return nil
	}
}

// applyMySQLTLS reads the modes the connection dialog writes (require,
// verify-ca, verify-full) and recall's boolean spellings. require
// encrypts without checking the certificate, as libpq's does; verify-ca
// checks the chain but not the host name.
func applyMySQLTLS(cfg *mysql.Config, mode, ca, cert, key string) error {
	switch mode {
	case "", "0", "false", "no", "off", "disabled", "disable":
		return nil
	case "preferred", "prefer":
		// NewConnector turns the name into skip-verify TLS with fallback.
		cfg.TLSConfig = "preferred"
		return nil
	}
	tc := &tls.Config{ServerName: hostOnly(cfg.Addr)}
	if ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			return err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return fmt.Errorf("ssl-ca: no certificate in %s", ca)
		}
		tc.RootCAs = pool
	}
	if cert != "" || key != "" {
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return err
		}
		tc.Certificates = []tls.Certificate{pair}
	}
	switch mode {
	case "verify-full", "verify_identity":
	case "verify-ca", "verify_ca":
		tc.InsecureSkipVerify = true
		tc.VerifyPeerCertificate = verifyChainOnly(tc.RootCAs)
	default: // require and the true spellings
		tc.InsecureSkipVerify = true
	}
	cfg.TLS = tc
	return nil
}

func hostOnly(addr string) string {
	h := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		h = addr[:i]
	}
	return strings.Trim(h, "[]")
}

func verifyChainOnly(roots *x509.CertPool) func([][]byte, [][]*x509.Certificate) error {
	return func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) == 0 {
			return errors.New("tls: server sent no certificate")
		}
		certs := make([]*x509.Certificate, len(raw))
		for i, r := range raw {
			c, err := x509.ParseCertificate(r)
			if err != nil {
				return err
			}
			certs[i] = c
		}
		opts := x509.VerifyOptions{Roots: roots, Intermediates: x509.NewCertPool()}
		for _, c := range certs[1:] {
			opts.Intermediates.AddCert(c)
		}
		_, err := certs[0].Verify(opts)
		return err
	}
}

func (c *myConn) Type() model.DBType { return model.MySQL }
func (c *myConn) Database() string   { return c.database }

func (c *myConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.conn.Close()
	return c.db.Close()
}

func (c *myConn) IsClosed() bool {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return true
	}
	// database/sql hides the socket; asking the driver is the one probe
	// that does not send anything.
	err := c.conn.Raw(func(any) error { return nil })
	return errors.Is(err, sql.ErrConnDone) || errors.Is(err, mysql.ErrInvalidConn)
}

// markBroken records a session the driver gave up on, which database/sql
// would otherwise quietly replace with a new socket outside our session.
func (c *myConn) markBroken(err error) {
	if errors.Is(err, mysql.ErrInvalidConn) || errors.Is(err, sql.ErrConnDone) || errors.Is(err, errBadConn) {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
	}
}

var errBadConn = errors.New("driver: bad connection")

func (c *myConn) Ping(ctx context.Context) (ServerInfo, error) {
	var version string
	var db sql.NullString
	if err := c.conn.QueryRowContext(ctx, "SELECT VERSION(), DATABASE()").Scan(&version, &db); err != nil {
		c.markBroken(err)
		return ServerInfo{}, myError(err)
	}
	name := "MySQL"
	switch v := strings.ToLower(version); {
	case strings.Contains(v, "mariadb"):
		name = "MariaDB"
	case strings.Contains(v, "percona"):
		name = "Percona Server"
	}
	return ServerInfo{ProductName: name, ProductVersion: strings.TrimSpace(version), CurrentDatabase: db.String}, nil
}

// query runs catalog SQL. With arguments go-sql-driver uses a prepared
// statement, so values come back typed; text reads them all alike.
func (c *myConn) query(ctx context.Context, q string, args []any, scan func(row []any) error) error {
	rows, err := c.conn.QueryContext(ctx, q, args...)
	if err != nil {
		c.markBroken(err)
		return myError(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return myError(err)
		}
		if err := scan(vals); err != nil {
			return myError(err)
		}
	}
	if err := rows.Err(); err != nil {
		c.markBroken(err)
		return myError(err)
	}
	return nil
}

func text(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(v)
	case string:
		return v
	case time.Time:
		return v.Format("2006-01-02 15:04:05.999999")
	}
	return fmt.Sprint(v)
}

// truthy reads MySQL's booleans, which arrive as 1/0 of whatever type.
func truthy(v any) bool {
	s := strings.ToLower(text(v))
	return s == "1" || s == "true"
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func (c *myConn) ListDatabases(ctx context.Context, filter string) ([]DatabaseInfo, error) {
	var out []DatabaseInfo
	args := append(append([]any{}, mysqlSystemSchemas...), filter, "%"+filter+"%")
	err := c.query(ctx, "SELECT s.schema_name FROM information_schema.schemata s "+
		"WHERE s.schema_name NOT IN ("+placeholders(len(mysqlSystemSchemas))+") "+
		"AND (? = '' OR s.schema_name LIKE ?) ORDER BY s.schema_name", args, func(r []any) error {
		out = append(out, DatabaseInfo{Name: text(r[0])})
		return nil
	})
	return out, err
}

// ListSchemas: a MySQL database is its own schema, so the tree has no
// level for them and showSystem has nothing to show.
func (c *myConn) ListSchemas(context.Context, bool) ([]string, error) { return nil, nil }

var mysqlTableTypes = map[string]string{"BASE TABLE": "table", "VIEW": "view", "SYSTEM VIEW": "view"}

func (c *myConn) ListTables(ctx context.Context, schema, filter string) ([]TableInfo, error) {
	var out []TableInfo
	err := c.query(ctx, "SELECT t.table_name, t.table_type, t.table_comment, t.table_rows FROM information_schema.tables t "+
		"WHERE t.table_schema = ? AND (? = '' OR t.table_name LIKE ?) ORDER BY t.table_name",
		[]any{schema, filter, "%" + filter + "%"}, func(r []any) error {
			t := TableInfo{Name: text(r[0]), Comment: text(r[2]), RowCount: -1}
			if t.Type = mysqlTableTypes[text(r[1])]; t.Type == "" {
				t.Type = text(r[1])
			}
			// table_rows is NULL for views and never-analyzed tables: unknown.
			if n, err := strconv.ParseInt(text(r[3]), 10, 64); err == nil && n >= 0 {
				t.RowCount = n
			}
			out = append(out, t)
			return nil
		})
	return out, err
}

func (c *myConn) ListColumns(ctx context.Context, schema, table string) ([]ColumnInfo, error) {
	var out []ColumnInfo
	err := c.query(ctx, "SELECT c.column_name, c.column_type, c.is_nullable, c.column_default, c.column_comment, c.extra, "+
		"c.generation_expression, "+
		"EXISTS (SELECT 1 FROM information_schema.statistics s WHERE s.table_schema = c.table_schema "+
		"  AND s.table_name = c.table_name AND s.column_name = c.column_name AND s.index_name = 'PRIMARY') "+
		"FROM information_schema.columns c WHERE c.table_schema = ? AND c.table_name = ? ORDER BY c.ordinal_position",
		[]any{schema, table}, func(r []any) error {
			col := ColumnInfo{
				Name:       text(r[0]),
				DataType:   text(r[1]),
				Nullable:   strings.EqualFold(text(r[2]), "YES"),
				Comment:    text(r[4]),
				PrimaryKey: truthy(r[7]),
			}
			if r[3] != nil {
				col.HasDefault, col.Default = true, text(r[3])
			}
			col.DDL = mysqlAddColumn(schema, table, col, text(r[5]), text(r[6]))
			out = append(out, col)
			return nil
		})
	return out, err
}

var (
	mysqlGeneratedRe = regexp.MustCompile(`(?i)\b(VIRTUAL|STORED) GENERATED\b`)
	mysqlNowRe       = regexp.MustCompile(`(?i)^current_timestamp(\(\d*\))?$`)
	mysqlOnUpdateRe  = regexp.MustCompile(`(?i)on update (\S+)`)
	mysqlAutoIncRe   = regexp.MustCompile(`(?i)auto_increment`)
	mysqlDefExprRe   = regexp.MustCompile(`(?i)DEFAULT_GENERATED`)
)

// mysqlAddColumn composes ALTER TABLE … ADD COLUMN from
// information_schema. column_default is ambiguous: MySQL 8 reports a
// literal unquoted and flags an expression DEFAULT_GENERATED, MariaDB
// quotes its literals. A leading quote is kept, an expression goes in
// parens (bare for CURRENT_TIMESTAMP, which 5.7 rejects in parens), and
// anything else is quoted, which MySQL coerces for numbers too.
func mysqlAddColumn(schema, table string, col ColumnInfo, extra, genExpr string) string {
	parts := []string{QuoteIdent(model.MySQL, col.Name) + " " + col.DataType}
	gen := mysqlGeneratedRe.FindStringSubmatch(extra)
	if gen != nil && genExpr != "" {
		parts = append(parts, "GENERATED ALWAYS AS ("+genExpr+") "+strings.ToUpper(gen[1]))
	}
	if col.Nullable {
		parts = append(parts, "NULL")
	} else {
		parts = append(parts, "NOT NULL")
	}
	if col.HasDefault && !strings.EqualFold(col.Default, "NULL") && gen == nil {
		d := col.Default
		switch {
		case strings.HasPrefix(d, "'"), mysqlNowRe.MatchString(d):
			parts = append(parts, "DEFAULT "+d)
		case mysqlDefExprRe.MatchString(extra):
			parts = append(parts, "DEFAULT ("+d+")")
		default:
			parts = append(parts, "DEFAULT "+mysqlString(d))
		}
	}
	if mysqlAutoIncRe.MatchString(extra) {
		parts = append(parts, "AUTO_INCREMENT")
	}
	if m := mysqlOnUpdateRe.FindStringSubmatch(extra); m != nil {
		parts = append(parts, "ON UPDATE "+m[1])
	}
	if col.Comment != "" {
		parts = append(parts, "COMMENT "+mysqlString(col.Comment))
	}
	return "ALTER TABLE " + Qualify(model.MySQL, schema, table) + " ADD COLUMN " + strings.Join(parts, " ") + ";"
}

func mysqlString(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", "''") + "'"
}

func (c *myConn) ListIndexes(ctx context.Context, schema, table string) ([]IndexInfo, error) {
	var out []IndexInfo
	args := append([]any{schema, table}, mysqlSystemSchemas...)
	err := c.query(ctx, "SELECT s.index_name, s.column_name, s.non_unique, s.index_type FROM information_schema.statistics s "+
		"WHERE s.table_schema = ? AND s.table_name = ? AND s.table_schema NOT IN ("+placeholders(len(mysqlSystemSchemas))+") "+
		"ORDER BY s.index_name, s.seq_in_index", args, func(r []any) error {
		name := text(r[0])
		if len(out) == 0 || out[len(out)-1].Name != name {
			// non_unique: MySQL spells it the other way round. index_type is
			// lowercased to match the pg_am spelling.
			out = append(out, IndexInfo{Name: name, Unique: !truthy(r[2]), Primary: name == "PRIMARY", Method: strings.ToLower(text(r[3]))})
		}
		// An expression part has no column name.
		if r[1] != nil {
			last := &out[len(out)-1]
			last.Columns = append(last.Columns, text(r[1]))
		}
		return nil
	})
	for i := range out {
		out[i].DDL = mysqlIndexDDL(schema, table, out[i])
	}
	return out, err
}

// mysqlIndexDDL composes the statement MySQL has no catalog function to
// print. A primary key is only a table constraint; FULLTEXT and SPATIAL
// are kinds, not USING options; HASH is the one method worth naming.
func mysqlIndexDDL(schema, table string, ix IndexInfo) string {
	target := Qualify(model.MySQL, schema, table)
	keys := make([]string, len(ix.Columns))
	for i, c := range ix.Columns {
		keys[i] = QuoteIdent(model.MySQL, c)
	}
	list := strings.Join(keys, ", ")
	if ix.Primary {
		return "ALTER TABLE " + target + " ADD PRIMARY KEY (" + list + ")"
	}
	kind := ""
	switch {
	case ix.Method == "fulltext":
		kind = "FULLTEXT "
	case ix.Method == "spatial":
		kind = "SPATIAL "
	case ix.Unique:
		kind = "UNIQUE "
	}
	using := ""
	if ix.Method == "hash" {
		using = " USING HASH"
	}
	return "CREATE " + kind + "INDEX " + QuoteIdent(model.MySQL, ix.Name) + " ON " + target + " (" + list + ")" + using
}

func (c *myConn) ListTriggers(ctx context.Context, schema, table string) ([]TriggerInfo, error) {
	var out []TriggerInfo
	args := append([]any{schema, table}, mysqlSystemSchemas...)
	err := c.query(ctx, "SELECT t.trigger_name, t.action_timing, t.event_manipulation, t.action_statement FROM information_schema.triggers t "+
		"WHERE t.trigger_schema = ? AND t.event_object_table = ? AND t.trigger_schema NOT IN ("+placeholders(len(mysqlSystemSchemas))+") "+
		"ORDER BY t.trigger_name", args, func(r []any) error {
		name, stmt := text(r[0]), text(r[3])
		// A multi-statement body can come as several rows of one trigger.
		if n := len(out); n > 0 && out[n-1].Name == name {
			last := &out[n-1]
			if stmt != "" && !strings.Contains(last.Statement, stmt) {
				if last.Statement == "" {
					last.Statement = stmt
				} else {
					last.Statement += ";\n" + stmt
				}
			}
			return nil
		}
		out = append(out, TriggerInfo{Name: name, Timing: text(r[1]), Event: text(r[2]), Statement: stmt})
		return nil
	})
	return out, err
}

func (c *myConn) ListForeignKeys(ctx context.Context, schema, table string) ([]ForeignKeyInfo, error) {
	var out []ForeignKeyInfo
	args := append([]any{schema, table}, mysqlSystemSchemas...)
	err := c.query(ctx, "SELECT k.constraint_name, k.column_name, k.referenced_table_schema, k.referenced_table_name, "+
		"k.referenced_column_name, r.update_rule, r.delete_rule "+
		"FROM information_schema.key_column_usage k "+
		"JOIN information_schema.referential_constraints r ON r.constraint_schema = k.constraint_schema "+
		"  AND r.constraint_name = k.constraint_name AND r.table_name = k.table_name "+
		"WHERE k.table_schema = ? AND k.table_name = ? AND k.referenced_table_name IS NOT NULL "+
		"AND k.table_schema NOT IN ("+placeholders(len(mysqlSystemSchemas))+") "+
		"ORDER BY k.constraint_name, k.ordinal_position", args, func(r []any) error {
		name := text(r[0])
		if len(out) == 0 || out[len(out)-1].Name != name {
			refSchema, refTable := text(r[2]), text(r[3])
			ref := refTable
			if refSchema != "" && refSchema != schema {
				ref = refSchema + "." + refTable
			}
			out = append(out, ForeignKeyInfo{Name: name, ReferencedTable: ref,
				OnUpdate: strings.ToUpper(strings.TrimSpace(text(r[5]))), OnDelete: strings.ToUpper(strings.TrimSpace(text(r[6])))})
		}
		last := &out[len(out)-1]
		if r[1] != nil {
			last.Columns = append(last.Columns, text(r[1]))
		}
		if r[4] != nil {
			last.ReferencedColumns = append(last.ReferencedColumns, text(r[4]))
		}
		return nil
	})
	return out, err
}

func (c *myConn) TableDDL(ctx context.Context, schema, table string) (string, error) {
	rows, err := c.conn.QueryContext(ctx, "SHOW CREATE TABLE "+Qualify(model.MySQL, schema, table))
	if err != nil {
		c.markBroken(err)
		return "", myError(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", myError(err)
		}
		return "", &Error{Code: "ERR_NOT_FOUND", Message: fmt.Sprintf("Table `%s`.`%s` does not exist.", schema, table), Offset: -1}
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return "", myError(err)
	}
	// The column is `Create Table` for a table and `Create View` for a view.
	for i, name := range cols {
		k := strings.ToLower(strings.ReplaceAll(name, " ", ""))
		if k == "createtable" || k == "createview" {
			return text(vals[i]), nil
		}
	}
	return "", nil
}

func (c *myConn) CountRows(ctx context.Context, schema, table, where string) (int64, error) {
	q := "SELECT COUNT(*) FROM " + Qualify(model.MySQL, schema, table)
	if w := strings.TrimSpace(where); w != "" {
		q += " WHERE " + w
	}
	if err := singleStatement(q, sqltext.MySQL); err != nil {
		return 0, err
	}
	var n int64
	if err := c.conn.QueryRowContext(ctx, q).Scan(&n); err != nil {
		c.markBroken(err)
		return 0, myError(err)
	}
	return n, nil
}

func (c *myConn) Execute(ctx context.Context, script string, maxRows int, progress func(Result)) []Result {
	return execute(ctx, c, sqltext.MySQL, script, maxRows, progress)
}

// killer stops a statement on cancel with KILL QUERY from a second
// connection. go-sql-driver on its own only closes our socket, which
// loses the session and leaves the statement running on the server.
func (c *myConn) killer(ctx context.Context) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-done:
		case <-ctx.Done():
			kctx, cancel := context.WithTimeout(context.Background(), cancelDeadline)
			defer cancel()
			_, _ = c.db.ExecContext(kctx, "KILL QUERY "+strconv.FormatInt(c.threadID, 10))
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

// binaryTypes are the column types whose bytes are not text.
var binaryTypes = map[string]bool{"BINARY": true, "VARBINARY": true, "BLOB": true, "TINYBLOB": true, "MEDIUMBLOB": true, "LONGBLOB": true, "GEOMETRY": true, "BIT": true}

func (c *myConn) run(ctx context.Context, q string, maxRows int) ([]Column, [][]any, int64, bool, error) {
	stop := c.killer(ctx)
	defer stop()
	// The query itself never sees ctx: the killer is what cancels it.
	bg := context.WithoutCancel(ctx)
	rows, err := c.conn.QueryContext(bg, q)
	if err != nil {
		c.markBroken(err)
		return nil, nil, 0, false, err
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, nil, 0, false, err
	}
	cols := make([]Column, len(types))
	binary := make([]bool, len(types))
	stamp := -1
	for i, t := range types {
		cols[i] = Column{Name: t.Name(), Type: t.DatabaseTypeName()}
		binary[i] = binaryTypes[cols[i].Type]
		if cols[i].Type == "TIMESTAMP" {
			stamp = i
		}
	}
	var out [][]any
	truncated := false
	for rows.Next() {
		if maxRows > 0 && len(out) >= maxRows {
			truncated = true
			continue
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, 0, false, err
		}
		for i, v := range vals {
			vals[i] = myCell(v, binary[i])
		}
		out = append(out, vals)
	}
	if err := rows.Err(); err != nil {
		c.markBroken(err)
		return nil, nil, 0, false, err
	}
	rows.Close()
	if strings.Contains(strings.ToLower(q), "time_zone") {
		c.readZone(bg)
	}
	if len(cols) == 0 {
		// A statement without a result set: its count is the session's
		// ROW_COUNT(), which nothing else has run on since.
		var n int64
		if err := c.conn.QueryRowContext(bg, "SELECT ROW_COUNT()").Scan(&n); err == nil && n > 0 {
			return nil, nil, n, false, nil
		}
		return nil, nil, 0, false, nil
	}
	if stamp >= 0 {
		c.zoneTimestamps(cols, out)
	}
	return cols, out, int64(len(out)), truncated, nil
}

// readZone asks for the session's UTC offset. TIMESTAMP is stored as UTC
// and printed in the session's zone without saying which. Any statement
// naming time_zone may have changed it (SET time_zone, SET @@session…),
// so run calls this again after one; a zone set inside a procedure goes
// unnoticed. With DST this is the offset of now, not of each value. On
// failure the offset stays unknown and TIMESTAMPs are left as printed.
func (c *myConn) readZone(ctx context.Context) {
	var diff string
	if err := c.conn.QueryRowContext(ctx, "SELECT TIME_FORMAT(TIMEDIFF(NOW(), UTC_TIMESTAMP()), '%H:%i')").Scan(&diff); err != nil {
		c.zone = ""
		return
	}
	c.zone = diff
	if !strings.HasPrefix(diff, "-") {
		c.zone = "+" + diff
	}
}

// zoneTimestamps appends the session's offset to TIMESTAMP text, so it
// reads as an instant like Postgres timestamptz.
func (c *myConn) zoneTimestamps(cols []Column, rows [][]any) {
	offset := c.zone
	if offset == "" {
		return
	}
	for i, col := range cols {
		if col.Type != "TIMESTAMP" {
			continue
		}
		for _, r := range rows {
			// The zero date is a placeholder, not an instant.
			if s, ok := r[i].(string); ok && !strings.HasPrefix(s, "0000-00-00") {
				r[i] = s + offset
			}
		}
	}
}

// myCell turns what go-sql-driver decoded back into text. Even over the
// text protocol it parses integers and floats; integers print back
// exactly, floats in Go's shortest form, which spells an exponent 1e+21
// where MySQL prints 1e21.
func myCell(v any, binary bool) any {
	switch v := v.(type) {
	case []byte:
		if binary {
			return v
		}
		return string(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
	return v
}

func (c *myConn) errorOf(err error, _ sqltext.Statement) *Error { return myError(err) }

func (c *myConn) Explain(ctx context.Context, q string) (Plan, error) {
	stmt := q
	if !alreadyExplains(q) {
		stmt = "EXPLAIN FORMAT=JSON " + q
	}
	_, rows, _, _, err := c.run(ctx, stmt, 0)
	if err != nil {
		return Plan{SQL: stmt}, myError(err)
	}
	return Plan{SQL: stmt, Raw: firstColumnText(rows)}, nil
}

func (c *myConn) UpdateRows(ctx context.Context, schema, table string, keyColumns []string, rows []RowChange) (int, error) {
	stmts := buildRowUpdates(model.MySQL, schema, table, keyColumns, rows)
	tx, err := c.conn.BeginTx(ctx, nil)
	if err != nil {
		c.markBroken(err)
		return 0, myError(err)
	}
	defer tx.Rollback()
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s.SQL, s.Args...); err != nil {
			return 0, myError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, myError(err)
	}
	return len(stmts), nil
}

// myError: MySQL reports no position, so Offset is always -1.
func myError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	out := &Error{Code: "ERR_UNKNOWN", Message: err.Error(), Offset: -1}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		out.Code, out.Message = strconv.Itoa(int(me.Number)), me.Message
		if me.SQLState != [5]byte{} {
			out.Detail = string(me.SQLState[:])
		}
	}
	return out
}
