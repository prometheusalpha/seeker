package sqltext

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// What editor completion needs to know of the statement around the caret.
// A scanner, not a parser (recall's SqlStatementScope.ts and the clause
// rules of SqlSchemaCompletionSource.ts): it answers "which tables does
// this statement name, under which aliases" well enough to offer columns,
// and stays silent when the shape is odd. A wrong alias would offer the
// columns of a table the statement never mentioned, which is worse than
// offering none.

// BlankOut returns text with comments and quoted runs replaced by spaces,
// newlines kept, so a keyword inside a literal cannot pass for a clause and
// every offset stays where it was. An unterminated run blanks to the end.
// Quoted identifiers go with the strings: a table called "My Table" is
// invisible here, and an absent suggestion beats a rejected one.
func BlankOut(text string) string {
	out := []byte(text)
	blank := func(from, to int) {
		for j := from; j < to; j++ {
			if out[j] != '\n' {
				out[j] = ' '
			}
		}
	}
	for i := 0; i < len(text); {
		end := -1
		switch {
		case strings.HasPrefix(text[i:], "--"):
			end = len(text)
			if n := strings.IndexByte(text[i+2:], '\n'); n >= 0 {
				end = i + 2 + n
			}
		case strings.HasPrefix(text[i:], "/*"):
			end = len(text)
			if n := strings.Index(text[i+2:], "*/"); n >= 0 {
				end = i + 2 + n + 2
			}
		case text[i] == '\'' || text[i] == '"' || text[i] == '`':
			end = len(text)
			if n := strings.IndexByte(text[i+1:], text[i]); n >= 0 {
				end = i + 1 + n + 1
			}
		}
		if end > 0 {
			blank(i, end)
			i = end
			continue
		}
		i++
	}
	return string(out)
}

// StatementBounds is the statement around pos: from the previous `;` to
// the next. A `;` inside a literal ends it early, which only makes the scan
// shorter, as the hint it feeds can afford.
func StatementBounds(doc string, pos int) (start, end int) {
	pos = max(0, min(pos, len(doc)))
	start = strings.LastIndexByte(doc[:pos], ';') + 1
	if n := strings.IndexByte(doc[pos:], ';'); n >= 0 {
		return start, pos + n
	}
	return start, len(doc)
}

func isIdentRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// word is an identifier run of a blanked text.
type word struct {
	text       string
	start, end int
}

// words lists the identifier runs, and says for each whether a `.` stands
// right before it (a qualified name, never a keyword).
func words(cleaned string) (ws []word, dotBefore []bool) {
	for i := 0; i < len(cleaned); {
		r, n := utf8.DecodeRuneInString(cleaned[i:])
		if !isIdentRune(r) {
			i += n
			continue
		}
		j := i
		for j < len(cleaned) {
			r, n := utf8.DecodeRuneInString(cleaned[j:])
			if !isIdentRune(r) {
				break
			}
			j += n
		}
		ws = append(ws, word{cleaned[i:j], i, j})
		dotBefore = append(dotBefore, i > 0 && cleaned[i-1] == '.')
		i = j
	}
	return
}

// notAlias are the words that can follow a table name without being its
// alias.
var notAlias = wordSet("on using where group order having limit offset fetch for union except intersect values returning set window select insert update delete with and or not is in like between case when then else end join inner left right full outer cross natural lateral tablesample partition conflict do")

// TableRef is one table a statement names: as written without its dotted
// prefix, and its alias when it has one.
type TableRef struct {
	Name, Alias string
}

// StatementScope is what a statement declares, for completion.
type StatementScope struct {
	// Tables are the tables named, last path segment only, in order.
	Tables []string
	// Aliases maps a lower-cased alias or table name to its table.
	Aliases map[string]string
	// CTEs are the names `WITH … AS (` declares: not tables, still
	// completable.
	CTEs []string
}

var cteName = regexp.MustCompile(`([\p{L}\p{N}_$]+)\s+(?i:AS)\s*\(`)

// tableRefs reads the references of blanked text: a FROM, JOIN, INTO,
// UPDATE or TABLE, a dotted path, and an alias. A path the text leaves
// open (`FROM public.`) is dropped rather than read as a table `public`.
func tableRefs(cleaned string) []TableRef {
	ws, dotBefore := words(cleaned)
	var refs []TableRef
	for i := 0; i < len(ws); i++ {
		if dotBefore[i] {
			continue
		}
		switch strings.ToLower(ws[i].text) {
		case "from", "join", "into", "update", "table":
		default:
			continue
		}
		// The path: name(.name)*, whitespace only between the keyword and it.
		at := ws[i].end
		j := i + 1
		if j >= len(ws) || strings.TrimSpace(cleaned[at:ws[j].start]) != "" || ws[j].start == at {
			continue
		}
		for j+1 < len(ws) && cleaned[ws[j].end:ws[j+1].start] == "." {
			j++
		}
		after := ws[j].end
		if after < len(cleaned) && cleaned[after] == '.' {
			continue
		}
		ref := TableRef{Name: ws[j].text}
		k := j + 1
		if k < len(ws) && strings.TrimSpace(cleaned[after:ws[k].start]) == "" && ws[k].start > after {
			if strings.EqualFold(ws[k].text, "as") && k+1 < len(ws) &&
				strings.TrimSpace(cleaned[ws[k].end:ws[k+1].start]) == "" && ws[k+1].start > ws[k].end {
				k++
			}
			if !notAlias[strings.ToLower(ws[k].text)] && !dotBefore[k] {
				ref.Alias = ws[k].text
			}
		}
		refs = append(refs, ref)
		i = j
	}
	return refs
}

