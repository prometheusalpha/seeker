package redis

import (
	"errors"
	"strings"
)

// readOnly is an explicit allowlist, so a command from a newer Redis, a
// module or a proxy is a write until someone reviews it. Whatever blocks,
// streams or scans the whole keyspace (KEYS, MONITOR, SUBSCRIBE, FLUSH*,
// DEBUG, SHUTDOWN) is left out on purpose, and so are the reads that can block
// (XREAD, WAIT, WAITAOF): the console shares its connection with the key
// browser, so a blocked read would freeze the tab. The list is recall's.
var readOnly = setOf(`BITCOUNT BITFIELD_RO BITPOS COMMAND DBSIZE DUMP ECHO EXISTS EXPIRETIME GEODIST GEOHASH GEOPOS
GEORADIUS_RO GEORADIUSBYMEMBER_RO GEOSEARCH GET GETBIT GETRANGE HEXISTS HGET HGETALL HKEYS HLEN HMGET
HRANDFIELD HSCAN HSTRLEN HPTTL HTTL HVALS INFO LASTSAVE LCS LINDEX LLEN LPOS LRANGE MGET OBJECT
PEXPIRETIME PFCOUNT PING PTTL PUBSUB RANDOMKEY ROLE SCAN SCARD SDIFF SINTER SINTERCARD SISMEMBER
SMEMBERS SMISMEMBER SORT_RO SRANDMEMBER SSCAN STRLEN SUNION TIME TTL TYPE XINFO XLEN
XPENDING XRANGE XREVRANGE ZCARD ZCOUNT ZDIFF ZINTER ZINTERCARD ZLEXCOUNT ZMSCORE ZRANDMEMBER
ZRANGE ZRANGEBYLEX ZRANGEBYSCORE ZRANK ZREVRANGE ZREVRANGEBYLEX ZREVRANGEBYSCORE ZREVRANK ZSCAN ZSCORE
ZUNION BF.CARD BF.EXISTS BF.INFO BF.MEXISTS CF.COUNT CF.EXISTS CF.INFO CF.MEXISTS CMS.INFO CMS.QUERY
FT._LIST FT.AGGREGATE FT.DICTDUMP FT.EXPLAIN FT.EXPLAINCLI FT.INFO FT.PROFILE FT.SEARCH FT.SPELLCHECK
FT.SYNDUMP FT.TAGVALS GRAPH.RO_QUERY JSON.ARRINDEX JSON.ARRLEN JSON.GET JSON.MGET JSON.OBJKEYS
JSON.OBJLEN JSON.RESP JSON.STRLEN JSON.TYPE TDIGEST.BYRANK TDIGEST.BYREVRANK TDIGEST.CDF TDIGEST.INFO
TDIGEST.MAX TDIGEST.MIN TDIGEST.QUANTILE TDIGEST.RANK TDIGEST.REVRANK TDIGEST.TRIMMED_MEAN TOPK.INFO
TOPK.LIST TOPK.QUERY TS.GET TS.INFO TS.MGET TS.MRANGE TS.QUERYINDEX TS.RANGE`)

// hijacking commands take over the connection; the console cannot serve them.
var hijacking = setOf("MONITOR SUBSCRIBE PSUBSCRIBE SSUBSCRIBE SYNC PSYNC")

func setOf(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

// IsRead reports a command known to be read-only, which the console runs at
// once; every other command is a write and asks first.
func IsRead(name string) bool { return readOnly[strings.ToUpper(name)] }

// IsHijacking reports a command that would take over the connection.
func IsHijacking(name string) bool { return hijacking[strings.ToUpper(name)] }

// ParseCommandLine splits a console line like redis-cli: whitespace
// separates, quotes group, backslash escapes (\n \r \t, anything else
// literal), a trailing `;` is dropped. An unterminated quote or an empty line
// is an error.
func ParseCommandLine(text string) ([]string, error) {
	src := strings.TrimRight(strings.TrimRight(text, " \t\r\n"), ";")
	var (
		argv     []string
		cur      strings.Builder
		quote    rune
		escaping bool
		started  bool
	)
	for _, ch := range src {
		switch {
		case escaping:
			started = true
			switch ch {
			case 'n':
				cur.WriteByte('\n')
			case 'r':
				cur.WriteByte('\r')
			case 't':
				cur.WriteByte('\t')
			default:
				cur.WriteRune(ch)
			}
			escaping = false
		case ch == '\\':
			started, escaping = true, true
		case quote != 0:
			if ch == quote {
				quote = 0
			} else {
				cur.WriteRune(ch)
			}
		case ch == '"' || ch == '\'':
			started, quote = true, ch
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			if started {
				argv = append(argv, cur.String())
			}
			cur.Reset()
			started = false
		default:
			started = true
			cur.WriteRune(ch)
		}
	}
	if escaping {
		cur.WriteByte('\\')
	}
	if quote != 0 {
		return nil, errors.New("Redis command has an unterminated quote")
	}
	if started {
		argv = append(argv, cur.String())
	}
	if len(argv) == 0 || argv[0] == "" {
		return nil, errors.New("Redis command is empty")
	}
	return argv, nil
}
