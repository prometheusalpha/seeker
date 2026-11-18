package app

import (
	"context"
	"crypto/rand"
	"slices"
	"strconv"
	"strings"
	"time"

	"seeker/internal/driver"
	"seeker/internal/model"
	"seeker/internal/redis"
	"seeker/internal/secret"
)

// Dialogs is the state of the windows the shell opens over itself.
type Dialogs struct {
	Conn      ConnDialog
	Databases DatabasesDialog
	Close     CloseDialog
	Files     FileDialog
}

// The engines a connection can speak.
const (
	KindPostgres = "PostgreSQL"
	KindMySQL    = "MySQL"
	KindRedis    = "Redis"
)

// ConnectionKinds is what the dialog's type picker offers.
var ConnectionKinds = []string{KindPostgres, KindMySQL, KindRedis}

func kindOf(name string) model.DBType {
	switch name {
	case KindMySQL:
		return model.MySQL
	case KindRedis:
		return model.Redis
	}
	return model.Postgres
}

func kindName(t model.DBType) string {
	switch t {
	case model.MySQL:
		return KindMySQL
	case model.Redis:
		return KindRedis
	}
	return KindPostgres
}

// TestState is the connection dialog's test panel: a pending spinner, a
// success line, or the server's whole error.
type TestState struct {
	Pending bool
	OK      bool
	Detail  string
	Failure string
}

// ConnDialog is one connection form. Numbers are text, so a half-typed
// value stays as typed; they are parsed when the form is saved or tested.
type ConnDialog struct {
	Open    bool
	Editing string // profile id, "" for a new one

	Kind string // KindPostgres, KindMySQL or KindRedis
	TLS  string // derived from Params each frame
	Name string
	Host string
	Port string
	User string
	// Password is what the keychain had for an edit that saves it, and
	// what the user typed otherwise.
	Password string
	Database string
	Schema   string
	Params   string
	Note     string
	// DefaultDB is the logical database a Redis profile opens on.
	DefaultDB string

	ConnectTimeout string
	QueryTimeout   string
	ShowSystem     bool
	SavePassword   bool
	SSL            bool

	Err  string
	Test TestState

	// datalists from a successful test, as recall fills the database and
	// schema fields from
	Databases []string
	Schemas   []string

	// lastKind resets the engine's defaults when the type changes; sig
	// clears a test result the form no longer matches.
	lastKind string
	sig      string

	// draftID is the id a new connection keeps from the first test to the
	// save, so the keychain item a test writes is the one the saved profile
	// reads; secretWritten says a test wrote one, which a cancel removes.
	draftID       string
	secretWritten bool
	// testGen names the test whose result the dialog still wants.
	testGen int
	// PasswordLoading holds Save back while an edit's keychain read is in
	// flight; keepSecret is set when that read failed or found nothing, so
	// an untouched blank field does not overwrite the keychain item.
	PasswordLoading bool
	keepSecret      bool
}

func (a *App) DialogsState() *Dialogs { return &a.dlg }

// OpenConnectionDialog shows the form; id picks the profile to edit.
func (a *App) OpenConnectionDialog(id string) {
	d := &a.dlg.Conn
	*d = ConnDialog{
		Open: true, Editing: id, Kind: KindPostgres, TLS: "disable",
		Host: "localhost", Port: "5432", User: "postgres", Schema: "public",
		ConnectTimeout: "10", QueryTimeout: "0", SavePassword: true, DefaultDB: "0",
	}
	if id == "" {
		d.draftID = rand.Text()
		d.lastKind, d.sig = d.Kind, d.signature()
		return
	}
	p, ok := a.Profile(id)
	if !ok {
		d.lastKind, d.sig = d.Kind, d.signature()
		return
	}
	d.Kind = kindName(p.DBType)
	d.Name, d.Host, d.Port, d.User = p.Name, p.Host, strconv.Itoa(p.Port), p.Username
	d.Database, d.Schema, d.Params = p.Database, p.DefaultSchema, p.URLParams
	d.Note, d.ShowSystem, d.SavePassword = p.Note, p.ShowSystemSchemas, p.SavePassword
	d.SSL = p.SSL
	d.DefaultDB = strconv.Itoa(p.DefaultDB)
	if p.DBType.IsSQL() {
		d.TLS = driver.TLSMode(p.DBType, p.SSL, p.URLParams)
	}
	d.ConnectTimeout = strconv.Itoa(p.ConnectTimeoutSecs)
	d.QueryTimeout = strconv.Itoa(p.QueryTimeoutSecs)
	// lastKind follows Kind only now: set before the profile's engine, it
	// would read as a type change and reset the port, user and parameters.
	d.lastKind, d.sig = d.Kind, d.signature()
	if p.SavePassword {
		d.PasswordLoading = true
		go func() {
			pw, ok, err := loadPassword(p)
			a.upd(func() {
				d := &a.dlg.Conn
				if !d.Open || d.Editing != p.ID {
					return
				}
				d.PasswordLoading = false
				if err == nil && ok {
					d.Password = pw
					d.sig = d.signature()
				} else {
					d.keepSecret = true
				}
			})
		}()
	}
}