// ParseStatementScope reads the tables, aliases and CTE names of one
// statement. Names are lower-cased for lookup only.
func ParseStatementScope(statement string) StatementScope {
	cleaned := BlankOut(statement)
	sc := StatementScope{Aliases: map[string]string{}}
	seen := map[string]bool{}
	for _, ref := range tableRefs(cleaned) {
		key := strings.ToLower(ref.Name)
		if !seen[key] {
			seen[key] = true
			sc.Tables = append(sc.Tables, ref.Name)
		}
		// The table's own name is a qualifier too: `users.` needs no alias.
		if _, ok := sc.Aliases[key]; !ok {
			sc.Aliases[key] = ref.Name
		}
		if ref.Alias != "" {
			alias := strings.ToLower(ref.Alias)
			if _, ok := sc.Aliases[alias]; !ok {
				sc.Aliases[alias] = ref.Name
			}
		}
	}
	if withRe.MatchString(cleaned) {
		declared := map[string]bool{}
		for _, m := range cteName.FindAllStringSubmatch(cleaned, -1) {
			key := strings.ToLower(m[1])
			if declared[key] || seen[key] {
				continue
			}
			declared[key] = true
			sc.CTEs = append(sc.CTEs, m[1])
		}
	}
	return sc
}

var withRe = regexp.MustCompile(`(?i)\bWITH\b`)

// ReferencedTables lists every table named anywhere in doc, for warming
// the column cache of a whole tab at once.
func ReferencedTables(doc string) []string {
	var out []string
	seen := map[string]bool{}
	for _, ref := range tableRefs(BlankOut(doc)) {
		key := strings.ToLower(ref.Name)
		if !seen[key] {
			seen[key] = true
			out = append(out, ref.Name)
		}
	}
	return out
}

// SchemaClause says what the caret wants.
type SchemaClause int

const (
	ClauseNone SchemaClause = iota
	ClauseTables
	ClauseColumns
)

// SchemaClauseFor reads the blanked statement up to the caret: the nearest
// keyword wins, which makes `SELECT a FROM t WHERE b` a column clause. None
// where no suggestion helps: tables right after SELECT would bury the
// columns that are wanted.
func SchemaClauseFor(before string) SchemaClause {
	ws, dotBefore := words(before)
	tableAt, columnAt := -1, -1
	selects := false
	for i, w := range ws {
		if dotBefore[i] {
			continue
		}
		lw := strings.ToLower(w.text)
		switch lw {
		case "select":
			selects = true
		case "from", "join", "into", "update", "table":
			tableAt = w.end
		case "where", "on", "having", "set":
			columnAt = w.end
		case "group", "order":
			if i+1 < len(ws) && strings.EqualFold(ws[i+1].text, "by") &&
				strings.TrimSpace(before[w.end:ws[i+1].start]) == "" && ws[i+1].start > w.end {
				columnAt = ws[i+1].end
			}
		}
	}
	switch {
	case tableAt < 0 && columnAt < 0:
		if selects {
			return ClauseColumns
		}
		return ClauseNone
	case tableAt < 0:
		return ClauseColumns
	case columnAt < 0:
		return ClauseTables
	case columnAt > tableAt:
		return ClauseColumns
	}
	return ClauseTables
}

// OpenClauseTail: the caret is past a keyword (or a comma) with no letter
// of the next word typed yet, as `FROM |`, which still wants its list.
var OpenClauseTail = regexp.MustCompile(`(?i)(?:\b(?:FROM|JOIN|INTO|UPDATE|TABLE|WHERE|ON|SET|HAVING|SELECT|VALUES|USING|RETURNING|GROUP\s+BY|ORDER\s+BY)|,)\s*$`)

// qualifiedTarget finds `qualifier.partial` at the end of before: the
// quoted or bare qualifier, and what is typed after the dot.
var qualifiedTarget = regexp.MustCompile(`(?:"([^"]+)"|([\p{L}\p{N}_$]+))\s*\.\s*([\p{L}\p{N}_$]*)$`)

// QualifiedAt reports the qualifier and partial word that end before, when
// it ends in one.
func QualifiedAt(before string) (qualifier, partial string, ok bool) {
	m := qualifiedTarget.FindStringSubmatch(before)
	if m == nil {
		return "", "", false
	}
	q := m[1]
	if q == "" {
		q = m[2]
	}
	return q, m[3], true
}

// WordBefore is the identifier run that ends at pos in doc.
func WordBefore(doc string, pos int) (start int) {
	pos = max(0, min(pos, len(doc)))
	start = pos
	for start > 0 {
		r, n := utf8.DecodeLastRuneInString(doc[:start])
		if !isIdentRune(r) {
			break
		}
		start -= n
	}
	return start
}
