package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"seeker/internal/driver"
	"seeker/internal/model"
)

// fakeConn is a session whose liveness the test controls.
type fakeConn struct {
	driver.Conn
	id       int
	database string
	closed   atomic.Bool
	hang     atomic.Bool // ping never answers
	pings    atomic.Int32
}

func (f *fakeConn) Ping(ctx context.Context) (driver.ServerInfo, error) {
	f.pings.Add(1)
	if f.hang.Load() {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // a dead socket gives the call back late
		return driver.ServerInfo{}, ctx.Err()
	}
	return driver.ServerInfo{ProductName: "fake"}, nil
}
func (f *fakeConn) Close() error   { f.closed.Store(true); return nil }
func (f *fakeConn) IsClosed() bool { return f.closed.Load() }

type fakeServer struct {
	mu    sync.Mutex
	conns []*fakeConn
	fail  error
}

func (s *fakeServer) open(_ context.Context, _ model.Profile, _ string, database string) (driver.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return nil, s.fail
	}
	c := &fakeConn{id: len(s.conns), database: database}
	s.conns = append(s.conns, c)
	return c, nil
}

func (s *fakeServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

func newTestPool(t *testing.T) (*Pool, *fakeServer, model.Profile) {
	srv := &fakeServer{}
	p := New()
	p.open = srv.open
	p.idleProbeAfter = 50 * time.Millisecond
	p.probeTimeout = 100 * time.Millisecond
	prof := model.Profile{ID: "c1", DBType: model.Postgres, Database: "main"}
	if _, err := p.Connect(context.Background(), prof, "pw"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.CloseAll)
	return p, srv, prof
}

func must(t *testing.T) func(driver.Conn, error) *fakeConn {
	return func(got driver.Conn, err error) *fakeConn {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return got.(*fakeConn)
	}
}

func catalog(p *Pool, db string) (driver.Conn, error) {
	var got driver.Conn
	err := p.Catalog(context.Background(), "c1", db, func(_ context.Context, c driver.Conn) error { got = c; return nil })
	return got, err
}

func query(p *Pool, db, tab string) (driver.Conn, error) {
	var got driver.Conn
	err := p.Query(context.Background(), "c1", db, tab, func(_ context.Context, c driver.Conn) error { got = c; return nil })
	return got, err
}

func TestCatalogReusesThePrimary(t *testing.T) {
	p, srv, _ := newTestPool(t)
	c := must(t)(catalog(p, ""))
	if c.id != 0 || srv.count() != 1 {
		t.Fatalf("got session %d of %d", c.id, srv.count())
	}
	other := must(t)(catalog(p, "other"))
	if other.database != "other" {
		t.Fatalf("postgres needs a session in %q, got %q", "other", other.database)
	}
}

func TestMySQLCatalogStaysOnThePrimary(t *testing.T) {
	srv := &fakeServer{}
	p := New()
	p.open = srv.open
	defer p.CloseAll()
	if _, err := p.Connect(context.Background(), model.Profile{ID: "c1", DBType: model.MySQL, Database: "main"}, ""); err != nil {
		t.Fatal(err)
	}
	if c := must(t)(catalog(p, "other")); c.id != 0 {
		t.Fatal("mysql answers every database from the profile's session")
	}
	if c := must(t)(query(p, "other", "tab1")); c.database != "other" {
		t.Fatal("user SQL runs in the tab's database even on mysql")
	}
}

func TestTabsGetTheirOwnSessions(t *testing.T) {
	p, srv, _ := newTestPool(t)
	a := must(t)(query(p, "", "tab1"))
	b := must(t)(query(p, "", "tab2"))
	a2 := must(t)(query(p, "", "tab1"))
	if a == b || a != a2 || a.database != "main" {
		t.Fatalf("tab sessions: %d %d %d (%q)", a.id, b.id, a2.id, a.database)
	}
	// Two tabs on one database run side by side.
	started := make(chan struct{})
	release := make(chan struct{})
	go p.Query(context.Background(), "c1", "", "tab1", func(context.Context, driver.Conn) error {
		close(started)
		<-release
		return nil
	})
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Query(ctx, "c1", "", "tab2", func(context.Context, driver.Conn) error { return nil }); err != nil {
		t.Fatalf("tab2 waited on tab1: %v", err)
	}
	// The same tab waits for its own session.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	if err := p.Query(ctx2, "c1", "", "tab1", func(context.Context, driver.Conn) error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a tab's statements must not overlap: %v", err)
	}
	close(release)
	p.CloseTab("tab1")
	time.Sleep(10 * time.Millisecond)
	if !a.IsClosed() || b.IsClosed() {
		t.Fatal("CloseTab closes only that tab's session")
	}
	_ = srv
}

func TestCatalogCapsSessions(t *testing.T) {
	p, srv, _ := newTestPool(t)
	var wg sync.WaitGroup
	release := make(chan struct{})
	for range 5 {
		wg.Add(1)
		go p.Catalog(context.Background(), "c1", "", func(context.Context, driver.Conn) error {
			wg.Done()
			<-release
			return nil
		})
	}
	time.Sleep(50 * time.Millisecond)
	if n := srv.count(); n != catalogSessions {
		t.Fatalf("opened %d sessions, cap %d", n, catalogSessions)
	}
	close(release)
	wg.Wait()
}

