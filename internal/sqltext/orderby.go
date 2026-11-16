package sqltext

import "strings"

// OrderTerm is one column an ORDER BY sorts by.
type OrderTerm struct {
	Column string
	Desc   bool
}

// OrderTerms reads the columns an ORDER BY clause (without the keywords)
// sorts by, in order. A term that is not a plain, possibly qualified and
// quoted, column (an expression, a call, a position) is left out: the
// header can only mark a column it can name.
func OrderTerms(clause string) []OrderTerm {
	var out []OrderTerm
	for _, term := range splitTopLevel(clause) {
		col, rest, ok := leadingIdent(strings.TrimSpace(term))
		if !ok {
			continue
		}
		desc := false
		for _, w := range strings.Fields(rest) {
			switch strings.ToUpper(w) {
			case "DESC":
				desc = true
			case "ASC":
			default:
				// An operator or a call after the name: not a plain column.
				if !strings.EqualFold(w, "NULLS") && !strings.EqualFold(w, "FIRST") && !strings.EqualFold(w, "LAST") {
					col = ""
				}
			}
		}
		if col != "" {
			out = append(out, OrderTerm{Column: col, Desc: desc})
		}
	}
	return out
}

// splitTopLevel cuts s at the commas outside quotes and parentheses.
func splitTopLevel(s string) []string {
	var out []string
	var quote rune
	depth, start := 0, 0
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"' || r == '`':
			quote = r
		case r == '(':
			depth++
		case r == ')':
			depth--
		case r == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// leadingIdent takes the column name a term starts with, the last part of a
// qualified one, and returns the rest of the term.
func leadingIdent(s string) (name, rest string, ok bool) {
	for {
		if s == "" {
			return "", "", false
		}
		var part string
		if q := s[0]; q == '"' || q == '`' {
			end := strings.IndexByte(s[1:], q)
			if end < 0 {
				return "", "", false
			}
			part, s = s[1:end+1], s[end+2:]
		} else {
			end := strings.IndexFunc(s, func(r rune) bool {
				return r == '.' || r == ' ' || r == '\t' || r == '\n' || r == '(' || r == ')' || r == ','
			})
			if end < 0 {
				end = len(s)
			}
			part, s = s[:end], s[end:]
			if part == "" || strings.ContainsAny(part, "'+-*/") || strings.Trim(part, "0123456789") == "" {
				return "", "", false
			}
		}
		if strings.HasPrefix(s, "(") {
			return "", "", false
		}
		name = part
		if !strings.HasPrefix(s, ".") {
			return name, s, true
		}
		s = s[1:]
	}
}
