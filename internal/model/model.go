// Package model holds the domain types the store, the drivers and the
// view share.
package model

type DBType string

const (
	Postgres DBType = "postgres"
	MySQL    DBType = "mysql"
	Redis    DBType = "redis"
)

func (t DBType) IsSQL() bool { return t == Postgres || t == MySQL }

// Profile is a saved connection. Its password is in the keychain, never
// here. The SQL fields are zero for Redis, and DefaultDB for SQL, as in
// recall's `connections` table.
type Profile struct {
	ID                 string
	Name               string
	DBType             DBType
	Host               string
	Port               int
	Username           string
	SSL                bool
	ConnectTimeoutSecs int
	Note               string
	SavePassword       bool

	Database          string
	DefaultSchema     string // Postgres only
	URLParams         string // raw query string appended to the driver URL
	QueryTimeoutSecs  int
	ShowSystemSchemas bool
	// HiddenDatabases are kept out of the sidebar and Quick Open; a list of
	// hidden names, so a database the server grows shows up by itself.
	HiddenDatabases []string

	DefaultDB int // Redis only
}

// Bookmark is a mnemonic bookmark: one letter or digit naming a line of a
// SQL file. LineHash finds the line again after edits above it.
type Bookmark struct {
	Mnemonic string
	Path     string
	Line     int // one-based
	LineHash string
}

// Datasource is where a SQL tab or file runs: the connection, and the
// database and schema within it.
type Datasource struct {
	Conn     string `json:"connectionId"`
	Database string `json:"database"`
	Schema   string `json:"schema"`
}

type PinnedTable struct {
	ConnectionID string
	Database     string
	Schema       string
	Table        string
}
