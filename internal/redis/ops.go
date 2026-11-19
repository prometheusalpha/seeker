package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	// StringReadCap is how much of a string is read; a longer one is shown as
	// its prefix, read-only.
	StringReadCap = 1024 * 1024
	hashPageCount = 200
	listPageCount = 200
	deleteChunk   = 500
)

// Database is one logical database of the server.
type Database struct {
	DB int
	// Keys is from INFO keyspace; 0 for one the server lists as empty.
	Keys int
}

// KeyInfo is a key as the list shows it.
type KeyInfo struct {
	Key Bytes
	// Type is TYPE as the server reports it: "string", "hash", "ReJSON-RL"…
	Type string
	// TTL is in seconds: -1 for a key that never expires, -2 for one gone.
	TTL int64
}

// ScanPage is one SCAN batch.
type ScanPage struct {
	// Cursor is "0" when the scan is complete.
	Cursor string
	Keys   []KeyInfo
	// Total is DBSIZE, sent on the first page only; -1 on later ones.
	Total int64
}

// HashEntry is one field of a hash.
type HashEntry struct{ Field, Value Bytes }

// HashPage is one HSCAN batch; Cursor is "0" when every field was read.
type HashPage struct {
	Entries []HashEntry
	Cursor  string
}

// SetPage is one SSCAN batch; Cursor is "0" when every member was read.
type SetPage struct {
	Members []Bytes
	Cursor  string
}

// ListPage is one LRANGE batch; More says a full batch came back, so there
// may be elements past it.
type ListPage struct {
	Items []Bytes
	More  bool
}

// Kind is what a key holds, as far as this build can show it.
type Kind string

const (
	KindString      Kind = "string"
	KindHash        Kind = "hash"
	KindSet         Kind = "set"
	KindList        Kind = "list"
	KindUnsupported Kind = "unsupported"
)

// Value is a key's value. A type this build cannot show is Unsupported, and
// the console still reaches it.
type Value struct {
	Kind Kind
	TTL  int64

	// String: Str is what was read, ByteLen the full STRLEN, Truncated that
	// only a prefix was (the value is then read-only).
	Str       Bytes
	ByteLen   int64
	Truncated bool

	// Hash: HashLen is HLEN and First the first page.
	HashLen int64
	First   HashPage

	// Set: SetLen is SCARD and FirstSet the first page.
	SetLen   int64
	FirstSet SetPage

	// List: ListLen is LLEN and FirstList the first page.
	ListLen   int64
	FirstList ListPage

	// RedisType is the server's name for an unsupported type.
	RedisType string
}

// TTLMode says what a write does to the key's expiry. Keep is the default
// for edits: a cache entry that loses its TTL on save quietly becomes
// permanent.
type TTLMode uint8

const (
	TTLKeep TTLMode = iota
	TTLPersist
	TTLSeconds
)

type TTLChange struct {
	Mode    TTLMode
	Seconds int64
}

