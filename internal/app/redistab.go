package app

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"seeker/internal/redis"
)

// The scan walks a page at a time and keeps asking while a sparse pattern
// has not filled a screenful: Redis counts slots visited, not keys matched,
// so a page can come back short or empty with the cursor still live. These
// are recall's numbers.
const (
	redisScanCount  = 500
	redisPageTarget = 200
	redisMaxTrips   = 20
	redisTimeout    = 30 * time.Second
)

// RedisTab is the state of one Redis tab: the keys of a logical database on
// the left, the chosen key's value on the right, and a console under both.
// The key list is scanned when the tab first shows and not restored: a
// keyspace changes under a closed tab, and a stale list would offer keys that
// are gone.
type RedisTab struct {
	Conn string
	DB   int

	// Pattern is the field's text; Keys is what the scan found for the
	// pattern it held when it ran.
	Pattern  string
	Keys     []redis.KeyInfo
	Total    int64 // DBSIZE, -1 until the first page says
	Scanning bool
	Err      string

	opened  bool
	started bool   // a first page has landed
	cursor  string // "0" when the walk is complete
	pattern string // the pattern the walk runs with
	gen     int    // bumped by every reset, so a page that lands after one is dropped

	// Sel is the chosen key, which Value holds the contents of.
	Sel   *redis.KeyInfo
	Value RedisValueState
	// ConfirmDelete turns the delete button into its confirmation.
	ConfirmDelete bool

	Console RedisConsole
	New     RedisNewKey
}

// Done reports a walk that reached the end of the keyspace.
func (r *RedisTab) Done() bool { return r.started && r.cursor == "0" }

// HasMore reports keys the walk has not read yet.
func (r *RedisTab) HasMore() bool { return r.started && r.cursor != "0" }

// RedisValueState is what the right pane shows of the chosen key, with the
// drafts of the edits under way.
type RedisValueState struct {
	Loading, Busy, Missing bool
	V                      *redis.Value
	// ReadAt is when V was read, which the TTL counts down from.
	ReadAt time.Time

	Hash        []redis.HashEntry
	HashCursor  string
	HashLoading bool

	Set        []redis.Bytes
	SetCursor  string
	SetLoading bool

	// Draft is the string being edited; it starts as the stored text and
	// again after every read.
	Draft string

	// TTLEditing opens the seconds field; TTLText is its text.
	TTLEditing bool
	TTLText    string

	// Field editing: EditField is the row being edited (a rename or a new
	// value), -1 for none; AddField opens a blank row at the end. A set uses
	// the same drafts, with FieldName as the member and no FieldValue.
	EditField  int
	FieldName  string
	FieldValue string
	AddField   bool

	gen int
}

// Remaining is the TTL left now, counted down from the last read; -1 and
// below stand as the server gave them.
func (v *RedisValueState) Remaining(now time.Time) int64 {
	if v.V == nil || v.V.TTL < 0 {
		if v.V == nil {
			return -2
		}
		return v.V.TTL
	}
	return max(0, v.V.TTL-int64(now.Sub(v.ReadAt)/time.Second))
}

// RedisEntry is one console line and what came of it.
type RedisEntry struct {
	ID      int
	Command string
	// State is "running", "ok", "error" or "confirm" (nothing was sent: the
	// command is not known to be read-only).
	State   string
	Output  string
	Elapsed time.Duration
}

// RedisConsole is a redis-cli style console on the tab's database. The
// backend decides what is safe: a command outside the read-only allowlist
// comes back unrun, and only an explicit "Run anyway" sends it. History
// lives for the tab's session.
type RedisConsole struct {
	Input   string
	Entries []RedisEntry
	history []string
	nextID  int
	// Scroll asks the output to jump to its end on the next frame.
	Scroll bool
}

// RedisNewKey is the new-key form. Creating never replaces a key that exists.
type RedisNewKey struct {
	Open  bool
	Key   string
	Type  string // "string", "hash" or "set"
	Value string
	Field string
	TTL   string // seconds; blank for none
	Err   string
	Busy  bool
}

// RedisKeyTypes is what the new-key form offers; the browser shows other
// types but cannot write them.
var RedisKeyTypes = []string{"string", "hash", "set"}

