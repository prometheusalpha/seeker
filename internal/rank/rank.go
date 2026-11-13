// Package rank orders Quick Open candidates: which tables sink below the
// user's own, and how well a query matches a name.
package rank

import "strings"

// Catalog schemas across the dialects. MySQL rows carry the database as
// their schema, so `mysql`, `sys`, ... are caught here too.
var systemSchemas = map[string]bool{
	"information_schema": true,
	"pg_catalog":         true,
	"pg_toast":           true,
	"mysql":              true,
	"sys":                true,
	"performance_schema": true,
}

func IsSystemSchema(name string) bool {
	lower := strings.ToLower(name)
	return systemSchemas[lower] || strings.HasPrefix(lower, "pg_")
}

// TableDemotion is how far down Quick Open pushes a table regardless of
// match quality: 0 for an ordinary table, 1 for a `_`-prefixed one, 2 for
// anything in a system schema. Applied before the fuzzy score, so a system
// table never outranks user data even when it is the closer match.
func TableDemotion(schema, table string) int {
	if IsSystemSchema(schema) {
		return 2
	}
	if strings.HasPrefix(table, "_") {
		return 1
	}
	return 0
}