// CloseConnectionDialog drops the form without saving.
func (a *App) CloseConnectionDialog() {
	d := a.dlg.Conn
	a.dlg.Conn = ConnDialog{}
	// A test of a connection that was never saved left a keychain item under
	// its draft id.
	if d.secretWritten && d.Editing == "" {
		p := model.Profile{ID: d.draftID, Username: strings.TrimSpace(d.User)}
		go func() { _ = secret.Remove(p.ID, p.Username) }()
	}
}

// ConnSync runs before the form builds: it keeps the TLS choice and the URL
// parameters saying the same thing, resets an engine's defaults when the
// type changes, and drops a test result the form no longer matches.
func (a *App) ConnSync() {
	d := &a.dlg.Conn
	if !d.Open {
		return
	}
	if d.lastKind != "" && d.lastKind != d.Kind {
		kind := kindOf(d.Kind)
		d.SSL, d.Params = false, ""
		switch kind {
		case model.MySQL:
			d.Port, d.User, d.Schema = "3306", "root", ""
		case model.Redis:
			// Redis has no user until ACLs are set up, so the field starts blank.
			d.Port, d.User, d.Schema = "6379", "", ""
		default:
			d.Port, d.User, d.Schema = "5432", "postgres", "public"
		}
		d.Test = TestState{}
	}
	d.lastKind = d.Kind
	if kindOf(d.Kind).IsSQL() {
		d.TLS = driver.TLSMode(kindOf(d.Kind), d.SSL, d.Params)
	}
	if sig := d.signature(); d.sig != "" && sig != d.sig {
		d.Test = TestState{}
		d.sig = sig
	}
}

// ConnTLSChanged writes the chosen mode into the URL parameters, which the
// drivers read, and keeps the ssl switch in step.
func (a *App) ConnTLSChanged() {
	d := &a.dlg.Conn
	if !kindOf(d.Kind).IsSQL() {
		return
	}
	p := driver.WithTLSMode(model.Profile{DBType: kindOf(d.Kind), SSL: d.SSL, URLParams: d.Params}, d.TLS)
	d.Params, d.SSL = p.URLParams, p.SSL
}

// signature is every field the test ran against; a change clears its
// result, as recall's form does on any edit.
func (d *ConnDialog) signature() string {
	return strings.Join([]string{
		d.Kind, d.Name, d.Host, d.Port, d.User, d.Password, d.Database, d.Schema, d.Params,
		d.Note, d.ConnectTimeout, d.QueryTimeout, d.TLS, d.DefaultDB,
		strconv.FormatBool(d.ShowSystem), strconv.FormatBool(d.SavePassword), strconv.FormatBool(d.SSL),
	}, "\x00")
}

func (d *ConnDialog) validate() bool {
	d.Err = ""
	port, perr := strconv.Atoi(strings.TrimSpace(d.Port))
	switch {
	case strings.TrimSpace(d.Host) == "":
		d.Err = "Enter a host."
	case perr != nil || port <= 0:
		d.Err = "Enter a port between 1 and 65535."
	case port > 65535:
		d.Err = "Port must be 65535 or lower."
	case strings.TrimSpace(d.User) == "" && kindOf(d.Kind).IsSQL():
		d.Err = "Enter a user."
	case kindOf(d.Kind) == model.Redis && !validDB(d.DefaultDB):
		d.Err = "Default database is a number such as 0."
	}
	return d.Err == ""
}

func validDB(s string) bool {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return err == nil && n >= 0
}

