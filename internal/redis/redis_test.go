package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"seeker/internal/model"
)

func TestNewBytes(t *testing.T) {
	if b := NewBytes([]byte("héllo")); b.Binary || b.Text != "héllo" {
		t.Errorf("utf8: %+v", b)
	}
	b := NewBytes([]byte{'a', 0xff, '\\', 0x00, 'z'})
	if !b.Binary || b.Text != `a\xff\\\x00z` {
		t.Errorf("binary label %q binary=%v", b.Text, b.Binary)
	}
	if string(b.Data) != "a\xff\\\x00z" {
		t.Errorf("the bytes must stay exact: %q", b.Data)
	}
}

func TestParseCommandLine(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		err  bool
	}{
		{"GET foo", []string{"GET", "foo"}, false},
		{"  SET   k  v ;;", []string{"SET", "k", "v"}, false},
		{`SET k "a b"`, []string{"SET", "k", "a b"}, false},
		{`SET k 'a "b" c'`, []string{"SET", "k", `a "b" c`}, false},
		{`SET k "line\nbreak\tx"`, []string{"SET", "k", "line\nbreak\tx"}, false},
		{`SET k a\ b`, []string{"SET", "k", "a b"}, false},
		{`SET k ""`, []string{"SET", "k", ""}, false},
		{`SET k "open`, nil, true},
		{"   ;", nil, true},
		{"", nil, true},
	}
	for _, c := range cases {
		got, err := ParseCommandLine(c.in)
		if (err != nil) != c.err || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseCommandLine(%q) = %q, %v; want %q (err %v)", c.in, got, err, c.want, c.err)
		}
	}
}

func TestCommandSafety(t *testing.T) {
	for _, n := range []string{"get", "HGETALL", "scan", "Ttl", "JSON.GET"} {
		if !IsRead(n) {
			t.Errorf("%s should be read-only", n)
		}
	}
	// Writes, and reads that block or walk the keyspace, ask first.
	for _, n := range []string{"set", "DEL", "FLUSHALL", "KEYS", "XREAD", "WAIT", "SOMEMODULE.CMD"} {
		if IsRead(n) {
			t.Errorf("%s must not run unconfirmed", n)
		}
	}
	for _, n := range []string{"monitor", "SUBSCRIBE", "psync"} {
		if !IsHijacking(n) {
			t.Errorf("%s takes over the connection", n)
		}
	}
}

