package pool

import (
	"context"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"seeker/internal/driver"
	"seeker/internal/model"
)

// reapingProxy forwards TCP until told to blackhole: then it drops every
// byte of the connections it has without closing either side, the shape
// of a socket a NAT reaped. Connections made afterwards still work.
type reapingProxy struct {
	ln    net.Listener
	mu    sync.Mutex
	pairs []*proxyPair
}

type proxyPair struct {
	client, upstream net.Conn
	dead             bool
	mu               sync.Mutex
}

func startProxy(t *testing.T, upstream string) *reapingProxy {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &reapingProxy{ln: ln}
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", upstream)
			if err != nil {
				client.Close()
				continue
			}
			pair := &proxyPair{client: client, upstream: up}
			p.mu.Lock()
			p.pairs = append(p.pairs, pair)
			p.mu.Unlock()
			go pair.forward(client, up)
			go pair.forward(up, client)
		}
	}()
	t.Cleanup(p.stop)
	return p
}

func (pp *proxyPair) forward(from, to net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := from.Read(buf)
		if err != nil {
			from.Close()
			to.Close()
			return
		}
		pp.mu.Lock()
		dead := pp.dead
		pp.mu.Unlock()
		if !dead {
			if _, err := to.Write(buf[:n]); err != nil && err != io.EOF {
				return
			}
		}
	}
}

func (p *reapingProxy) port() int { return p.ln.Addr().(*net.TCPAddr).Port }

func (p *reapingProxy) blackholeAll() []*proxyPair {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pp := range p.pairs {
		pp.mu.Lock()
		pp.dead = true
		pp.mu.Unlock()
	}
	return append([]*proxyPair(nil), p.pairs...)
}

func (p *reapingProxy) stop() {
	p.ln.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pp := range p.pairs {
		pp.client.Close()
		pp.upstream.Close()
	}
}

func livePG(t *testing.T) (model.Profile, string, string) {
	raw := os.Getenv("SEEKER_TEST_PG_URL")
	if raw == "" {
		t.Skip("SEEKER_TEST_PG_URL is not set; start ../recall/compose.yaml and set it to run this test")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	port, _ := strconv.Atoi(u.Port())
	p := model.Profile{ID: "pool-live", DBType: model.Postgres, Host: "127.0.0.1", Port: port, Username: u.User.Username(),
		ConnectTimeoutSecs: 5, Database: strings.TrimPrefix(u.Path, "/")}
	return p, pw, u.Host
}

func selectOne(ctx context.Context, c driver.Conn) error {
	if r := c.Execute(ctx, "select 1", 1, nil); len(r) != 1 || r[0].Err != nil {
		return r[0].Err
	}
	return nil
}

func TestLiveReapedSessionIsReplacedWithoutLoss(t *testing.T) {
	prof, pw, upstream := livePG(t)
	proxy := startProxy(t, upstream)
	prof.Port = proxy.port()
	p := New()
	p.idleProbeAfter = 300 * time.Millisecond
	p.probeTimeout = 500 * time.Millisecond
	defer p.CloseAll()
	var mu sync.Mutex
	var lost []string
	p.OnLost = func(_, reason string) { mu.Lock(); lost = append(lost, reason); mu.Unlock() }
	if _, err := p.Connect(context.Background(), prof, pw); err != nil {
		t.Fatal(err)
	}
	var first driver.Conn
	ctx := context.Background()
	if err := p.Catalog(ctx, prof.ID, "", func(ctx context.Context, c driver.Conn) error { first = c; return selectOne(ctx, c) }); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	reaped := proxy.blackholeAll()

	// Opening a table asks for several things at once; the second request
	// arrives while the first one's probe waits on the dead socket.
	start := time.Now()
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = p.Catalog(ctx, prof.ID, "", func(ctx context.Context, c driver.Conn) error {
				if c == first {
					t.Error("handed the reaped session")
				}
				return selectOne(ctx, c)
			})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("replacement took %v", d)
	}
	// The dead socket only goes away now, after its replacement owns the
	// slot: the window recall's false "Connection lost" came from.
	for _, pp := range reaped {
		pp.client.Close()
		pp.upstream.Close()
	}
	time.Sleep(time.Second)
	if err := p.Catalog(ctx, prof.ID, "", selectOne); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lost) != 0 || !p.IsConnected(prof.ID) {
		t.Fatalf("replacement reported as loss: %v", lost)
	}
}

func TestLiveTabSessionKeepsState(t *testing.T) {
	prof, pw, _ := livePG(t)
	p := New()
	defer p.CloseAll()
	if _, err := p.Connect(context.Background(), prof, pw); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runIn := func(tab, sql string) []driver.Result {
		var rs []driver.Result
		if err := p.Query(ctx, prof.ID, "", tab, func(ctx context.Context, c driver.Conn) error {
			rs = c.Execute(ctx, sql, driver.MaxRows, nil)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return rs
	}
	runIn("a", "create temp table only_a (v int)")
	if r := runIn("a", "select * from only_a"); r[0].Err != nil {
		t.Fatalf("tab a lost its temp table: %v", r[0].Err)
	}
	if r := runIn("b", "select * from only_a"); r[0].Err == nil {
		t.Fatal("tab b sees tab a's session")
	}
}
