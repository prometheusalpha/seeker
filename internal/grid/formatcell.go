package grid

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// displayZone renders the columns that carry an absolute instant: a value
// read in the session zone shows the wrong wall clock for a row written in
// Vietnam. Fixed +07 rather than the named Asia/Bangkok zone: Bangkok has
// been +07 since 1920, and the `+07` suffix is a constant, so an earlier
// date must not be shifted by the historical offset and still be labelled
// +07. It also needs no tzdata on the machine.
var displayZone = time.FixedZone("+07", 7*3600)

// displayOffset is appended to a rendered instant so the zone is never
// ambiguous.
const displayOffset = "+07"

// Types the databases report for a value stored as UTC and converted per
// session. Postgres spells a wall clock `timestamp without time zone`, so
// the bare `timestamp` is MySQL's: no collision. Everything else that looks
// like a date is a wall clock the database never meant to shift:
// `timestamp without time zone`, MySQL DATETIME, `date`, `time`.
var absoluteInstantTypes = map[string]bool{
	"timestamp with time zone": true,
	"timestamptz":              true,
	"timestamp":                true,
}

// isoInstant is an instant with a mandatory offset: `Z` or `±HH[:MM[:SS]]`,
// after a `T` or a space. Postgres text is `2026-01-01 06:15:00.123456+00`
// (offset `+HH`, `+HH:MM` or `+HH:MM:SS`), the MySQL driver appends
// `+00:00`, and the ISO `T`/`Z` shape is what recall's Date fallback made.
// Anything else (`infinity`, `date`/`time` text, MySQL text with no offset)
// misses and is left untouched.
var isoInstant = regexp.MustCompile(`^(\d{4,})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2}):(\d{2})(\.\d+)?(Z|[+-]\d{2}(?::?\d{2}(?::?\d{2})?)?)$`)

// offsetSeconds is an ISO offset as the seconds to subtract to reach UTC.
func offsetSeconds(text string) int {
	if text == "Z" {
		return 0
	}
	sign := 1
	if text[0] == '-' {
		sign = -1
	}
	digits := strings.ReplaceAll(text[1:], ":", "")
	part := func(from int) int {
		if len(digits) < from+2 {
			return 0
		}
		n, _ := strconv.Atoi(digits[from : from+2])
		return n
	}
	return sign * (part(0)*3600 + part(2)*60 + part(4))
}

// formatInstant renders an ISO instant as `YYYY-MM-DD HH:MM:SS[.fff…]+07`,
// or false when it does not parse.
func formatInstant(text string) (string, bool) {
	m := isoInstant.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
	year, err := strconv.Atoi(m[1])
	if err != nil {
		return "", false
	}
	// time.Date takes the year as is, so one below 100 is not remapped
	// into the 20th century.
	wall := time.Date(year, time.Month(n(2)), n(3), n(4), n(5), n(6), 0, time.UTC)
	out := wall.Add(-time.Duration(offsetSeconds(m[8])) * time.Second).In(displayZone)
	// The fraction never enters the time, so it is spliced back verbatim:
	// microseconds survive. No fraction in means no fraction out.
	return out.Format("2006-01-02 15:04:05") + m[7] + displayOffset, true
}

// FormatCell is the text a cell shows. columnType is the name the database
// reported ("" when unknown), the only thing that separates an instant from
// a wall clock once both are text.
//
// Extracted so the width sampler and the grid paint the identical string: a
// column measured with one formatting and painted with another clips its
// content or leaves a gap. NULL is the empty string; the grid draws it
// dimmed as `NULL` itself. Binary shows as base64, as recall did, because
// its drivers handed the grid base64 text (toJsonSafe).
func FormatCell(value any, columnType string) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		if absoluteInstantTypes[strings.ToLower(columnType)] {
			if s, ok := formatInstant(v); ok {
				return s
			}
		}
		return v
	case []byte:
		return base64.StdEncoding.EncodeToString(v)
	}
	if s, ok := numberText(value); ok {
		return s
	}
	if b, ok := value.(bool); ok {
		return strconv.FormatBool(b)
	}
	if s, ok := jsonText(value); ok {
		return s
	}
	return fmt.Sprint(value)
}

// numberText is the JavaScript `String(number)` of any Go number, which is
// what recall showed and inserted.
func numberText(value any) (string, bool) {
	switch v := value.(type) {
	case int:
		return strconv.FormatInt(int64(v), 10), true
	case int8:
		return strconv.FormatInt(int64(v), 10), true
	case int16:
		return strconv.FormatInt(int64(v), 10), true
	case int32:
		return strconv.FormatInt(int64(v), 10), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case uint:
		return strconv.FormatUint(uint64(v), 10), true
	case uint8:
		return strconv.FormatUint(uint64(v), 10), true
	case uint16:
		return strconv.FormatUint(uint64(v), 10), true
	case uint32:
		return strconv.FormatUint(uint64(v), 10), true
	case uint64:
		return strconv.FormatUint(v, 10), true
	case float32:
		return floatText(float64(v), 32), true
	case float64:
		return floatText(v, 64), true
	}
	return "", false
}

func floatText(f float64, bits int) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	if abs := math.Abs(f); abs != 0 && (abs >= 1e21 || abs < 1e-6) {
		// Go pads the exponent to two digits, JavaScript does not.
		s := strconv.FormatFloat(f, 'e', -1, bits)
		mant, exp, _ := strings.Cut(s, "e")
		return mant + "e" + exp[:1] + strings.TrimLeft(exp[1:], "0")
	}
	return strconv.FormatFloat(f, 'f', -1, bits)
}

// jsonText is `JSON.stringify` of a nested value, without Go's HTML
// escaping.
func jsonText(value any) (string, bool) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(value) != nil {
		return "", false
	}
	return strings.TrimSuffix(buf.String(), "\n"), true
}