func TestIdleSessionIsProbedAndReplacedQuietly(t *testing.T) {
	p, srv, _ := newTestPool(t)
	var lost []string
	p.OnLost = func(_, reason string) { lost = append(lost, reason) }
	first := must(t)(catalog(p, ""))
	if first.pings.Load() != 1 { // Connect's own
		t.Fatalf("a fresh session is not probed: %d pings", first.pings.Load())
	}
	time.Sleep(60 * time.Millisecond)
	first.hang.Store(true)
	next := must(t)(catalog(p, ""))
	if next == first || srv.count() != 2 {
		t.Fatal("a session that failed its probe must be replaced")
	}
	time.Sleep(100 * time.Millisecond)
	if !first.IsClosed() {
		t.Fatal("the dead session is closed once its ping gives up")
	}
	if len(lost) != 0 || !p.IsConnected("c1") {
		t.Fatalf("a probe replacement is not a loss: %v", lost)
	}
}

func TestRequestDuringProbeIsNotHandedTheDeadSession(t *testing.T) {
	p, _, _ := newTestPool(t)
	_, err := query(p, "", "tab1")
	if err != nil {
		t.Fatal(err)
	}
	dead := must(t)(query(p, "", "tab1"))
	time.Sleep(60 * time.Millisecond)
	dead.hang.Store(true)
	var wg sync.WaitGroup
	got := make([]*fakeConn, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := query(p, "", "tab1")
			if err == nil {
				got[i] = c.(*fakeConn)
			}
		}()
	}
	wg.Wait()
	if got[0] == nil || got[1] == nil || got[0] == dead || got[1] == dead || got[0] != got[1] {
		t.Fatalf("both requests should share the one replacement: %v %v (dead %v)", got[0], got[1], dead)
	}
}

func TestClosedSessionIsReportedAndReopened(t *testing.T) {
	p, _, _ := newTestPool(t)
	var lost []string
	p.OnLost = func(_, reason string) { lost = append(lost, reason) }
	err := p.Query(context.Background(), "c1", "", "tab1", func(_ context.Context, c driver.Conn) error {
		c.Close() // as a cancel that had to cut the socket
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lost) != 1 {
		t.Fatalf("a session that died in use is a loss: %v", lost)
	}
	if c := must(t)(query(p, "", "tab1")); c.IsClosed() {
		t.Fatal("the next request gets a fresh session")
	}
}

func TestDisconnectSticks(t *testing.T) {
	p, srv, _ := newTestPool(t)
	must(t)(catalog(p, "other"))
	p.Disconnect("c1")
	for _, c := range srv.conns {
		if !c.IsClosed() {
			t.Fatal("disconnect closes every session")
		}
	}
	if _, err := catalog(p, ""); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("a disconnected profile must not reopen: %v", err)
	}
}

func TestSessionInUseClosesOnReleaseAfterDisconnect(t *testing.T) {
	p, _, _ := newTestPool(t)
	var held *fakeConn
	p.Query(context.Background(), "c1", "", "tab1", func(_ context.Context, c driver.Conn) error {
		held = c.(*fakeConn)
		p.Disconnect("c1")
		if held.IsClosed() {
			t.Error("a session in use is not closed under its user")
		}
		return nil
	})
	if !held.IsClosed() {
		t.Fatal("it closes when released")
	}
}

func TestOpenFailureIsReturned(t *testing.T) {
	p, srv, _ := newTestPool(t)
	srv.fail = errors.New("refused")
	if _, err := catalog(p, "other"); err == nil || err.Error() != "refused" {
		t.Fatalf("got %v", err)
	}
}

// A catalog session of another database closes once it sat quiet; the
// profile's own and a tab's stay.
func TestIdleCatalogSessionOfAnotherDatabaseCloses(t *testing.T) {
	p, srv, _ := newTestPool(t)
	p.idleClose = 50 * time.Millisecond
	primary := must(t)(catalog(p, "main"))
	other := must(t)(catalog(p, "other"))
	tab := must(t)(query(p, "other", "t1"))
	deadline := time.Now().Add(2 * time.Second)
	for !other.IsClosed() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !other.IsClosed() {
		t.Fatal("idle session of another database stayed open")
	}
	if primary.IsClosed() || tab.IsClosed() {
		t.Fatalf("closed: primary %v, tab %v", primary.IsClosed(), tab.IsClosed())
	}
	before := srv.count()
	if again := must(t)(catalog(p, "other")); again == other || srv.count() != before+1 {
		t.Fatal("the next request did not open a fresh session")
	}
}

// A session used again before the deadline is not closed by the earlier
// release's timer.
func TestIdleCloseWaitsForTheLastUse(t *testing.T) {
	p, _, _ := newTestPool(t)
	p.idleProbeAfter = time.Hour
	p.idleClose = 150 * time.Millisecond
	other := must(t)(catalog(p, "other"))
	time.Sleep(100 * time.Millisecond)
	must(t)(catalog(p, "other"))
	time.Sleep(80 * time.Millisecond)
	if other.IsClosed() {
		t.Fatal("closed 80ms after its last use")
	}
}
