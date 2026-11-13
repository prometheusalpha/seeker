package grid

import (
	"strings"
	"testing"
)

func TestFormatCellInstants(t *testing.T) {
	cases := []struct {
		name, value, typ, want string
	}{
		// Postgres text keeps its microsecond digits through the re-zone.
		{"keeps six fractional digits from Postgres text", "2026-01-01T06:15:00.123456-05:45", "timestamp with time zone", "2026-01-01 19:00:00.123456+07"},
		{"keeps three fractional digits from the millisecond ISO Z", "2026-01-01T12:00:00.123Z", "timestamptz", "2026-01-01 19:00:00.123+07"},
		{"accepts an offset with a colon and no fraction", "2026-01-01T19:00:00+07:00", "timestamptz", "2026-01-01 19:00:00+07"},
		{"keeps a wall clock untouched", "2026-01-01 12:00:00.123456", "timestamp without time zone", "2026-01-01 12:00:00.123456"},
		{"leaves infinity untouched", "infinity", "timestamptz", "infinity"},
		{"leaves negative infinity untouched", "-infinity", "timestamptz", "-infinity"},
		{"leaves Postgres date text untouched", "2026-01-01", "date", "2026-01-01"},
		{"leaves an ISO string of a non-instant type untouched", "2026-01-01T06:15:00.123456-05:45", "text", "2026-01-01T06:15:00.123456-05:45"},
		{"leaves an unknown type untouched", "2026-01-01T06:15:00Z", "", "2026-01-01T06:15:00Z"},

		// Shapes the Go drivers produce.
		{"Postgres simple protocol: space and +HH", "2026-01-01 06:15:00.123456+00", "timestamp with time zone", "2026-01-01 13:15:00.123456+07"},
		{"Postgres +HH:MM", "2026-01-01 06:15:00-05:45", "timestamp with time zone", "2026-01-01 19:00:00+07"},
		{"Postgres +HH:MM:SS", "2026-01-01 06:15:00.5+00:00:30", "timestamptz", "2026-01-01 13:14:30.5+07"},
		{"MySQL TIMESTAMP with the appended session offset", "2026-01-01 06:15:00.123456+00:00", "TIMESTAMP", "2026-01-01 13:15:00.123456+07"},
		{"MySQL TIMESTAMP crossing midnight", "2026-01-01 20:00:00+00:00", "timestamp", "2026-01-02 03:00:00+07"},
		{"MySQL DATETIME is a wall clock", "2026-01-01 06:15:00+00:00", "DATETIME", "2026-01-01 06:15:00+00:00"},
		{"MySQL TIMESTAMP without offset is left alone", "2026-01-01 06:15:00", "TIMESTAMP", "2026-01-01 06:15:00"},
		{"type match is case-insensitive", "2026-01-01 00:00:00+07", "TIMESTAMPTZ", "2026-01-01 00:00:00+07"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FormatCell(c.value, c.typ); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}

	t.Run("does not remap a year below 100 into the 20th century", func(t *testing.T) {
		got := FormatCell("0099-01-01T00:00:00Z", "timestamptz")
		year, _, _ := strings.Cut(got, "-")
		if year != "0099" {
			t.Errorf("got %q", got)
		}
	})
}

func TestFormatCellNonStrings(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"stringifies ints", 42, "42"},
		{"stringifies int64", int64(-7), "-7"},
		{"stringifies uint64", uint64(18446744073709551615), "18446744073709551615"},
		{"stringifies bool", true, "true"},
		{"empties nil", nil, ""},
		{"float without exponent", 1.5, "1.5"},
		{"float with JS exponent", 1e21, "1e+21"},
		{"small float with JS exponent", 1e-7, "1e-7"},
		{"JSON-encodes nested values", map[string]any{"a": 1, "b": []any{2, "x"}}, `{"a":1,"b":[2,"x"]}`},
		{"JSON does not escape HTML", []any{"<a&b>"}, `["<a&b>"]`},
		{"binary shows as base64, as recall's drivers delivered it", []byte{0xde, 0xad, 0xbe, 0xef}, "3q2+7w=="},
		{"empty binary is empty", []byte{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FormatCell(c.value, ""); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
