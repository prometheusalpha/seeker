// Package pool owns the open sessions: which session answers a request,
// when a quiet one is checked before use, and when a dead one is replaced.
//
// Two kinds of session, as in recall:
//   - catalog sessions answer the sidebar (lists, DDL, counts, edits).
//     Postgres needs them per database; MySQL answers every database from
//     the profile's own.
//   - query sessions run the user's SQL, one per tab, opened in the tab's
//     database so unqualified names resolve there, and kept for the tab so
//     its SET, temp tables and BEGIN survive between runs. One per tab
//     rather than per database, so two tabs on one database run side by
//     side.
package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"seeker/internal/driver"
	"seeker/internal/model"
)

const (
	// IdleProbeAfter: a socket a NAT or firewall reaped leaves no FIN and
	// no RST, so the next statement waits out the kernel's retransmissions
	// (over 300s seen in recall). A session idle this long is pinged first.
	// Five seconds is well under the timers that reap flows (thirty and up).
	IdleProbeAfter = 5 * time.Second
	// ProbeTimeout is how long that ping may take before the session
	// counts as gone.
	ProbeTimeout = 5 * time.Second
	// CatalogTimeout bounds a sidebar request. User SQL has none.
	CatalogTimeout = 30 * time.Second
	// catalogSessions lets a slow foreign-key listing not hold up the
	// columns the user opened next.
	catalogSessions = 3
)

var ErrNotConnected = errors.New("pool: not connected")

type OpenFunc func(ctx context.Context, p model.Profile, password, database string) (driver.Conn, error)

type Pool struct {
	// OnLost hears of a session that died under the app (server gone, a
	// cancel that had to cut the socket). Its state is gone; the next
	// request opens a fresh one. Called without the pool's lock held.
	OnLost func(connectionID, reason string)

	open           OpenFunc
	idleProbeAfter time.Duration
	probeTimeout   time.Duration

	mu sync.Mutex
	// profiles outlive their sessions: the password is only here, and it
	// is what lets a request reopen a session that died. Disconnect is
	// what forgets it, so an explicit disconnect sticks.
	profiles map[string]profile
	slots    map[key]*slot
}

type profile struct {
	p        model.Profile
	password string
}

type key struct {
	conn, database string
	tab            string // "" for catalog sessions
}

type slot struct {
	max      int
	sessions []*session
	opening  int
	// changed is closed and replaced whenever a session frees up or goes
	// away, waking the requests waiting for one.
	changed chan struct{}
}

type session struct {
	conn      driver.Conn
	busy      bool
	idleSince time.Time
}

func New() *Pool {
	return &Pool{
		open:           driver.Open,
		idleProbeAfter: IdleProbeAfter,
		probeTimeout:   ProbeTimeout,
		profiles:       map[string]profile{},
		slots:          map[key]*slot{},
	}
}

// Connect opens the profile's own catalog session and keeps the profile
// for the sessions opened later. A profile already connected is replaced:
// its sessions close, so an edited profile takes effect.
func (p *Pool) Connect(ctx context.Context, prof model.Profile, password string) (driver.ServerInfo, error) {
	conn, err := p.open(ctx, prof, password, prof.Database)
	if err != nil {
		return driver.ServerInfo{}, err
	}
	info, err := conn.Ping(ctx)
	if err != nil {
		conn.Close()
		return driver.ServerInfo{}, err
	}
	p.Disconnect(prof.ID)
	p.mu.Lock()
	p.profiles[prof.ID] = profile{prof, password}
	s := p.slotLocked(key{conn: prof.ID, database: prof.Database}, catalogSessions)
	s.sessions = append(s.sessions, &session{conn: conn, idleSince: time.Now()})
	p.mu.Unlock()
	return info, nil
}

// Disconnect closes every session of the profile and forgets it.
func (p *Pool) Disconnect(connectionID string) {
	p.closeWhere(func(k key) bool { return k.conn == connectionID }, func() { delete(p.profiles, connectionID) })
}

// CloseTab closes the query sessions a tab holds.
func (p *Pool) CloseTab(tab string) {
	p.closeWhere(func(k key) bool { return k.tab == tab && tab != "" }, nil)
}

// CloseAll closes everything, on quit.
func (p *Pool) CloseAll() {
	p.closeWhere(func(key) bool { return true }, func() { clear(p.profiles) })
}

// closeWhere drops the matching slots. A session in use is closed by
// whoever holds it, when they release it into a slot that is gone.
func (p *Pool) closeWhere(match func(key) bool, forget func()) {
	var idle []driver.Conn
	p.mu.Lock()
	if forget != nil {
		forget()
	}
	for k, s := range p.slots {
		if !match(k) {
			continue
		}
		for _, ss := range s.sessions {
			if !ss.busy {
				idle = append(idle, ss.conn)
			}
		}
		delete(p.slots, k)
		close(s.changed)
	}
	p.mu.Unlock()
	for _, c := range idle {
		c.Close()
	}
}

func (p *Pool) IsConnected(connectionID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.profiles[connectionID]
	return ok
}

// Profile is the connected profile, as Connect was given it.
func (p *Pool) Profile(connectionID string) (model.Profile, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pr, ok := p.profiles[connectionID]
	return pr.p, ok
}

