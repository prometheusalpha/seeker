package grid

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// Text the grid hands to the clipboard and to files.
//
// What leaves the grid is the row the database returned, not what the grid
// paints: NULL is empty (JSON null), binary is base64 as recall wrote it,
// and everything else is the server's own text. Copy is the exception, which
// takes the displayed text, so what is pasted is what was read.

// RawText is a cell as an export writes it.
func RawText(value any) string { return FormatCell(value, "") }

// TSV joins rows of text with tabs and newlines. A tab or a newline inside
// a value would break the row and column contract for whatever receives the
// paste, so those become spaces.
func TSV(rows [][]string) string {
	lines := make([]string, len(rows))
	for i, row := range rows {
		cells := make([]string, len(row))
		for j, cell := range row {
			cells[j] = strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(cell)
		}
		lines[i] = strings.Join(cells, "\t")
	}
	return strings.Join(lines, "\n")
}

// csvCell is RFC 4180: quoted when the value holds a delimiter, a quote or
// a line break, with an embedded quote doubled.
func csvCell(text string) string {
	if strings.ContainsAny(text, "\",\n\r") {
		return `"` + strings.ReplaceAll(text, `"`, `""`) + `"`
	}
	return text
}

// CSV is the header and the rows, one line each, without a final newline.
func CSV(columns []string, rows [][]any) string {
	lines := make([]string, 0, len(rows)+1)
	head := make([]string, len(columns))
	for i, name := range columns {
		head[i] = csvCell(name)
	}
	lines = append(lines, strings.Join(head, ","))
	for _, row := range rows {
		cells := make([]string, len(columns))
		for i := range columns {
			if i < len(row) {
				cells[i] = csvCell(RawText(row[i]))
			}
		}
		lines = append(lines, strings.Join(cells, ","))
	}
	return strings.Join(lines, "\n")
}

// jsonValue is a cell as JSON: null, the server's text, base64 for binary,
// a number or boolean as itself.
func jsonValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		b, _ := json.Marshal(v)
		return string(b)
	case []byte:
		b, _ := json.Marshal(base64.StdEncoding.EncodeToString(v))
		return string(b)
	}
	if b, err := json.Marshal(value); err == nil {
		return string(b)
	}
	b, _ := json.Marshal(FormatCell(value, ""))
	return string(b)
}

// JSON is an array of objects keyed by column name, in column order,
// indented by two spaces as JSON.stringify(rows, null, 2) writes it.
func JSON(columns []string, rows [][]any) string {
	if len(rows) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteString("[\n")
	for r, row := range rows {
		b.WriteString("  {\n")
		for i, name := range columns {
			key, _ := json.Marshal(name)
			var value any
			if i < len(row) {
				value = row[i]
			}
			b.WriteString("    " + string(key) + ": " + jsonValue(value))
			if i < len(columns)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString("  }")
		if r < len(rows)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("]")
	return b.String()
}