// RedisOf is a Redis tab's state, made, and its key list scanned, the first
// time the tab shows. Nil for a tab that is not Redis.
func (a *App) RedisOf(id string) *RedisTab {
	t, ok := a.TabByID(id)
	if !ok || t.Mode != ModeRedis {
		return nil
	}
	if a.redisTabs == nil {
		a.redisTabs = map[string]*RedisTab{}
	}
	rt := a.redisTabs[id]
	if rt == nil {
		db, _ := strconv.Atoi(t.Database)
		rt = &RedisTab{Conn: t.Conn, DB: db, Total: -1, cursor: "0", Value: RedisValueState{EditField: -1}}
		a.redisTabs[id] = rt
	}
	if !rt.opened {
		rt.opened = true
		a.redisReset(id)
	}
	return rt
}

// redisCall runs fn against the tab's database client off the window's
// thread: it waits for the connection the tab started, and then done runs on
// the window's thread with fn's error.
func (a *App) redisCall(rt *RedisTab, fn func(ctx context.Context, c *goredis.Client) error, done func(error)) {
	cs := a.conn(rt.Conn)
	conn, db := rt.Conn, rt.DB
	if a.Status(conn) != Connected {
		a.Connect(conn)
	}
	go func() {
		err := waitConnected(cs)
		if err == nil {
			ctx, cancel := context.WithTimeout(a.ctx, redisTimeout)
			defer cancel()
			var c *goredis.Client
			if c, err = a.rd.Client(ctx, conn, db); err == nil {
				err = fn(ctx, c)
			}
		}
		a.upd(func() { done(err) })
	}()
}

// RedisSearch scans again with the pattern field's text.
func (a *App) RedisSearch(id string) {
	if rt := a.redisTabs[id]; rt != nil {
		a.redisReset(id)
	}
}

// RedisRefresh scans again with the pattern as it was last applied.
func (a *App) RedisRefresh(id string) {
	if rt := a.redisTabs[id]; rt != nil {
		rt.Pattern = rt.pattern
		a.redisReset(id)
	}
}

func (a *App) redisReset(id string) {
	rt := a.redisTabs[id]
	if rt == nil {
		return
	}
	rt.gen++
	rt.pattern = strings.TrimSpace(rt.Pattern)
	rt.Keys, rt.cursor, rt.started, rt.Scanning, rt.Total, rt.Err = nil, "0", false, false, -1, ""
	a.RedisMore(id)
}

// RedisMore reads the next screenful of keys.
func (a *App) RedisMore(id string) {
	rt := a.redisTabs[id]
	if rt == nil || rt.Scanning || rt.Done() {
		return
	}
	rt.Scanning = true
	gen, cursor, pattern := rt.gen, rt.cursor, rt.pattern
	var got []redis.KeyInfo
	total, added := int64(-1), 0
	seen := map[string]bool{}
	for _, k := range rt.Keys {
		seen[k.Key.ID()] = true
	}
	a.redisCall(rt, func(ctx context.Context, c *goredis.Client) error {
		for trip := 0; trip < redisMaxTrips && added < redisPageTarget; trip++ {
			page, err := redis.ScanKeys(ctx, c, cursor, pattern, redisScanCount)
			if err != nil {
				return err
			}
			if page.Total >= 0 {
				total = page.Total
			}
			// SCAN may return a key twice across pages; the list must not.
			for _, k := range page.Keys {
				if !seen[k.Key.ID()] {
					seen[k.Key.ID()] = true
					got = append(got, k)
					added++
				}
			}
			cursor = page.Cursor
			if cursor == "0" {
				break
			}
		}
		return nil
	}, func(err error) {
		if rt.gen != gen {
			return
		}
		rt.Scanning = false
		if err != nil {
			rt.Err = shortError(err)
			return
		}
		if total >= 0 {
			rt.Total = total
		}
		rt.Keys = append(rt.Keys, got...)
		rt.cursor, rt.started = cursor, true
	})
}

// RedisSelect chooses a key and reads it.
func (a *App) RedisSelect(id string, key redis.KeyInfo) {
	rt := a.redisTabs[id]
	if rt == nil {
		return
	}
	if rt.Sel != nil && rt.Sel.Key.ID() == key.Key.ID() {
		return
	}
	rt.Sel = &key
	rt.ConfirmDelete = false
	rt.Value = RedisValueState{EditField: -1}
	a.RedisReload(id)
}