func str(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

func num(v any) int64 {
	switch v := v.(type) {
	case int64:
		return v
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return 0
}

// do runs a command with its arguments as given: Go strings carry any bytes.
func do(ctx context.Context, c interface {
	Do(context.Context, ...any) *goredis.Cmd
}, args ...any) *goredis.Cmd {
	return c.Do(ctx, args...)
}

// ScanKeys reads one SCAN batch, each key with its TYPE and TTL.
func ScanKeys(ctx context.Context, c *goredis.Client, cursor, pattern string, count int) (ScanPage, error) {
	if pattern == "" {
		pattern = "*"
	}
	res, err := do(ctx, c, "SCAN", cursor, "MATCH", pattern, "COUNT", count).Slice()
	if err != nil {
		return ScanPage{}, err
	}
	if len(res) != 2 {
		return ScanPage{}, errors.New("unexpected SCAN reply")
	}
	page := ScanPage{Cursor: str(res[0]), Total: -1}
	names, _ := res[1].([]any)
	if len(names) > 0 {
		pipe := c.Pipeline()
		cmds := make([]*goredis.Cmd, 0, 2*len(names))
		for _, k := range names {
			cmds = append(cmds, do(ctx, pipe, "TYPE", str(k)), do(ctx, pipe, "TTL", str(k)))
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return ScanPage{}, err
		}
		for i, k := range names {
			typ, _ := cmds[2*i].Result()
			ttl, _ := cmds[2*i+1].Result()
			t := str(typ)
			if t == "" {
				t = "none"
			}
			page.Keys = append(page.Keys, KeyInfo{Key: NewBytes([]byte(str(k))), Type: t, TTL: num(ttl)})
		}
	}
	if cursor == "0" {
		n, err := c.DBSize(ctx).Result()
		if err != nil {
			return ScanPage{}, err
		}
		page.Total = n
	}
	return page, nil
}

func typeAndTTL(ctx context.Context, c *goredis.Client, key string) (string, int64, error) {
	pipe := c.Pipeline()
	t, l := do(ctx, pipe, "TYPE", key), do(ctx, pipe, "TTL", key)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", 0, err
	}
	tv, _ := t.Result()
	lv, _ := l.Result()
	return str(tv), num(lv), nil
}

// GetValue reads a key; a nil Value means it no longer exists.
func GetValue(ctx context.Context, c *goredis.Client, key Bytes) (*Value, error) {
	k := string(key.Data)
	typ, ttl, err := typeAndTTL(ctx, c, k)
	if err != nil {
		return nil, err
	}
	switch typ {
	case "none":
		return nil, nil
	case "string":
		n, err := do(ctx, c, "STRLEN", k).Int64()
		if err != nil {
			return nil, err
		}
		raw, err := do(ctx, c, "GETRANGE", k, 0, StringReadCap-1).Text()
		if err != nil && !errors.Is(err, goredis.Nil) {
			return nil, err
		}
		return &Value{Kind: KindString, TTL: ttl, Str: NewBytes([]byte(raw)), ByteLen: n, Truncated: n > StringReadCap}, nil
	case "hash":
		n, err := do(ctx, c, "HLEN", k).Int64()
		if err != nil {
			return nil, err
		}
		first, err := HashScan(ctx, c, key, "0")
		if err != nil {
			return nil, err
		}
		return &Value{Kind: KindHash, TTL: ttl, HashLen: n, First: first}, nil
	case "set":
		n, err := do(ctx, c, "SCARD", k).Int64()
		if err != nil {
			return nil, err
		}
		first, err := SetScan(ctx, c, key, "0")
		if err != nil {
			return nil, err
		}
		return &Value{Kind: KindSet, TTL: ttl, SetLen: n, FirstSet: first}, nil
	case "list":
		n, err := do(ctx, c, "LLEN", k).Int64()
		if err != nil {
			return nil, err
		}
		first, err := ListRange(ctx, c, key, 0)
		if err != nil {
			return nil, err
		}
		return &Value{Kind: KindList, TTL: ttl, ListLen: n, FirstList: first}, nil
	}
	return &Value{Kind: KindUnsupported, TTL: ttl, RedisType: typ}, nil
}

// HashScan reads one HSCAN batch.
func HashScan(ctx context.Context, c *goredis.Client, key Bytes, cursor string) (HashPage, error) {
	res, err := do(ctx, c, "HSCAN", string(key.Data), cursor, "COUNT", hashPageCount).Slice()
	if err != nil {
		return HashPage{}, err
	}
	if len(res) != 2 {
		return HashPage{}, errors.New("unexpected HSCAN reply")
	}
	flat, _ := res[1].([]any)
	page := HashPage{Cursor: str(res[0])}
	for i := 0; i+1 < len(flat); i += 2 {
		page.Entries = append(page.Entries, HashEntry{
			Field: NewBytes([]byte(str(flat[i]))), Value: NewBytes([]byte(str(flat[i+1]))),
		})
	}
	return page, nil
}

// SetScan reads one SSCAN batch. A set has no order, and SSCAN may return a
// member twice across batches, which the caller drops.
func SetScan(ctx context.Context, c *goredis.Client, key Bytes, cursor string) (SetPage, error) {
	res, err := do(ctx, c, "SSCAN", string(key.Data), cursor, "COUNT", hashPageCount).Slice()
	if err != nil {
		return SetPage{}, err
	}
	if len(res) != 2 {
		return SetPage{}, errors.New("unexpected SSCAN reply")
	}
	flat, _ := res[1].([]any)
	page := SetPage{Cursor: str(res[0])}
	for _, m := range flat {
		page.Members = append(page.Members, NewBytes([]byte(str(m))))
	}
	return page, nil
}

// ListRange reads one page of a list from index start.
func ListRange(ctx context.Context, c *goredis.Client, key Bytes, start int64) (ListPage, error) {
	res, err := do(ctx, c, "LRANGE", string(key.Data), start, start+listPageCount-1).Slice()
	if err != nil {
		return ListPage{}, err
	}
	page := ListPage{More: len(res) == listPageCount}
	for _, r := range res {
		page.Items = append(page.Items, NewBytes([]byte(str(r))))
	}
	return page, nil
}

// SetString creates or overwrites a string. OnlyIfAbsent is for the new-key
// form, so creating never silently replaces a key; it then reports false
// instead of writing.
//
// Keep reads PTTL and applies it again in the same MULTI rather than using
// KEEPTTL, which is Redis 6.0 and later. A TTL changed between the read and
// the MULTI is the race this accepts.
func SetString(ctx context.Context, c *goredis.Client, key Bytes, value string, ttl TTLChange, onlyIfAbsent bool) (bool, error) {
	k := string(key.Data)
	args := []any{"SET", k, value}
	if ttl.Mode == TTLSeconds {
		args = append(args, "EX", ttl.Seconds)
	}
	if onlyIfAbsent {
		args = append(args, "NX")
	}
	if ttl.Mode == TTLSeconds || ttl.Mode == TTLPersist || onlyIfAbsent {
		err := do(ctx, c, args...).Err()
		if errors.Is(err, goredis.Nil) {
			return false, nil
		}
		return err == nil, err
	}
	pttl, err := do(ctx, c, "PTTL", k).Int64()
	if err != nil {
		return false, err
	}
	_, err = c.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		do(ctx, p, "SET", k, value)
		if pttl > 0 {
			do(ctx, p, "PEXPIRE", k, pttl)
		}
		return nil
	})
	return err == nil, err
}

