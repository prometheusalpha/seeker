package sqltext

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"seeker/internal/rank"
)

// Column suggestions for the raw WHERE / ORDER BY fragments of the result
// filter bar.
//
// The field holds SQL the user typed rather than a structure we own, so the
// list reads the fragment around the caret before it can answer: `id =
// '73ba` is inside a literal and has nothing to suggest, `id = ` wants a
// field, and `id = 1 ` wants the connector that keeps the expression going.
// Which one comes from the text immediately left of the caret: a token scan,
// not a parser. All offsets are bytes, as the editor's.

// ClauseKind is the fragment a field edits.
type ClauseKind int

const (
	ClauseWhere ClauseKind = iota
	ClauseOrderBy
)

// ClauseRole is what the fragment at the caret is waiting for.
type ClauseRole int

const (
	RoleField ClauseRole = iota
	RoleConnector
	RoleNone
)

// ClauseTarget is the identifier run under the caret, and the span a pick
// replaces.
type ClauseTarget struct {
	// Value is the whole fragment: the role is read from the text left of
	// the caret.
	Value string
	// Token is what the user has typed of the identifier under the caret.
	Token string
	// From and To are the replacement span: the run under the caret, or the
	// selection when there is one.
	From, To int
	// QuotedIdentifier: the caret is inside a quoted identifier, so its
	// content is what changes.
	QuotedIdentifier bool
	// InsideString: the caret is inside a string literal, where nothing is
	// suggested.
	InsideString bool
}

type ClauseFieldSuggestion struct {
	Column string
	// InsertText is what a pick writes: the name, quoted when the dialect
	// requires it.
	InsertText string
}

type ClauseFieldOptions struct {
	Columns          []string
	Token            string
	QuotedIdentifier bool
	// Dialect is nil when the caller does not know the dialect; names are
	// then inserted as they are.
	Dialect *Dialect
}

// Operators whose right-hand side is a value: `deleted_at IS`, `name LIKE`,
// `id BETWEEN`. A list of columns there answers the wrong question, and the
// operand is the user's own text.
var (
	valueOperator = regexp.MustCompile(`(?i)(?:^|[\s(])(?:IS(?:\s+NOT)?|(?:NOT\s+)?(?:LIKE|ILIKE|IN|BETWEEN)|SIMILAR\s+TO|REGEXP|RLIKE|GLOB|MATCH)\s*$`)
	keywordBefore = regexp.MustCompile(`(?i)(?:^|\s)(?:AND|OR|NOT)$`)
	operatorEnd   = regexp.MustCompile("[,(<>=!~+\\-*/]$")
)

var connectors = []string{"AND", "OR"}

// Identifiers that resolve to themselves unquoted. Postgres folds bare names
// to lower case; MySQL keeps their case but allows no space, dash or dot.
var bareIdentifier = [2]*regexp.Regexp{
	Postgres: regexp.MustCompile(`^[a-z_][a-z0-9_$]*$`),
	MySQL:    regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`),
}

// IdentQuote is the byte a dialect quotes identifiers with.
func (d Dialect) IdentQuote() byte {
	if d == MySQL {
		return '`'
	}
	return '"'
}

// Where punctuation that opens a new token ends the run at the caret, so
// the `id` of `id = na` is not part of the token being typed. ORDER BY has
// no operators, so `-` and `>` stay inside names.
func tokenStop(kind ClauseKind) func(rune) bool {
	if kind == ClauseWhere {
		return func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(",()><=!&|", r) }
	}
	return func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(",()", r) }
}

type activeQuote struct {
	identifier   bool // else a string literal
	close        byte
	contentStart int
}

// activeQuoteAt is the quote the caret sits inside, if any. It scans from
// the start rather than searching backwards: whether a quote opens or closes
// depends on the state the previous ones left behind. A doubled quote
// escapes itself in both kinds, and a backslash escapes only inside a
// string. identQuote 0 means no identifier quote is known, so `"` opens a
// string.
func activeQuoteAt(value string, cursor int, identQuote byte) *activeQuote {
	var active *activeQuote
	for i := 0; i < cursor; i++ {
		c := value[i]
		if active == nil {
			switch {
			case identQuote != 0 && c == identQuote:
				active = &activeQuote{true, identQuote, i + 1}
			case c == '\'' || c == '"' && identQuote != '"':
				active = &activeQuote{false, c, i + 1}
			}
			continue
		}
		if !active.identifier && c == '\\' {
			i++
			continue
		}
		if c != active.close {
			continue
		}
		if i+1 < len(value) && value[i+1] == active.close && i+2 <= cursor {
			i++
			continue
		}
		active = nil
	}
	return active
}

// ClauseTargetAt reads the fragment at the caret and returns what a pick
// would replace. selectionEnd is the caret itself when nothing is selected.
//
// A selection wins over the run under the caret: having selected text is the
// user saying exactly what the pick goes over. Inside a quoted identifier
// only its content is replaced, so quoting the user already typed survives.
// identQuote is 0 when unknown.
func ClauseTargetAt(kind ClauseKind, value string, caret, selectionEnd int, identQuote byte) ClauseTarget {
	start := min(max(caret, 0), len(value))
	end := min(max(selectionEnd, start), len(value))
	var quote *activeQuote
	if kind == ClauseWhere {
		quote = activeQuoteAt(value, start, identQuote)
	}

	if quote != nil && !quote.identifier {
		return ClauseTarget{Value: value, From: start, To: end, InsideString: true}
	}

	if quote != nil {
		selected := start != end
		closeAt := -1
		if !selected {
			if j := strings.IndexByte(value[start:], quote.close); j >= 0 {
				closeAt = start + j
			}
		}
		t := ClauseTarget{Value: value, QuotedIdentifier: true, From: quote.contentStart, To: end}
		t.Token = value[quote.contentStart:start]
		if selected {
			t.Token, t.From = value[start:end], start
		} else if closeAt >= 0 {
			t.To = closeAt
		}
		return t
	}

	if start != end {
		return ClauseTarget{Value: value, Token: value[start:end], From: start, To: end}
	}

	// The run under the caret extends forward too: the arrow keys can park
	// the caret inside a name, and a pick then replaces the whole name, not
	// a tail.
	stop := tokenStop(kind)
	before := trailingRun(value[:start], stop)
	after := leadingRun(value[start:], stop)
	return ClauseTarget{Value: value, Token: before, From: start - len(before), To: start + len(after)}
}

