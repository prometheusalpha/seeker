package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/plugins/sqlite"
)

// RecallDB is where recall keeps its file on macOS: Electrobun's app data
// folder, named after recall's bundle identifier.
func RecallDB() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "app.recall.desktop", "recall.db"), nil
}

const importedKey = "imported.recall"

// ImportRecall copies recall's connections, bookmarks and pinned tables
// from the file at path, once: the first run of a store with no
// connections imports, and every later run leaves it, even after the user
// deletes what came over. Passwords stay in the keychain under recall's
// service names, which internal/secret shares. It returns how many
// connections came over.
func (s *Store) ImportRecall(ctx context.Context, path string) (int, error) {
	var done bool
	switch err := s.Setting(ctx, importedKey, &done); {
	case err == nil:
		return 0, nil
	case !errors.Is(err, ErrNoSetting):
		return 0, err
	}
	mark := sqlite.Statement{SQL: "INSERT OR REPLACE INTO settings (key, value) VALUES (?, 'true')", Args: []any{importedKey}}
	existing, err := s.db.Query(ctx, "SELECT count(*) FROM connections")
	if err != nil {
		return 0, err
	}
	if _, err := os.Stat(path); err != nil || num(existing.Values[0][0]) > 0 {
		_, err := s.db.Transaction(ctx, []sqlite.Statement{mark})
		return 0, err
	}

	src, err := sqlite.Open(ctx, path, sqlite.OpenOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer src.Close()
	stmts := []sqlite.Statement{mark}
	conns, err := copyRows(ctx, src, "connections", connectionColumns, &stmts)
	if err != nil {
		return 0, err
	}
	if _, err := copyRows(ctx, src, "bookmarks", "mnemonic, path, line, line_hash", &stmts); err != nil {
		return 0, err
	}
	if _, err := copyRows(ctx, src, "pinned_tables", "connection_id, database, schema, table_name, position", &stmts); err != nil {
		return 0, err
	}
	if _, err := s.db.Transaction(ctx, stmts); err != nil {
		return 0, err
	}
	return conns, nil
}

// copyRows reads columns of table from src and appends their inserts.
func copyRows(ctx context.Context, src *sqlite.DB, table, columns string, stmts *[]sqlite.Statement) (int, error) {
	rows, err := src.Query(ctx, "SELECT "+columns+" FROM "+table)
	if err != nil {
		return 0, err
	}
	marks := "?"
	for range len(rows.Columns) - 1 {
		marks += ", ?"
	}
	for _, r := range rows.Values {
		*stmts = append(*stmts, sqlite.Statement{SQL: "INSERT OR REPLACE INTO " + table + " (" + columns + ") VALUES (" + marks + ")", Args: r})
	}
	return len(rows.Values), nil
}
