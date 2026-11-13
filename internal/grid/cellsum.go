package grid

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Summing a selection of result-grid cells. Drivers hand `numeric`,
// `decimal` and `bigint` over as text, and the declared column type is not
// reliable enough to gate on, so any text that parses to a finite number
// counts, whatever the column type.

// SumCell is one selected cell: the raw value plus the type the server
// declared.
type SumCell struct {
	Value      any
	ColumnType string
}

type CellSumResult struct {
	Sum   float64 // of every finite number in the selection; 0 when there are none
	Count int     // cells that contributed a number
	// Skipped cells are NULL, NaN, Infinity, non-numeric text or anything
	// else.
	Skipped int
}

var (
	moneyHead = regexp.MustCompile(`^[^\d.+-]+`)
	moneyTail = regexp.MustCompile(`[^\d]+$`)
)

// numericValue is the number a cell contributes, if any.
func numericValue(value any, columnType string) (float64, bool) {
	if s, ok := value.(string); ok {
		return parseNumber(s, strings.ToLower(columnType))
	}
	if s, ok := numberText(value); ok {
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil && !math.IsInf(f, 0) && !math.IsNaN(f)
	}
	return 0, false
}

func parseNumber(text, columnType string) (float64, bool) {
	// Postgres renders `money` in the session's currency, so the wire value
	// is `"$5.00"`. Only the surrounding decoration is stripped; the digits,
	// sign and decimal point that remain are parsed.
	if columnType == "money" || columnType == "smallmoney" {
		text = moneyTail.ReplaceAllString(moneyHead.ReplaceAllString(text, ""), "")
	}
	text = strings.TrimSpace(text)
	if text == "" || strings.ContainsAny(text, "_xX") {
		return 0, false
	}
	// ParseFloat reads "NaN" and "Inf" too, so finiteness is the only test
	// that keeps a Postgres `numeric` NaN out of the total. Hex and
	// underscore forms, which JavaScript's Number and Go disagree on, are
	// not numbers here.
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// SumCells adds up every cell that is a number or text parsing to one.
func SumCells(cells []SumCell) CellSumResult {
	var r CellSumResult
	for _, c := range cells {
		if v, ok := numericValue(c.Value, c.ColumnType); ok {
			r.Sum += v
			r.Count++
		} else {
			r.Skipped++
		}
	}
	return r
}
