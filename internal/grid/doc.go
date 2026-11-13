// Package grid is the logic of the result grid that needs no UI: what a cell
// shows, how wide a column is, which columns are on screen, how the user's
// column order is kept, what a selection sums to, and the INSERT a row
// becomes. Ported from recall's src/mainview/lib.
//
// Cell values are what the drivers hand over: nil is NULL, string is the
// server's text form, []byte is a binary column. Column types are the names
// the drivers report: Postgres `format_type` spellings ("timestamp with time
// zone", "integer[]") and MySQL's upper-case `DatabaseTypeName` ("TIMESTAMP",
// "UNSIGNED INT"); the empty string is an unknown type. Dialects are
// sqltext.Dialect, the same value the splitter takes, so the grid needs no
// model import.
package grid
