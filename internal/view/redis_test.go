package view

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"seeker/internal/app"
	"seeker/internal/model"
	"seeker/internal/redis"
)

// TestRedisTab walks a Redis tab against a real server (SEEKER_TEST_REDIS,
// host:port): the keys list, choosing one reads it, editing writes it back
// with its TTL kept, a hash field renames, the console asks before a write,
// and a delete drops the row. It touches only keys under a prefix of its
// own in database 15, and removes them.
func TestRedisTab(t *testing.T) {
	addr := os.Getenv("SEEKER_TEST_REDIS")
	if addr == "" {
		t.Skip("SEEKER_TEST_REDIS is not set")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(port)
	profile := model.Profile{ID: "r", Name: "cache", DBType: model.Redis, Host: host, Port: n, ConnectTimeoutSecs: 5, DefaultDB: 15}

	var id [4]byte
	rand.Read(id[:])
	prefix := "seeker-test:" + hex.EncodeToString(id[:]) + ":"
	str, hash := redis.TextBytes(prefix+"str"), redis.TextBytes(prefix+"hash")

	// Seed with a client of the test's own, apart from the app's.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := redis.ConfigOf(profile, "")
	pool := redis.NewPool()
	defer pool.CloseAll()
	if _, err := pool.Open(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	c, err := pool.Client(ctx, cfg.ID, 15)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { redis.DeleteKeys(context.Background(), c, []redis.Bytes{str, hash}) })
	redis.SetString(ctx, c, str, "hello", redis.TTLChange{Mode: redis.TTLSeconds, Seconds: 500}, false)
	redis.HashSet(ctx, c, hash, redis.TextBytes("f1"), "v1", nil, redis.TTLChange{}, false)

	core, p, tt := editorWindow(t, profile, "", "select 1")
	core.OpenRedisTab("r", 15)
	tab := activeID(core)
	rt := core.RedisOf(tab)
	if rt == nil {
		t.Fatal("no Redis tab")
	}
	rt.Pattern = prefix + "*"
	core.RedisSearch(tab)
	p.waitUntil(t, tt, "both keys", func() bool { return len(rt.Keys) == 2 && !rt.Scanning })
	p.frame(tt)
	if !tt.HasText(prefix+"str") || !tt.HasText(prefix+"hash") {
		t.Fatalf("the list lacks the keys: %v", tt.Texts())
	}

	// Choosing a string reads it, with its TTL.
	tt.Click(prefix + "str")
	p.waitUntil(t, tt, "the string", func() bool { return rt.Value.V != nil && rt.Value.V.Kind == redis.KindString })
	if rt.Value.Draft != "hello" || rt.Value.V.TTL <= 0 {
		t.Fatalf("value %+v draft %q", rt.Value.V, rt.Value.Draft)
	}

	// Saving keeps the TTL.
	rt.Value.Draft = "changed"
	core.RedisSaveString(tab)
	p.waitUntil(t, tt, "the save", func() bool { return !rt.Value.Busy && rt.Value.V != nil && rt.Value.V.Str.Text == "changed" })
	if rt.Value.V.TTL <= 0 {
		t.Errorf("the save lost the TTL: %d", rt.Value.V.TTL)
	}

	// A hash lists its fields, and a field renames.
	tt.Click(prefix + "hash")
	p.waitUntil(t, tt, "the hash", func() bool { return rt.Value.V != nil && rt.Value.V.Kind == redis.KindHash })
	p.frame(tt)
	if !tt.HasText("f1") || !tt.HasText("v1") {
		t.Fatalf("hash rows: %v", tt.Texts())
	}
	prev := redis.TextBytes("f1")
	core.RedisSaveField(tab, redis.TextBytes("f2"), "v2", &prev)
	p.waitUntil(t, tt, "the rename", func() bool {
		return !rt.Value.Busy && len(rt.Value.Hash) == 1 && rt.Value.Hash[0].Field.Text == "f2"
	})

	// The console asks before a write, and sends it once told to.
	rt.Console.Input = "SET " + prefix + "con 1"
	core.RedisRun(tab)
	p.waitUntil(t, tt, "the question", func() bool {
		return len(rt.Console.Entries) == 1 && rt.Console.Entries[0].State == "confirm"
	})
	core.RedisConfirm(tab, rt.Console.Entries[0].ID)
	p.waitUntil(t, tt, "the write", func() bool { return rt.Console.Entries[0].State == "ok" })
	rt.Console.Input = "GET " + prefix + "con"
	core.RedisRun(tab)
	p.waitUntil(t, tt, "the read", func() bool { return len(rt.Console.Entries) == 2 && rt.Console.Entries[1].State == "ok" })
	if got := rt.Console.Entries[1].Output; got != `"1"` {
		t.Errorf("GET printed %s", got)
	}
	redis.DeleteKeys(ctx, c, []redis.Bytes{redis.TextBytes(prefix + "con")})

	// A delete takes the row out without scanning again.
	core.RedisDeleteSelected(tab)
	p.waitUntil(t, tt, "the delete", func() bool { return len(rt.Keys) == 1 && rt.Sel == nil })
	p.frame(tt)
	if tt.HasText(prefix + "hash") {
		t.Errorf("the deleted key still shows: %v", tt.Texts())
	}

	// A set made from the form lists its members; a member is replaced and
	// another removed.
	setName := prefix + "set"
	t.Cleanup(func() { redis.DeleteKeys(context.Background(), c, []redis.Bytes{redis.TextBytes(setName)}) })
	core.OpenRedisNewKey(tab)
	rt.New.Key, rt.New.Type, rt.New.Field = setName, "set", "m1"
	core.RedisCreate(tab)
	p.waitUntil(t, tt, "the set", func() bool {
		return rt.Value.V != nil && rt.Value.V.Kind == redis.KindSet && len(rt.Value.Set) == 1
	})
	p.frame(tt)
	if !tt.HasText("m1") {
		t.Fatalf("set rows: %v", tt.Texts())
	}
	prevM := redis.TextBytes("m1")
	core.RedisSaveMember(tab, redis.TextBytes("m2"), &prevM)
	p.waitUntil(t, tt, "the replace", func() bool {
		return !rt.Value.Busy && len(rt.Value.Set) == 1 && rt.Value.Set[0].Text == "m2"
	})
	core.RedisSaveMember(tab, redis.TextBytes("m3"), nil)
	p.waitUntil(t, tt, "the add", func() bool { return !rt.Value.Busy && len(rt.Value.Set) == 2 })
	core.RedisRemoveMember(tab, redis.TextBytes("m2"))
	p.waitUntil(t, tt, "the remove", func() bool {
		return !rt.Value.Busy && len(rt.Value.Set) == 1 && rt.Value.Set[0].Text == "m3"
	})
	_ = app.ModeRedis
}

// A Redis connection's rows are its numbered databases, leaves that open a
// tab on their database.
func TestRedisTree(t *testing.T) {
	addr := os.Getenv("SEEKER_TEST_REDIS")
	if addr == "" {
		t.Skip("SEEKER_TEST_REDIS is not set")
	}
	host, port, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(port)
	profile := model.Profile{ID: "r", Name: "cache", DBType: model.Redis, Host: host, Port: n, ConnectTimeoutSecs: 5, DefaultDB: 0}
	core, p, tt := editorWindow(t, profile, "", "select 1")

	conn := app.Node{Kind: app.KindConnection, Conn: "r"}
	core.ActivateNode(conn)
	var dbs []app.Node
	p.waitUntil(t, tt, "the databases", func() bool {
		dbs = core.Children(conn)
		return len(dbs) > 0 && dbs[0].Kind == app.KindRedisDB
	})
	if len(dbs) < 16 || dbs[3].DB != "3" {
		t.Fatalf("databases: %d, %+v", len(dbs), dbs[:min(4, len(dbs))])
	}
	core.ActivateNode(dbs[3])
	tab, _ := core.ActiveTab()
	if tab.Mode != app.ModeRedis || tab.Database != "3" || tab.Conn != "r" {
		t.Errorf("the tab is %+v", tab)
	}
	// The same database again reuses its tab.
	before := len(core.Tabs().Items)
	core.ActivateNode(dbs[3])
	if len(core.Tabs().Items) != before {
		t.Error("a second click opened another tab")
	}
}

// Opening a Redis database selects its row in the sidebar.
func TestRedisTabRevealsItsRow(t *testing.T) {
	addr := os.Getenv("SEEKER_TEST_REDIS")
	if addr == "" {
		t.Skip("SEEKER_TEST_REDIS is not set")
	}
	host, port, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(port)
	profile := model.Profile{ID: "r", Name: "cache", DBType: model.Redis, Host: host, Port: n, ConnectTimeoutSecs: 5}
	core, p, tt := editorWindow(t, profile, "", "select 1")
	core.OpenRedisTab("r", 2)
	p.waitUntil(t, tt, "the row", func() bool {
		row := *core.TreeRow()
		out := core.Outline()
		return row >= 0 && row < out.Rows() && out.Item(row).Kind == app.KindRedisDB && out.Item(row).DB == "2"
	})
}