// RedisReload reads the chosen key again.
func (a *App) RedisReload(id string) {
	rt := a.redisTabs[id]
	if rt == nil || rt.Sel == nil {
		return
	}
	key := rt.Sel.Key
	rt.Value.gen++
	gen := rt.Value.gen
	rt.Value.Loading = true
	rt.ConfirmDelete = false
	var v *redis.Value
	a.redisCall(rt, func(ctx context.Context, c *goredis.Client) (err error) {
		v, err = redis.GetValue(ctx, c, key)
		return err
	}, func(err error) {
		if rt.Value.gen != gen {
			return
		}
		rt.Value.Loading = false
		if err != nil {
			a.toast(shortError(err))
			return
		}
		rt.Value.Missing, rt.Value.V, rt.Value.ReadAt = v == nil, v, time.Now()
		rt.Value.EditField, rt.Value.AddField, rt.Value.TTLEditing = -1, false, false
		if v == nil {
			return
		}
		if v.Kind == redis.KindString {
			rt.Value.Draft = v.Str.Text
		}
		if v.Kind == redis.KindHash {
			rt.Value.Hash, rt.Value.HashCursor = v.First.Entries, v.First.Cursor
		}
		if v.Kind == redis.KindSet {
			rt.Value.Set, rt.Value.SetCursor = v.FirstSet.Members, v.FirstSet.Cursor
		}
		// The list's row follows what the server says now.
		for i := range rt.Keys {
			if rt.Keys[i].Key.ID() == key.ID() {
				rt.Keys[i].TTL = v.TTL
			}
		}
		if rt.Sel != nil && rt.Sel.Key.ID() == key.ID() {
			rt.Sel.TTL = v.TTL
		}
	})
}

// redisWrite runs one write on the chosen key and reads it again, so the pane
// shows what Redis holds. Writes do not overlap.
func (a *App) redisWrite(id string, fn func(ctx context.Context, c *goredis.Client, key redis.Bytes) error) {
	rt := a.redisTabs[id]
	if rt == nil || rt.Sel == nil || rt.Value.Busy {
		return
	}
	rt.Value.Busy = true
	key := rt.Sel.Key
	a.redisCall(rt, func(ctx context.Context, c *goredis.Client) error { return fn(ctx, c, key) }, func(err error) {
		rt.Value.Busy = false
		if err != nil {
			a.toast(shortError(err))
			return
		}
		a.RedisReload(id)
	})
}

var keepTTL = redis.TTLChange{Mode: redis.TTLKeep}

// RedisSaveString writes the draft, keeping the key's TTL: a cache entry that
// loses its TTL on save quietly becomes permanent.
func (a *App) RedisSaveString(id string) {
	rt := a.redisTabs[id]
	if rt == nil {
		return
	}
	text := rt.Value.Draft
	a.redisWrite(id, func(ctx context.Context, c *goredis.Client, key redis.Bytes) error {
		_, err := redis.SetString(ctx, c, key, text, keepTTL, false)
		return err
	})
}

// RedisSaveField sets a field of the chosen hash; previous renames it.
func (a *App) RedisSaveField(id string, field redis.Bytes, value string, previous *redis.Bytes) {
	a.redisWrite(id, func(ctx context.Context, c *goredis.Client, key redis.Bytes) error {
		_, err := redis.HashSet(ctx, c, key, field, value, previous, keepTTL, false)
		return err
	})
}

// RedisRemoveField deletes one field of the chosen hash.
func (a *App) RedisRemoveField(id string, field redis.Bytes) {
	a.redisWrite(id, func(ctx context.Context, c *goredis.Client, key redis.Bytes) error {
		return redis.HashDelete(ctx, c, key, field)
	})
}

// RedisMoreFields reads the next page of the chosen hash.
func (a *App) RedisMoreFields(id string) {
	rt := a.redisTabs[id]
	if rt == nil || rt.Sel == nil || rt.Value.HashLoading || rt.Value.HashCursor == "0" || rt.Value.HashCursor == "" {
		return
	}
	rt.Value.HashLoading = true
	key, cursor, gen := rt.Sel.Key, rt.Value.HashCursor, rt.Value.gen
	var page redis.HashPage
	a.redisCall(rt, func(ctx context.Context, c *goredis.Client) (err error) {
		page, err = redis.HashScan(ctx, c, key, cursor)
		return err
	}, func(err error) {
		if rt.Value.gen != gen {
			return
		}
		rt.Value.HashLoading = false
		if err != nil {
			a.toast(shortError(err))
			return
		}
		seen := map[string]bool{}
		for _, e := range rt.Value.Hash {
			seen[e.Field.ID()] = true
		}
		for _, e := range page.Entries {
			if !seen[e.Field.ID()] {
				rt.Value.Hash = append(rt.Value.Hash, e)
			}
		}
		rt.Value.HashCursor = page.Cursor
	})
}

