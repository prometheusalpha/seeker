package files

import (
	"regexp"
	"strings"
	"sync"
)

// Glob is deliberately minimal: `*`, `?` and one level of `{a,b}`, which
// covers `*.sql`, `*.{sql,md}` and a bare `*`. Anything else is literal, and
// matching ignores case because the filesystems here do.
var (
	globMu    sync.Mutex
	globCache = map[string]*regexp.Regexp{}
)

func compileGlob(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("(?i)^")
	for i := 0; i < len(pattern); i++ {
		switch ch := pattern[i]; ch {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '{':
			end := strings.IndexByte(pattern[i:], '}')
			if end < 0 {
				b.WriteString(`\{`)
				continue
			}
			alts := strings.Split(pattern[i+1:i+end], ",")
			for j, alt := range alts {
				alts[j] = regexp.QuoteMeta(alt)
			}
			b.WriteString("(?:" + strings.Join(alts, "|") + ")")
			i += end
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// Match reports a file name that fits the filter; an empty filter is `*`.
func Match(name, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		filter = "*"
	}
	globMu.Lock()
	re, ok := globCache[filter]
	if !ok {
		// The user edits one filter at a time; the cache only has to
		// stop a long typing session from growing it without limit.
		if len(globCache) >= 32 {
			clear(globCache)
		}
		re = compileGlob(filter)
		globCache[filter] = re
	}
	globMu.Unlock()
	return re.MatchString(name)
}
