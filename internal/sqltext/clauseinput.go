package sqltext

import "strings"

// ClauseCompletions are the whole fragments a pick would leave in a filter
// field, for a field whose text is edited at its end: the token last typed
// is replaced by a column, or by AND or OR when the fragment waits for a
// connector. A widget that offers a field's text as a list gets each as
// the text the field would then hold.
func ClauseCompletions(kind ClauseKind, value string, columns []string, d Dialect) []string {
	t := ClauseTargetAt(kind, value, len(value), len(value), d.IdentQuote())
	if t.InsideString {
		return nil
	}
	var out []string
	switch ClauseRoleOf(kind, t) {
	case RoleField:
		for _, s := range ClauseFieldSuggestions(ClauseFieldOptions{
			Columns: columns, Token: t.Token, QuotedIdentifier: t.QuotedIdentifier, Dialect: &d,
		}) {
			out = append(out, ClausePickAt(ClausePickOptions{
				Kind: kind, Value: value, Caret: len(value), SelectionEnd: len(value),
				InsertText: s.InsertText, IdentQuote: d.IdentQuote(),
			}).Value)
		}
	case RoleConnector:
		for _, c := range ClauseConnectors(t.Token) {
			out = append(out, strings.TrimRight(value[:t.From], " ")+" "+c+" ")
		}
	}
	return out
}