// HashSet sets one field. PreviousField renames: the old field goes in the
// same transaction. OnlyIfAbsent refuses when the key already exists.
func HashSet(ctx context.Context, c *goredis.Client, key, field Bytes, value string, previous *Bytes, ttl TTLChange, onlyIfAbsent bool) (bool, error) {
	k := string(key.Data)
	if onlyIfAbsent {
		n, err := do(ctx, c, "EXISTS", k).Int64()
		if err != nil {
			return false, err
		}
		if n > 0 {
			return false, nil
		}
	}
	_, err := c.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		if previous != nil && string(previous.Data) != string(field.Data) {
			do(ctx, p, "HDEL", k, string(previous.Data))
		}
		do(ctx, p, "HSET", k, string(field.Data), value)
		switch ttl.Mode {
		case TTLPersist:
			do(ctx, p, "PERSIST", k)
		case TTLSeconds:
			do(ctx, p, "EXPIRE", k, ttl.Seconds)
		}
		return nil
	})
	return err == nil, err
}

// HashDelete removes one field.
func HashDelete(ctx context.Context, c *goredis.Client, key, field Bytes) error {
	return do(ctx, c, "HDEL", string(key.Data), string(field.Data)).Err()
}

// SetAdd adds one member. Previous replaces: the old member goes in the same
// transaction, since a set cannot change a member in place. OnlyIfAbsent
// refuses when the key already exists.
func SetAdd(ctx context.Context, c *goredis.Client, key, member Bytes, previous *Bytes, ttl TTLChange, onlyIfAbsent bool) (bool, error) {
	k := string(key.Data)
	if onlyIfAbsent {
		n, err := do(ctx, c, "EXISTS", k).Int64()
		if err != nil {
			return false, err
		}
		if n > 0 {
			return false, nil
		}
	}
	_, err := c.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		if previous != nil && string(previous.Data) != string(member.Data) {
			do(ctx, p, "SREM", k, string(previous.Data))
		}
		do(ctx, p, "SADD", k, string(member.Data))
		switch ttl.Mode {
		case TTLPersist:
			do(ctx, p, "PERSIST", k)
		case TTLSeconds:
			do(ctx, p, "EXPIRE", k, ttl.Seconds)
		}
		return nil
	})
	return err == nil, err
}

