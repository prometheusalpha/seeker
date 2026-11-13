package rank

import "unicode"

// Kind is the match quality, best first.
type Kind int

const (
	Exact Kind = iota
	Prefix
	WordPrefix
	Substring
	Fuzzy
)

// Match is a scored hit. Lower Score is better: it is the tier base plus a
// length penalty, with tier bases 100 apart and the penalty capped at 99, so
// a better tier outranks a worse one however short or long the text is, and
// inside a tier the shorter text wins (`users` beats `users_archive_2019`
// for the query `users`).
type Match struct {
	Kind  Kind
	Score int
	// Indices are the matched positions as rune offsets into the text,
	// ascending, for highlighting.
	Indices []int
}

var tierBase = [...]int{Exact: 0, Prefix: 100, WordPrefix: 200, Substring: 300, Fuzzy: 400}

const maxLengthPenalty = 99

// MatchFuzzy scores query against text, case-insensitively. An empty query
// matches nothing: the palette shows an empty state rather than an unranked
// dump of every table on the server.
//
// Offsets are runes where recall's were UTF-16 units; they differ only past
// the BMP.
func MatchFuzzy(query, text string) (Match, bool) {
	if query == "" || text == "" {
		return Match{}, false
	}
	q, t := []rune(query), []rune(text)
	lq, lt := lower(q), lower(t)
	score := func(k Kind) int { return tierBase[k] + min(max(len(lt)-len(lq), 0), maxLengthPenalty) }

	if equal(lt, lq) {
		return Match{Exact, score(Exact), span(0, len(t))}, true
	}
	if hasPrefix(lt, lq) {
		return Match{Prefix, score(Prefix), span(0, len(lq))}, true
	}
	// Earliest occurrence that starts on a word boundary, e.g. `login` in
	// `user_login_table`, which beats the same word buried mid-identifier.
	first := index(lt, lq, 0)
	for i := first; i >= 0; i = index(lt, lq, i+1) {
		if isWordStart(t, i) {
			return Match{WordPrefix, score(WordPrefix), span(i, len(lq))}, true
		}
	}
	if first >= 0 {
		return Match{Substring, score(Substring), span(first, len(lq))}, true
	}
	// Leftmost greedy subsequence: `ue` matches `users` at 0 and 2.
	var idx []int
	for i, c := 0, 0; i < len(lt); i++ {
		if lt[i] != lq[c] {
			continue
		}
		idx = append(idx, i)
		if c++; c == len(lq) {
			return Match{Fuzzy, score(Fuzzy), idx}, true
		}
	}
	return Match{}, false
}

// isWordStart: the text starts there, the previous character is a
// separator, or it is a camelCase hump (lower or digit followed by upper).
func isWordStart(t []rune, i int) bool {
	if i == 0 {
		return true
	}
	p, c := t[i-1], t[i]
	switch p {
	case ' ', '_', '-', '.', '/', '\\':
		return true
	}
	return (p >= 'a' && p <= 'z' || p >= '0' && p <= '9') && c >= 'A' && c <= 'Z'
}

// lower folds rune by rune so offsets stay aligned with the text, which
// strings.ToLower does not guarantee.
func lower(r []rune) []rune {
	out := make([]rune, len(r))
	for i, c := range r {
		out[i] = unicode.ToLower(c)
	}
	return out
}

func equal(a, b []rune) bool { return len(a) == len(b) && hasPrefix(a, b) }

func hasPrefix(a, b []rune) bool {
	if len(b) > len(a) {
		return false
	}
	for i := range b {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func index(hay, needle []rune, from int) int {
	for i := from; i+len(needle) <= len(hay); i++ {
		if hasPrefix(hay[i:], needle) {
			return i
		}
	}
	return -1
}

func span(from, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = from + i
	}
	return out
}