// profile is the form as a profile: defaults where a number is blank, the
// hidden databases the edited profile already had, and the TLS mode applied
// to the parameters.
func (a *App) profile(d *ConnDialog) model.Profile {
	port := atoiOr(d.Port, 0)
	connect := atoiOr(d.ConnectTimeout, 0)
	query := atoiOr(d.QueryTimeout, 0)
	kind := kindOf(d.Kind)
	id := d.Editing
	if id == "" {
		if d.draftID == "" {
			d.draftID = rand.Text()
		}
		id = d.draftID
	}
	p := model.Profile{
		ID: id, Name: strings.TrimSpace(d.Name), DBType: kind,
		Host: strings.TrimSpace(d.Host), Port: port, Username: strings.TrimSpace(d.User),
		Note: d.Note, SavePassword: d.SavePassword, SSL: d.SSL,
		Database: strings.TrimSpace(d.Database), DefaultSchema: strings.TrimSpace(d.Schema),
		URLParams: d.Params, ConnectTimeoutSecs: connect, QueryTimeoutSecs: query,
		ShowSystemSchemas: d.ShowSystem,
	}
	if kind == model.Redis {
		// A Redis row has no SQL fields: TLS is the one switch.
		p.Database, p.DefaultSchema, p.URLParams, p.QueryTimeoutSecs, p.ShowSystemSchemas = "", "", "", 0, false
		p.DefaultDB = atoiOr(d.DefaultDB, 0)
		return p
	}
	if old, ok := a.Profile(id); ok {
		p.HiddenDatabases = old.HiddenDatabases
	}
	return driver.WithTLSMode(p, d.TLS)
}

func atoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

// loadPassword is the keychain read, off the window's thread.
func loadPassword(p model.Profile) (string, bool, error) {
	return secret.Load(p.ID, p.Username)
}

// TestConnectionDialog opens a throwaway session with the form's values and
// reports what the server says. It never touches the profile or the pool,
// and it saves the password when the form says to, as recall's does.
func (a *App) TestConnectionDialog() {
	d := &a.dlg.Conn
	if d.Test.Pending || !d.validate() {
		return
	}
	p := a.profile(d)
	password := d.Password
	d.Test = TestState{Pending: true}
	a.testGen++
	gen := a.testGen
	d.testGen = gen
	// A test result and the fields it filled describe the form as it was;
	// a new test starts them over.
	d.Databases, d.Schemas = nil, nil
	if p.SavePassword && password != "" {
		d.secretWritten = true
		go a.savePassword(p, password)
	}
	if p.DBType == model.Redis {
		go func() {
			ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
			defer cancel()
			info, err := redis.Test(ctx, redis.ConfigOf(p, password))
			a.upd(func() {
				d := &a.dlg.Conn
				if !d.Open || d.testGen != gen {
					return
				}
				if err != nil {
					d.Test = TestState{Failure: err.Error()}
					return
				}
				d.Test = TestState{OK: true, Detail: strings.TrimSpace(info.Product+" "+info.Version) + " · " + info.Database}
			})
		}()
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		defer cancel()
		conn, err := driver.Open(ctx, p, password, p.Database)
		var info driver.ServerInfo
		var dbs []string
		var schemas []string
		if err == nil {
			info, err = conn.Ping(ctx)
			if err == nil {
				if list, e := conn.ListDatabases(ctx, ""); e == nil {
					for _, db := range list {
						dbs = append(dbs, db.Name)
					}
				}
				if p.DBType == model.Postgres {
					if list, e := conn.ListSchemas(ctx, p.ShowSystemSchemas); e == nil {
						schemas = list
					}
				}
			}
			conn.Close()
		}
		a.upd(func() {
			d := &a.dlg.Conn
			if !d.Open || d.testGen != gen {
				return
			}
			if err != nil {
				d.Test = TestState{Failure: err.Error()}
				return
			}
			detail := strings.TrimSpace(info.ProductName + " " + info.ProductVersion)
			if info.CurrentDatabase != "" {
				detail += " · " + info.CurrentDatabase
			}
			d.Test = TestState{OK: true, Detail: detail}
			d.Databases, d.Schemas = dbs, schemas
		})
	}()
}

// SaveConnectionDialog stores the form and starts connecting it; a failed
// handshake shows on the connection's row rather than keeping the form
// open.
func (a *App) SaveConnectionDialog() {
	d := &a.dlg.Conn
	if d.PasswordLoading || !d.validate() {
		return
	}
	// An edit whose keychain read failed keeps the item unless the user
	// typed a password over the blank field.
	writeSecret := !(d.keepSecret && d.Password == "" && d.SavePassword)
	if err := a.saveProfile(a.profile(d), d.Password, writeSecret); err != nil {
		d.Err = "Could not save the connection: " + err.Error()
		return
	}
	a.dlg.Conn = ConnDialog{}
}