// RedisSaveMember adds a member to the chosen set; previous replaces it.
func (a *App) RedisSaveMember(id string, member redis.Bytes, previous *redis.Bytes) {
	a.redisWrite(id, func(ctx context.Context, c *goredis.Client, key redis.Bytes) error {
		_, err := redis.SetAdd(ctx, c, key, member, previous, keepTTL, false)
		return err
	})
}

// RedisRemoveMember removes one member of the chosen set.
func (a *App) RedisRemoveMember(id string, member redis.Bytes) {
	a.redisWrite(id, func(ctx context.Context, c *goredis.Client, key redis.Bytes) error {
		return redis.SetRemove(ctx, c, key, member)
	})
}

// RedisMoreMembers reads the next page of the chosen set.
func (a *App) RedisMoreMembers(id string) {
	rt := a.redisTabs[id]
	if rt == nil || rt.Sel == nil || rt.Value.SetLoading || rt.Value.SetCursor == "0" || rt.Value.SetCursor == "" {
		return
	}
	rt.Value.SetLoading = true
	key, cursor, gen := rt.Sel.Key, rt.Value.SetCursor, rt.Value.gen
	var page redis.SetPage
	a.redisCall(rt, func(ctx context.Context, c *goredis.Client) (err error) {
		page, err = redis.SetScan(ctx, c, key, cursor)
		return err
	}, func(err error) {
		if rt.Value.gen != gen {
			return
		}
		rt.Value.SetLoading = false
		if err != nil {
			a.toast(shortError(err))
			return
		}
		seen := map[string]bool{}
		for _, m := range rt.Value.Set {
			seen[m.ID()] = true
		}
		for _, m := range page.Members {
			if !seen[m.ID()] {
				rt.Value.Set = append(rt.Value.Set, m)
			}
		}
		rt.Value.SetCursor = page.Cursor
	})
}

// RedisSetTTL gives the chosen key a lifetime of seconds, or none.
func (a *App) RedisSetTTL(id string, change redis.TTLChange) {
	a.redisWrite(id, func(ctx context.Context, c *goredis.Client, key redis.Bytes) error {
		_, err := redis.SetTTL(ctx, c, key, change)
		return err
	})
}

// RedisDeleteSelected removes the chosen key and drops it from the list
// without scanning again.
func (a *App) RedisDeleteSelected(id string) {
	rt := a.redisTabs[id]
	if rt == nil || rt.Sel == nil || rt.Value.Busy {
		return
	}
	rt.Value.Busy = true
	key := rt.Sel.Key
	a.redisCall(rt, func(ctx context.Context, c *goredis.Client) error {
		_, err := redis.DeleteKeys(ctx, c, []redis.Bytes{key})
		return err
	}, func(err error) {
		rt.Value.Busy, rt.ConfirmDelete = false, false
		if err != nil {
			a.toast(shortError(err))
			return
		}
		a.redisForget(rt, key)
	})
}

// redisForget drops a deleted key from the list and the pane.
func (a *App) redisForget(rt *RedisTab, key redis.Bytes) {
	n := len(rt.Keys)
	rt.Keys = slices.DeleteFunc(rt.Keys, func(k redis.KeyInfo) bool { return k.Key.ID() == key.ID() })
	if rt.Total >= 0 && len(rt.Keys) < n {
		rt.Total--
	}
	if rt.Sel != nil && rt.Sel.Key.ID() == key.ID() {
		rt.Sel, rt.Value = nil, RedisValueState{EditField: -1}
	}
}

// OpenRedisNewKey shows the new-key form.
func (a *App) OpenRedisNewKey(id string) {
	if rt := a.redisTabs[id]; rt != nil {
		rt.New = RedisNewKey{Open: true, Type: "string"}
	}
}

// CloseRedisNewKey drops the form.
func (a *App) CloseRedisNewKey(id string) {
	if rt := a.redisTabs[id]; rt != nil {
		rt.New = RedisNewKey{}
	}
}

