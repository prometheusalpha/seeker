package store

import (
	"context"

	"seeker/internal/model"
)

// PinnedTables lists the pins in the order they were added.
func (s *Store) PinnedTables(ctx context.Context) ([]model.PinnedTable, error) {
	rows, err := s.db.Query(ctx, "SELECT connection_id, database, schema, table_name FROM pinned_tables ORDER BY position, table_name")
	if err != nil {
		return nil, err
	}
	out := make([]model.PinnedTable, 0, len(rows.Values))
	for _, r := range rows.Values {
		out = append(out, model.PinnedTable{ConnectionID: str(r[0]), Database: str(r[1]), Schema: str(r[2]), Table: str(r[3])})
	}
	return out, nil
}

// SetPinned pins or unpins t; pinning a pinned table keeps its place.
func (s *Store) SetPinned(ctx context.Context, t model.PinnedTable, pinned bool) error {
	key := []any{t.ConnectionID, t.Database, t.Schema, t.Table}
	var err error
	if pinned {
		_, err = s.db.Execute(ctx, "INSERT OR IGNORE INTO pinned_tables (connection_id, database, schema, table_name, position) VALUES (?, ?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM pinned_tables))", key...)
	} else {
		_, err = s.db.Execute(ctx, "DELETE FROM pinned_tables WHERE connection_id = ? AND database = ? AND schema = ? AND table_name = ?", key...)
	}
	return err
}
