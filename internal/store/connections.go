package store

import (
	"context"
	"encoding/json"

	"github.com/egoist/mygo/plugins/sqlite"

	"seeker/internal/model"
)

const connectionColumns = `id, name, db_type, host, port, username, database, default_schema, ssl,
	url_params, connect_timeout_secs, query_timeout_secs, note, save_password,
	show_system_schemas, hidden_databases, options, position`

// redisOptions is the JSON of `options` for Redis, which keeps the fields
// only one engine family has, as recall does.
type redisOptions struct {
	DefaultDB int `json:"defaultDb"`
}

func (s *Store) Connections(ctx context.Context) ([]model.Profile, error) {
	rows, err := s.db.Query(ctx, "SELECT "+connectionColumns+" FROM connections ORDER BY position")
	if err != nil {
		return nil, err
	}
	out := make([]model.Profile, 0, len(rows.Values))
	for _, r := range rows.Values {
		out = append(out, profileOf(r))
	}
	return out, nil
}

func profileOf(r []any) model.Profile {
	p := model.Profile{
		ID:                 str(r[0]),
		Name:               str(r[1]),
		DBType:             model.DBType(str(r[2])),
		Host:               str(r[3]),
		Port:               num(r[4]),
		Username:           str(r[5]),
		SSL:                num(r[8]) != 0,
		ConnectTimeoutSecs: num(r[10]),
		Note:               str(r[12]),
		SavePassword:       num(r[13]) != 0,
	}
	if p.DBType == model.Redis {
		// A damaged `options` falls back to db 0, as in recall.
		var o redisOptions
		if json.Unmarshal([]byte(str(r[16])), &o) == nil && o.DefaultDB >= 0 {
			p.DefaultDB = o.DefaultDB
		}
		return p
	}
	p.Database = str(r[6])
	p.DefaultSchema = str(r[7])
	p.URLParams = str(r[9])
	p.QueryTimeoutSecs = num(r[11])
	p.ShowSystemSchemas = num(r[14]) != 0
	// Non-string entries of a damaged list are dropped, the rest kept.
	var hidden []any
	_ = json.Unmarshal([]byte(str(r[15])), &hidden)
	for _, h := range hidden {
		if name, ok := h.(string); ok {
			p.HiddenDatabases = append(p.HiddenDatabases, name)
		}
	}
	return p
}

// SaveConnections replaces every profile with ps, in their order, at once.
func (s *Store) SaveConnections(ctx context.Context, ps []model.Profile) error {
	stmts := []sqlite.Statement{{SQL: "DELETE FROM connections"}}
	for i, p := range ps {
		stmts = append(stmts, insertConnection(p, i))
	}
	_, err := s.db.Transaction(ctx, stmts)
	return err
}

func insertConnection(p model.Profile, position int) sqlite.Statement {
	database, schema, params, timeout, system := p.Database, p.DefaultSchema, p.URLParams, p.QueryTimeoutSecs, p.ShowSystemSchemas
	hidden := p.HiddenDatabases
	options := "{}"
	if p.DBType == model.Redis {
		// The SQL columns hold neutral values for a Redis row.
		database, schema, params, timeout, system, hidden = "", "", "", 0, false, nil
		data, _ := json.Marshal(redisOptions{DefaultDB: p.DefaultDB})
		options = string(data)
	}
	if hidden == nil {
		hidden = []string{}
	}
	hiddenJSON, _ := json.Marshal(hidden)
	return sqlite.Statement{
		SQL: "INSERT INTO connections (" + connectionColumns + ") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		Args: []any{
			p.ID, p.Name, string(p.DBType), p.Host, p.Port, p.Username, database, schema, boolInt(p.SSL),
			params, p.ConnectTimeoutSecs, timeout, p.Note, boolInt(p.SavePassword),
			boolInt(system), string(hiddenJSON), options, position,
		},
	}
}