func trailingRun(s string, stop func(rune) bool) string {
	i := len(s)
	for i > 0 {
		r, n := utf8.DecodeLastRuneInString(s[:i])
		if stop(r) {
			break
		}
		i -= n
	}
	return s[i:]
}

func leadingRun(s string, stop func(rune) bool) string {
	i := 0
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		if stop(r) {
			break
		}
		i += n
	}
	return s[:i]
}

// ClauseRoleOf is what the fragment at the caret wants: a field, the
// connector that continues an expression, or nothing at all. ORDER BY is
// always a field list: it has no connectors and no values.
func ClauseRoleOf(kind ClauseKind, t ClauseTarget) ClauseRole {
	if kind != ClauseWhere {
		return RoleField
	}
	if t.InsideString {
		return RoleNone
	}
	if t.QuotedIdentifier {
		return RoleField
	}
	prefix := strings.TrimRightFunc(t.Value[:t.From], unicode.IsSpace)
	if valueOperator.MatchString(prefix) {
		return RoleNone
	}
	if prefix == "" || keywordBefore.MatchString(prefix) || operatorEnd.MatchString(prefix) {
		return RoleField
	}
	return RoleConnector
}

// ClauseConnectors are the connectors the typed token is a prefix of, exact
// match excluded.
func ClauseConnectors(token string) []string {
	lower := strings.ToLower(token)
	out := []string{}
	for _, k := range connectors {
		if kl := strings.ToLower(k); strings.HasPrefix(kl, lower) && kl != lower {
			out = append(out, k)
		}
	}
	return out
}

// ClauseFieldSuggestions are the columns for the token at the caret, best
// match first.
//
// A token no column matches gets an empty list rather than every column. A
// number or a literal is the operand the user is in the middle of writing,
// and a full-list fallback there is what made Enter, the keystroke that
// applies the clause, look like it had rewritten the value. A token that IS
// a column ends the list for the same reason: the name is complete, and what
// follows is an operator.
func ClauseFieldSuggestions(o ClauseFieldOptions) []ClauseFieldSuggestion {
	lower := strings.ToLower(o.Token)
	if lower != "" {
		for _, c := range o.Columns {
			if strings.ToLower(c) == lower {
				return []ClauseFieldSuggestion{}
			}
		}
	}
	type scored struct {
		column string
		score  int
	}
	var hits []scored
	for _, c := range o.Columns {
		score := 0
		if lower != "" {
			m, ok := rank.MatchFuzzy(lower, c)
			if !ok {
				continue
			}
			score = m.Score
		}
		hits = append(hits, scored{c, score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score < hits[j].score })
	out := make([]ClauseFieldSuggestion, 0, len(hits))
	for _, h := range hits {
		// Inside quotes the name is already delimited, so the quotes
		// survive whatever the dialect would have added.
		text := h.column
		if !o.QuotedIdentifier {
			text = ClauseInsertText(h.column, o.Dialect)
		}
		out = append(out, ClauseFieldSuggestion{h.column, text})
	}
	return out
}

// ClauseInsertText is the name as a pick should write it: quoted only when
// the dialect needs it, so a readable fragment stays readable. A bare
// identifier spelled as a keyword parses as the keyword, so the word list
// decides the half of the rule the pattern cannot.
func ClauseInsertText(column string, d *Dialect) string {
	if d == nil {
		return column
	}
	if bareIdentifier[*d].MatchString(column) && !reserved[*d]()[strings.ToLower(column)] {
		return column
	}
	q := string(d.IdentQuote())
	return q + strings.ReplaceAll(column, q, q+q) + q
}

type ClausePick struct {
	Value string
	// Caret is where the caret belongs after the pick, ready for the
	// operator.
	Caret int
}

type ClausePickOptions struct {
	Kind         ClauseKind
	Value        string
	Caret        int
	SelectionEnd int // equal to Caret when nothing is selected
	InsertText   string
	IdentQuote   byte
}

// ClausePickAt is the fragment after a pick, and where the caret lands. A
// pick that lands past the end of the fragment writes the space the operator
// will follow; one that replaces a name being typed leaves the text around
// it alone, and one inside a quoted identifier keeps the quotes the user
// typed rather than adding the dialect's own.
func ClausePickAt(o ClausePickOptions) ClausePick {
	t := ClauseTargetAt(o.Kind, o.Value, o.Caret, o.SelectionEnd, o.IdentQuote)
	suffix := ""
	if t.Token == "" && !t.QuotedIdentifier && !t.InsideString && t.From >= len(o.Value) {
		suffix = " "
	}
	return ClausePick{
		Value: o.Value[:t.From] + o.InsertText + suffix + o.Value[t.To:],
		Caret: t.From + len(o.InsertText) + len(suffix),
	}
}