// RedisCreate writes the form's key, which must not exist yet, and chooses it.
func (a *App) RedisCreate(id string) {
	rt := a.redisTabs[id]
	if rt == nil || rt.New.Busy {
		return
	}
	n := &rt.New
	name := n.Key
	var ttl redis.TTLChange
	switch secs := strings.TrimSpace(n.TTL); {
	case name == "":
		n.Err = "Enter a key name."
		return
	case n.Type == "hash" && n.Field == "":
		n.Err = "Enter the first field's name."
		return
	case n.Type == "set" && n.Field == "":
		n.Err = "Enter the first member."
		return
	case secs != "":
		v, err := strconv.ParseInt(secs, 10, 64)
		if err != nil || v <= 0 {
			n.Err = "TTL is a number of seconds above 0, or blank for none."
			return
		}
		ttl = redis.TTLChange{Mode: redis.TTLSeconds, Seconds: v}
	}
	n.Err, n.Busy = "", true
	key, typ, value, field := redis.TextBytes(name), n.Type, n.Value, redis.TextBytes(n.Field)
	var created bool
	a.redisCall(rt, func(ctx context.Context, c *goredis.Client) (err error) {
		switch typ {
		case "hash":
			created, err = redis.HashSet(ctx, c, key, field, value, nil, ttl, true)
		case "set":
			created, err = redis.SetAdd(ctx, c, key, field, nil, ttl, true)
		default:
			created, err = redis.SetString(ctx, c, key, value, ttl, true)
		}
		return err
	}, func(err error) {
		rt.New.Busy = false
		switch {
		case err != nil:
			rt.New.Err = shortError(err)
		case !created:
			rt.New.Err = "A key with that name already exists."
		default:
			rt.New = RedisNewKey{}
			info := redis.KeyInfo{Key: key, Type: typ, TTL: -1}
			if ttl.Mode == redis.TTLSeconds {
				info.TTL = ttl.Seconds
			}
			rt.Keys = append([]redis.KeyInfo{info}, rt.Keys...)
			if rt.Total >= 0 {
				rt.Total++
			}
			rt.Sel = nil
			a.RedisSelect(id, info)
		}
	})
}

// RedisRun sends the console's line.
func (a *App) RedisRun(id string) {
	rt := a.redisTabs[id]
	if rt == nil {
		return
	}
	line := strings.TrimSpace(rt.Console.Input)
	if line == "" {
		return
	}
	con := &rt.Console
	if len(con.history) == 0 || con.history[len(con.history)-1] != line {
		con.history = append(con.history, line)
	}
	con.Input = ""
	con.nextID++
	con.Entries = append(con.Entries, RedisEntry{ID: con.nextID, Command: line, State: "running"})
	a.redisExec(id, con.nextID, false)
}

// RedisConfirm sends a command the console held back.
func (a *App) RedisConfirm(id string, entry int) { a.redisExec(id, entry, true) }

func (a *App) redisExec(id string, entry int, confirmed bool) {
	rt := a.redisTabs[id]
	if rt == nil {
		return
	}
	i := slices.IndexFunc(rt.Console.Entries, func(e RedisEntry) bool { return e.ID == entry })
	if i < 0 {
		return
	}
	rt.Console.Entries[i].State = "running"
	line := rt.Console.Entries[i].Command
	var res redis.Result
	a.redisCall(rt, func(ctx context.Context, c *goredis.Client) error {
		res = redis.Execute(ctx, c, line, confirmed)
		return nil
	}, func(err error) {
		j := slices.IndexFunc(rt.Console.Entries, func(e RedisEntry) bool { return e.ID == entry })
		if j < 0 {
			return
		}
		e := &rt.Console.Entries[j]
		switch {
		case err != nil:
			e.State, e.Output = "error", shortError(err)
		case res.Status == redis.StatusNeedsConfirmation:
			e.State, e.Output = "confirm", "This command is not known to be read-only and may change data."
		case res.Status == redis.StatusError:
			e.State, e.Output, e.Elapsed = "error", "(error) "+res.Message, res.Elapsed
		default:
			e.State, e.Output, e.Elapsed = "ok", redis.FormatReply(res.Reply), res.Elapsed
			// What a confirmed command did may show in the open key.
			if confirmed {
				a.RedisReload(id)
			}
		}
		rt.Console.Scroll = true
	})
}

// RedisHistory steps the console input through the commands sent: delta -1
// is older. It reports whether the input changed.
func (a *App) RedisHistory(id string, delta int) {
	rt := a.redisTabs[id]
	if rt == nil || len(rt.Console.history) == 0 {
		return
	}
	con := &rt.Console
	at := slices.Index(con.history, strings.TrimSpace(con.Input))
	switch {
	case at < 0 && delta < 0:
		at = len(con.history) - 1
	case at < 0:
		return
	default:
		at += delta
	}
	if at < 0 {
		at = 0
	}
	if at >= len(con.history) {
		con.Input = ""
		return
	}
	con.Input = con.history[at]
}

// ClearRedisConsole empties the output.
func (a *App) ClearRedisConsole(id string) {
	if rt := a.redisTabs[id]; rt != nil {
		rt.Console.Entries = nil
	}
}