// DatabaseItem is one row of the database visibility dialog.
type DatabaseItem struct {
	Name    string
	Comment string
	Hidden  bool
	Own     bool // the profile's own database, which can never be hidden
}

type DatabasesDialog struct {
	Open    bool
	ConnID  string
	Filter  string
	Loading bool
	Err     string

	items []DatabaseItem
}

// OpenDatabasesDialog lists a server's databases, connecting first: the
// list comes from the server, not from what the sidebar has loaded.
func (a *App) OpenDatabasesDialog(id string) {
	p, ok := a.Profile(id)
	if !ok || !p.DBType.IsSQL() {
		return
	}
	a.dlg.Databases = DatabasesDialog{Open: true, ConnID: id, Loading: true}
	if a.Status(id) != Connected {
		a.Connect(id)
	}
	c := a.conn(id)
	go func() {
		err := waitConnected(c)
		var dbs []driver.DatabaseInfo
		if err == nil {
			err = a.pl.Catalog(a.ctx, id, p.Database, func(ctx context.Context, conn driver.Conn) error {
				var e error
				dbs, e = conn.ListDatabases(ctx, "")
				return e
			})
		}
		a.upd(func() {
			d := &a.dlg.Databases
			if !d.Open || d.ConnID != id {
				return
			}
			d.Loading = false
			if err != nil {
				d.Err = shortError(err)
				return
			}
			hidden := p.HiddenDatabases
			for _, db := range dbs {
				d.items = append(d.items, DatabaseItem{
					Name: db.Name, Comment: db.Comment,
					Hidden: db.Name != p.Database && slices.Contains(hidden, db.Name),
					Own:    db.Name == p.Database,
				})
			}
		})
	}()
}

// CloseDatabasesDialog hides the dialog; toggles were already saved.
func (a *App) CloseDatabasesDialog() { a.dlg.Databases = DatabasesDialog{} }

// DatabaseItems is the dialog's list, filtered by name as typed.
func (a *App) DatabaseItems() []DatabaseItem {
	d := &a.dlg.Databases
	q := strings.ToLower(strings.TrimSpace(d.Filter))
	if q == "" {
		return d.items
	}
	var out []DatabaseItem
	for _, it := range d.items {
		if strings.Contains(strings.ToLower(it.Name), q) {
			out = append(out, it)
		}
	}
	return out
}

// ToggleDatabase shows or hides one database. Hiding is profile state, read
// whenever the sidebar builds that connection's rows; open tabs keep
// working.
func (a *App) ToggleDatabase(name string) {
	d := &a.dlg.Databases
	for i := range d.items {
		if d.items[i].Name != name || d.items[i].Own {
			continue
		}
		d.items[i].Hidden = !d.items[i].Hidden
	}
	a.saveDatabaseVisibility()
}

// ShowAllDatabases clears the hidden list.
func (a *App) ShowAllDatabases() {
	d := &a.dlg.Databases
	for i := range d.items {
		d.items[i].Hidden = false
	}
	a.saveDatabaseVisibility()
}

// HideAllDatabases hides everything the server listed but the profile's
// own database.
func (a *App) HideAllDatabases() {
	d := &a.dlg.Databases
	for i := range d.items {
		d.items[i].Hidden = !d.items[i].Own
	}
	a.saveDatabaseVisibility()
}

func (a *App) saveDatabaseVisibility() {
	d := &a.dlg.Databases
	var hidden []string
	for _, it := range d.items {
		if it.Hidden && !it.Own {
			hidden = append(hidden, it.Name)
		}
	}
	if err := a.SetHiddenDatabases(d.ConnID, hidden); err != nil {
		d.Err = "Could not save the database list: " + err.Error()
	}
}

// CloseDialog is the unsaved-SQL confirmation, shared by close, close
// others and close all: every dirty tab queues in Tabs.Pending and one
// dialog asks for all of them.
type CloseDialog struct {
	Open bool
}

// PendingTabs is what the close dialog names.
func (a *App) PendingTabs() []Tab {
	var out []Tab
	for _, id := range a.tabs.Pending {
		if t, ok := a.TabByID(id); ok {
			out = append(out, t)
		}
	}
	return out
}