// SetRemove removes one member.
func SetRemove(ctx context.Context, c *goredis.Client, key, member Bytes) error {
	return do(ctx, c, "SREM", string(key.Data), string(member.Data)).Err()
}

// ListPush adds an element at the head or the tail. OnlyIfAbsent refuses
// when the key already exists.
func ListPush(ctx context.Context, c *goredis.Client, key Bytes, value string, head bool, ttl TTLChange, onlyIfAbsent bool) (bool, error) {
	k := string(key.Data)
	if onlyIfAbsent {
		n, err := do(ctx, c, "EXISTS", k).Int64()
		if err != nil {
			return false, err
		}
		if n > 0 {
			return false, nil
		}
	}
	cmd := "RPUSH"
	if head {
		cmd = "LPUSH"
	}
	_, err := c.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		do(ctx, p, cmd, k, value)
		switch ttl.Mode {
		case TTLPersist:
			do(ctx, p, "PERSIST", k)
		case TTLSeconds:
			do(ctx, p, "EXPIRE", k, ttl.Seconds)
		}
		return nil
	})
	return err == nil, err
}

// ErrListChanged is a list edit that found another element at its index
// than the one the pane showed: someone else changed the list since.
var ErrListChanged = errors.New("The list changed since it was read. Reload and try again.")

// ListSet replaces the element at index with value, provided it still is
// expected, which WATCH makes certain of up to the write.
func ListSet(ctx context.Context, c *goredis.Client, key Bytes, index int64, expected Bytes, value string) error {
	return listEdit(ctx, c, key, index, expected, func(p goredis.Pipeliner, k string) {
		do(ctx, p, "LSET", k, index, value)
	})
}

// ListRemoveAt removes the element at index, provided it still is expected.
// A list has no delete by index: the element is overwritten with a marker
// no one else writes, and the marker is removed, so a duplicate earlier in
// the list is not the one that goes.
func ListRemoveAt(ctx context.Context, c *goredis.Client, key Bytes, index int64, expected Bytes) error {
	var nonce [8]byte
	rand.Read(nonce[:])
	marker := "\x00seeker-del:" + hex.EncodeToString(nonce[:])
	return listEdit(ctx, c, key, index, expected, func(p goredis.Pipeliner, k string) {
		do(ctx, p, "LSET", k, index, marker)
		do(ctx, p, "LREM", k, 1, marker)
	})
}

func listEdit(ctx context.Context, c *goredis.Client, key Bytes, index int64, expected Bytes, write func(p goredis.Pipeliner, k string)) error {
	k := string(key.Data)
	err := c.Watch(ctx, func(tx *goredis.Tx) error {
		cur, err := tx.LIndex(ctx, k, index).Result()
		if errors.Is(err, goredis.Nil) || (err == nil && cur != string(expected.Data)) {
			return ErrListChanged
		}
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(p goredis.Pipeliner) error {
			write(p, k)
			return nil
		})
		return err
	}, k)
	if errors.Is(err, goredis.TxFailedErr) {
		return ErrListChanged
	}
	return err
}

// DeleteKeys removes keys with UNLINK, which frees memory off the server's
// main thread so a huge key does not stall it, and says how many went.
func DeleteKeys(ctx context.Context, c *goredis.Client, keys []Bytes) (int64, error) {
	var removed int64
	for i := 0; i < len(keys); i += deleteChunk {
		args := []any{"UNLINK"}
		for _, k := range keys[i:min(len(keys), i+deleteChunk)] {
			args = append(args, string(k.Data))
		}
		n, err := do(ctx, c, args...).Int64()
		if err != nil {
			return removed, err
		}
		removed += n
	}
	return removed, nil
}

// SetTTL persists the key or gives it a lifetime, and returns the TTL it has
// after (-1 for none).
func SetTTL(ctx context.Context, c *goredis.Client, key Bytes, ttl TTLChange) (int64, error) {
	k := string(key.Data)
	var err error
	switch ttl.Mode {
	case TTLPersist:
		err = do(ctx, c, "PERSIST", k).Err()
	case TTLSeconds:
		err = do(ctx, c, "EXPIRE", k, ttl.Seconds).Err()
	default:
		return 0, errors.New("a TTL change must persist or set seconds")
	}
	if err != nil {
		return 0, err
	}
	return do(ctx, c, "TTL", k).Int64()
}

