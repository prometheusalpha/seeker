package app

// Snippet is a mnemonic template: the prefix the user types, offered in
// the completion list, inserted as an editor snippet whose ${n:name} fields
// are tab stops. Recall's built-ins, dialect-neutral; its settings dialog
// for editing them is not part of M3.
type Snippet struct {
	Label, Prefix, Body string
}

var defaultSnippets = []Snippet{
	{"select *", "sel", "SELECT *\nFROM ${1:table};"},
	{"insert into", "ins", "INSERT INTO ${1:table} (${2:columns})\nVALUES (${3:values});"},
	{"update set", "upd", "UPDATE ${1:table}\nSET ${2:column} = ${3:value}\nWHERE ${4:condition};"},
	{"delete from", "del", "DELETE FROM ${1:table}\nWHERE ${2:condition};"},
	{"common table expression", "cte", "WITH ${1:name} AS (\n  SELECT ${2:columns}\n  FROM ${3:table}\n)\nSELECT *\nFROM ${1:name};"},
	{"join", "join", "JOIN ${1:table} ON ${2:left_column} = ${3:right_column}"},
	{"case when", "case", "CASE\n  WHEN ${1:condition} THEN ${2:value}\n  ELSE ${3:default}\nEND"},
	{"create table", "ct", "CREATE TABLE ${1:table} (\n  ${2:column} ${3:type}\n);"},
	{"create index", "ci", "CREATE INDEX ${1:index_name}\nON ${2:table} (${3:column});"},
	{"explain analyze", "ex", "EXPLAIN ANALYZE\n${1:statement};"},
	{"create function", "fn", "CREATE OR REPLACE FUNCTION ${1:name}(${2:arguments})\nRETURNS ${3:return_type}\nLANGUAGE ${4:language}\nAS ${5:body};"},
	{"update every row (no WHERE — destructive)", "updall", "UPDATE ${1:table}\nSET ${2:column} = ${3:value};"},
}
