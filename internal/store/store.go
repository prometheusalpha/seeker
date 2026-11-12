// Package store is the app's SQLite file: connection profiles, bookmarks,
// pinned tables and the UI settings recall kept in localStorage.
//
// It goes through mygo's sqlite plugin, which has no interactive
// transactions: Transaction runs a fixed batch. Anything that reads before
// deciding what to write does the read outside the batch.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/plugins/sqlite"
)

// The tables recall has, column for column, so importing its file is a
// copy; settings replaces recall's localStorage.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS connections (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		db_type TEXT NOT NULL CHECK (db_type IN ('postgres', 'mysql', 'redis')),
		host TEXT NOT NULL,
		port INTEGER NOT NULL,
		username TEXT NOT NULL,
		database TEXT NOT NULL,
		default_schema TEXT NOT NULL,
		ssl INTEGER NOT NULL,
		url_params TEXT NOT NULL,
		connect_timeout_secs INTEGER NOT NULL,
		query_timeout_secs INTEGER NOT NULL,
		note TEXT NOT NULL,
		save_password INTEGER NOT NULL,
		show_system_schemas INTEGER NOT NULL,
		hidden_databases TEXT NOT NULL DEFAULT '[]',
		options TEXT NOT NULL DEFAULT '{}',
		position INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS bookmarks (
		mnemonic TEXT PRIMARY KEY CHECK (mnemonic GLOB '[a-z0-9]'),
		path TEXT NOT NULL,
		line INTEGER NOT NULL,
		line_hash TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS bookmarks_path ON bookmarks(path)`,
	`CREATE TABLE IF NOT EXISTS pinned_tables (
		connection_id TEXT NOT NULL,
		database TEXT NOT NULL,
		schema TEXT NOT NULL,
		table_name TEXT NOT NULL,
		position INTEGER NOT NULL,
		PRIMARY KEY (connection_id, database, schema, table_name)
	)`,
	`CREATE INDEX IF NOT EXISTS pinned_tables_position ON pinned_tables(position)`,
	`CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
}

type Store struct {
	db *sqlite.DB
}

// Open opens the file at path, creating it and its folder when missing.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{})
	if err != nil {
		return nil, err
	}
	for _, stmt := range schema {
		if _, err := db.Execute(ctx, stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("store: schema: %w", err)
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ErrNoSetting is what Setting returns for a key never set.
var ErrNoSetting = errors.New("store: setting not set")

// Setting decodes the JSON stored under key into v.
func (s *Store) Setting(ctx context.Context, key string, v any) error {
	rows, err := s.db.Query(ctx, "SELECT value FROM settings WHERE key = ?", key)
	if err != nil {
		return err
	}
	if len(rows.Values) == 0 {
		return ErrNoSetting
	}
	return json.Unmarshal([]byte(str(rows.Values[0][0])), v)
}

func (s *Store) SetSetting(ctx context.Context, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Execute(ctx, "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", key, string(data))
	return err
}

func str(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	}
	return ""
}

func num(v any) int {
	switch v := v.(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