// Catalog runs fn on a catalog session that can see database, under
// CatalogTimeout.
func (p *Pool) Catalog(ctx context.Context, connectionID, database string, fn func(context.Context, driver.Conn) error) error {
	ctx, cancel := context.WithTimeout(ctx, CatalogTimeout)
	defer cancel()
	prof, ok := p.Profile(connectionID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotConnected, connectionID)
	}
	// MySQL reaches any database from the profile's session; Postgres
	// sessions are pinned, so another database gets its own.
	if !driver.DatabaseScoped(prof.DBType) || database == "" {
		database = prof.Database
	}
	return p.with(ctx, key{conn: connectionID, database: database}, catalogSessions, fn)
}

// Query runs fn on the tab's own session in database; "" is where the
// profile's default puts it.
func (p *Pool) Query(ctx context.Context, connectionID, database, tab string, fn func(context.Context, driver.Conn) error) error {
	prof, ok := p.Profile(connectionID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotConnected, connectionID)
	}
	if database == "" {
		database = prof.Database
	}
	return p.with(ctx, key{conn: connectionID, database: database, tab: tab}, 1, fn)
}

func (p *Pool) with(ctx context.Context, k key, max int, fn func(context.Context, driver.Conn) error) error {
	ss, err := p.acquire(ctx, k, max)
	if err != nil {
		return err
	}
	defer p.release(k, ss)
	return fn(ctx, ss.conn)
}

func (p *Pool) slotLocked(k key, max int) *slot {
	s := p.slots[k]
	if s == nil {
		s = &slot{max: max, changed: make(chan struct{})}
		p.slots[k] = s
	}
	return s
}

func (s *slot) wake() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// acquire hands out an idle session, probing it first when it sat quiet,
// or opens one while the slot has room, or waits for one to free up.
// A session being probed is busy, so a request arriving meanwhile is never
// handed the socket the probe may condemn.
func (p *Pool) acquire(ctx context.Context, k key, max int) (*session, error) {
	for {
		p.mu.Lock()
		prof, ok := p.profiles[k.conn]
		if !ok {
			p.mu.Unlock()
			return nil, fmt.Errorf("%w: %s", ErrNotConnected, k.conn)
		}
		s := p.slotLocked(k, max)
		var picked *session
		for _, ss := range s.sessions {
			if !ss.busy {
				picked = ss
				break
			}
		}
		if picked != nil {
			picked.busy = true
			p.mu.Unlock()
			ok, lost := p.healthy(ctx, picked)
			if ok {
				return picked, nil
			}
			p.drop(k, s, picked, lost)
			continue
		}
		if len(s.sessions)+s.opening < s.max {
			s.opening++
			p.mu.Unlock()
			conn, err := p.open(ctx, prof.p, prof.password, k.database)
			p.mu.Lock()
			s.opening--
			if err != nil {
				s.wake()
				p.mu.Unlock()
				return nil, err
			}
			ss := &session{conn: conn, busy: true}
			if p.slots[k] != s {
				// Disconnected while opening.
				p.mu.Unlock()
				conn.Close()
				return nil, fmt.Errorf("%w: %s", ErrNotConnected, k.conn)
			}
			s.sessions = append(s.sessions, ss)
			p.mu.Unlock()
			return ss, nil
		}
		wait := s.changed
		p.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// healthy reports whether ss can be used. lost is the reason to tell the
// user when it died on its own; a probe failure is not told, since the
// replacement is the user's next request and nothing was lost to it.
func (p *Pool) healthy(ctx context.Context, ss *session) (ok bool, lost string) {
	if ss.conn.IsClosed() {
		return false, "The database connection closed unexpectedly."
	}
	if time.Since(ss.idleSince) < p.idleProbeAfter {
		return true, ""
	}
	conn := ss.conn
	pctx, cancel := context.WithTimeout(ctx, p.probeTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := conn.Ping(pctx)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			ss.idleSince = time.Now()
			return true, ""
		}
		return false, ""
	case <-pctx.Done():
		// A dead socket may not give the ping back for a while (pgx waits
		// for its cancel request first); its close waits for it, not us.
		go func() {
			<-done
			conn.Close()
		}()
		ss.conn = nil
		return false, ""
	}
}

// drop forgets ss by identity: a late close of a replaced session must
// never take its replacement with it (recall's false "Connection lost").
func (p *Pool) drop(k key, s *slot, ss *session, lost string) {
	conn := ss.conn
	p.mu.Lock()
	for i, x := range s.sessions {
		if x == ss {
			s.sessions = append(s.sessions[:i], s.sessions[i+1:]...)
			break
		}
	}
	if p.slots[k] == s {
		s.wake()
	}
	onLost := p.OnLost
	p.mu.Unlock()
	if conn != nil {
		go conn.Close()
	}
	if lost != "" && onLost != nil {
		onLost(k.conn, lost)
	}
}

func (p *Pool) release(k key, ss *session) {
	p.mu.Lock()
	s := p.slots[k]
	live := false
	if s != nil {
		for _, x := range s.sessions {
			live = live || x == ss
		}
	}
	if !live {
		// Its slot was closed while it was in use.
		p.mu.Unlock()
		ss.conn.Close()
		return
	}
	p.mu.Unlock()
	if ss.conn.IsClosed() {
		p.drop(k, s, ss, "The session was reset: the database connection closed.")
		return
	}
	p.mu.Lock()
	ss.busy = false
	ss.idleSince = time.Now()
	s.wake()
	p.mu.Unlock()
}