var keyspaceRe = regexp.MustCompile(`(?m)^db(\d+):keys=(\d+)`)

// ListDatabases lists db0 to the server's `databases` setting (16 when CONFIG
// is renamed or denied, as on managed Redis), with the key counts INFO gives.
func ListDatabases(ctx context.Context, c *goredis.Client) ([]Database, error) {
	count := 16
	if res, err := do(ctx, c, "CONFIG", "GET", "databases").Slice(); err == nil && len(res) == 2 {
		if n, err := strconv.Atoi(str(res[1])); err == nil && n > 0 {
			count = n
		}
	}
	keys := map[int]int{}
	if info, err := c.Info(ctx, "keyspace").Result(); err == nil {
		for _, m := range keyspaceRe.FindAllStringSubmatch(info, -1) {
			db, _ := strconv.Atoi(m[1])
			n, _ := strconv.Atoi(m[2])
			keys[db] = n
		}
	}
	out := make([]Database, count)
	for i := range out {
		out[i] = Database{DB: i, Keys: keys[i]}
	}
	return out, nil
}

// ReplyKind tags a Reply.
type ReplyKind uint8

const (
	ReplyNil ReplyKind = iota
	ReplyInt
	ReplyBytes
	ReplyError
	ReplyArray
)

// Reply is a RESP reply, bulk strings as Bytes.
type Reply struct {
	Kind  ReplyKind
	Int   int64
	Bytes Bytes
	Err   string
	Items []Reply
}

func replyOf(v any) Reply {
	switch v := v.(type) {
	case nil:
		return Reply{}
	case int64:
		return Reply{Kind: ReplyInt, Int: v}
	case string:
		return Reply{Kind: ReplyBytes, Bytes: NewBytes([]byte(v))}
	case []byte:
		return Reply{Kind: ReplyBytes, Bytes: NewBytes(v)}
	case bool:
		if v {
			return Reply{Kind: ReplyInt, Int: 1}
		}
		return Reply{Kind: ReplyInt}
	case error:
		return Reply{Kind: ReplyError, Err: v.Error()}
	case []any:
		items := make([]Reply, len(v))
		for i, x := range v {
			items[i] = replyOf(x)
		}
		return Reply{Kind: ReplyArray, Items: items}
	}
	return Reply{Kind: ReplyBytes, Bytes: TextBytes(fmt.Sprint(v))}
}

// Status is how a console line ended.
type Status uint8

const (
	StatusOK Status = iota
	StatusError
	// StatusNeedsConfirmation: nothing was sent, as the command is not known
	// to be read-only and was not confirmed.
	StatusNeedsConfirmation
)

// Result is the outcome of one console line.
type Result struct {
	Status Status
	Reply  Reply
	// Message is the error for StatusError.
	Message string
	Elapsed time.Duration
}

// Execute runs one console line. A command outside the read-only allowlist
// comes back StatusNeedsConfirmation unless confirmed.
func Execute(ctx context.Context, c *goredis.Client, line string, confirmed bool) Result {
	argv, err := ParseCommandLine(line)
	if err != nil {
		return Result{Status: StatusError, Message: err.Error()}
	}
	name := argv[0]
	if IsHijacking(name) {
		return Result{Status: StatusError, Message: fmt.Sprintf("%s is not supported in the console: it takes over the connection", upper(name))}
	}
	if !confirmed && !IsRead(name) {
		return Result{Status: StatusNeedsConfirmation}
	}
	args := make([]any, len(argv))
	for i, a := range argv {
		args[i] = a
	}
	start := time.Now()
	v, err := c.Do(ctx, args...).Result()
	took := time.Since(start).Round(time.Millisecond)
	if errors.Is(err, goredis.Nil) {
		return Result{Status: StatusOK, Elapsed: took}
	}
	if err != nil {
		return Result{Status: StatusError, Message: err.Error(), Elapsed: took}
	}
	return Result{Status: StatusOK, Reply: replyOf(v), Elapsed: took}
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}