// testConfig reads SEEKER_TEST_REDIS (host:port) and skips without it, so
// the suite runs on a machine with no server:
//
//	SEEKER_TEST_REDIS=172.16.13.110:6379
func testConfig(t *testing.T) Config {
	t.Helper()
	addr := os.Getenv("SEEKER_TEST_REDIS")
	if addr == "" {
		t.Skip("SEEKER_TEST_REDIS is not set")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	return ConfigOf(model.Profile{ID: "t", DBType: model.Redis, Host: host, Port: p, ConnectTimeoutSecs: 5, DefaultDB: 15}, os.Getenv("SEEKER_TEST_REDIS_PASSWORD"))
}

// The live test touches only keys under a prefix of its own, and removes
// them, so it can share a server.
func TestLive(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := NewPool()
	defer pool.CloseAll()
	info, err := pool.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if info.Product != "Redis" || info.Version == "" || info.Database != "db15" {
		t.Errorf("info %+v", info)
	}
	c, err := pool.Client(ctx, cfg.ID, cfg.DefaultDB)
	if err != nil {
		t.Fatal(err)
	}
	var id [4]byte
	rand.Read(id[:])
	prefix := "seeker-test:" + hex.EncodeToString(id[:]) + ":"
	str, hash, bin := TextBytes(prefix+"str"), TextBytes(prefix+"hash"), NewBytes([]byte(prefix+"bin\xff"))
	t.Cleanup(func() { DeleteKeys(context.Background(), c, []Bytes{str, hash, bin}) })

	// A new key refuses to replace one that exists.
	if ok, err := SetString(ctx, c, str, "v1", TTLChange{Mode: TTLSeconds, Seconds: 100}, true); !ok || err != nil {
		t.Fatalf("create: %v %v", ok, err)
	}
	if ok, _ := SetString(ctx, c, str, "other", TTLChange{}, true); ok {
		t.Error("only-if-absent replaced a key")
	}
	// An edit keeps the TTL it found.
	if ok, err := SetString(ctx, c, str, "v2", TTLChange{Mode: TTLKeep}, false); !ok || err != nil {
		t.Fatalf("edit: %v %v", ok, err)
	}
	v, err := GetValue(ctx, c, str)
	if err != nil || v == nil || v.Kind != KindString || v.Str.Text != "v2" || v.TTL <= 0 || v.TTL > 100 {
		t.Fatalf("value %+v, %v", v, err)
	}
	if n, err := SetTTL(ctx, c, str, TTLChange{Mode: TTLPersist}); err != nil || n != -1 {
		t.Errorf("persist: %d %v", n, err)
	}

	// Binary keys stay whole.
	if ok, err := SetString(ctx, c, bin, "x", TTLChange{}, true); !ok || err != nil {
		t.Fatalf("binary key: %v %v", ok, err)
	}

	// A hash: create, add, rename a field, delete a field.
	if ok, err := HashSet(ctx, c, hash, TextBytes("a"), "1", nil, TTLChange{}, true); !ok || err != nil {
		t.Fatalf("hash create: %v %v", ok, err)
	}
	HashSet(ctx, c, hash, TextBytes("b"), "2", nil, TTLChange{}, false)
	prev := TextBytes("a")
	HashSet(ctx, c, hash, TextBytes("c"), "1", &prev, TTLChange{}, false)
	v, err = GetValue(ctx, c, hash)
	if err != nil || v == nil || v.Kind != KindHash || v.HashLen != 2 {
		t.Fatalf("hash %+v, %v", v, err)
	}
	got := map[string]string{}
	for _, e := range v.First.Entries {
		got[e.Field.Text] = e.Value.Text
	}
	if !reflect.DeepEqual(got, map[string]string{"b": "2", "c": "1"}) {
		t.Errorf("hash after rename: %v", got)
	}
	HashDelete(ctx, c, hash, TextBytes("b"))

	// The scan finds them, with type and TTL.
	page, err := ScanKeys(ctx, c, "0", prefix+"*", 1000)
	if err != nil || page.Total < 3 {
		t.Fatalf("scan %+v, %v", page, err)
	}
	seen := map[string]string{}
	for {
		for _, k := range page.Keys {
			seen[k.Key.ID()] = k.Type
		}
		if page.Cursor == "0" {
			break
		}
		if page, err = ScanKeys(ctx, c, page.Cursor, prefix+"*", 1000); err != nil {
			t.Fatal(err)
		}
	}
	if seen[str.ID()] != "string" || seen[hash.ID()] != "hash" || seen[bin.ID()] != "string" {
		t.Errorf("scan saw %q", seen)
	}

	// The console asks before a write, and runs it once confirmed.
	if r := Execute(ctx, c, "SET "+prefix+"con 1", false); r.Status != StatusNeedsConfirmation {
		t.Errorf("unconfirmed write: %+v", r)
	}
	if r := Execute(ctx, c, "SET "+prefix+"con 1", true); r.Status != StatusOK {
		t.Errorf("confirmed write: %+v", r)
	}
	r := Execute(ctx, c, "GET "+prefix+"con", false)
	if r.Status != StatusOK || r.Reply.Kind != ReplyBytes || r.Reply.Bytes.Text != "1" {
		t.Errorf("read: %+v", r)
	}
	if r := Execute(ctx, c, "NOSUCHCOMMAND", true); r.Status != StatusError {
		t.Errorf("unknown command: %+v", r)
	}
	if r := Execute(ctx, c, "SUBSCRIBE x", true); r.Status != StatusError {
		t.Errorf("subscribe: %+v", r)
	}
	DeleteKeys(ctx, c, []Bytes{TextBytes(prefix + "con")})

	n, err := DeleteKeys(ctx, c, []Bytes{str, hash, bin})
	if err != nil || n != 3 {
		t.Errorf("delete: %d %v", n, err)
	}
	if v, err := GetValue(ctx, c, str); err != nil || v != nil {
		t.Errorf("a deleted key reads as %+v, %v", v, err)
	}

	dbs, err := ListDatabases(ctx, c)
	if err != nil || len(dbs) < 16 {
		t.Errorf("databases %v, %v", len(dbs), err)
	}
}

func TestFormatTTL(t *testing.T) {
	for in, want := range map[int64]string{-1: "No expiry", -2: "Expired", 0: "0s", 59: "59s", 61: "1m 1s", 3600: "1h", 90061: "1d 1h", 3 * 86400: "3d"} {
		if got := FormatTTL(in); got != want {
			t.Errorf("FormatTTL(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatReply(t *testing.T) {
	arr := Reply{Kind: ReplyArray, Items: []Reply{
		{Kind: ReplyBytes, Bytes: TextBytes("a<b")},
		{Kind: ReplyInt, Int: 3},
		{},
		{Kind: ReplyArray, Items: []Reply{{Kind: ReplyBytes, Bytes: TextBytes("x")}, {Kind: ReplyBytes, Bytes: TextBytes("y")}}},
	}}
	want := "1) \"a<b\"\n2) (integer) 3\n3) (nil)\n4) 1) \"x\"\n   2) \"y\""
	if got := FormatReply(arr); got != want {
		t.Errorf("FormatReply:\n%s\nwant\n%s", got, want)
	}
	if got := FormatReply(Reply{Kind: ReplyArray}); got != "(empty array)" {
		t.Errorf("empty: %q", got)
	}
}
