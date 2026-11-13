package app

import (
	"context"
	"errors"
	"sync"

	"seeker/internal/driver"
	"seeker/internal/model"
	"seeker/internal/secret"
)

// Status is how far a profile's session is, as the sidebar shows it.
type Status int

const (
	Disconnected Status = iota
	Connecting
	Connected
	Failed
)

// connState is one profile's session state. The view reads it on the
// window's thread and the connect goroutine publishes into it, so its own
// mutex guards the fields a goroutine reaches.
type connState struct {
	mu        sync.Mutex
	status    Status
	info      driver.ServerInfo
	err       string
	done      chan struct{} // closed when the attempt in flight ends
	cancel    context.CancelFunc
	cancelled bool   // the user stopped the attempt, which is not a failure
	password  string // kept for this session when the profile saves none
}

func (c *connState) setPassword(pw string) {
	c.mu.Lock()
	c.password = pw
	c.mu.Unlock()
}

func (c *connState) snapshot() (Status, driver.ServerInfo, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status, c.info, c.err
}

func (a *App) conn(id string) *connState {
	if a.conns == nil {
		a.conns = map[string]*connState{}
	}
	c := a.conns[id]
	if c == nil {
		c = &connState{}
		a.conns[id] = c
	}
	return c
}

// SetSessionPassword is the password a profile keeps for this run only,
// for one that does not save it. The connection dialog hands it over
// through SaveProfile; a restored session or a password prompt uses this.
func (a *App) SetSessionPassword(id, password string) { a.conn(id).setPassword(password) }

// Status reports the profile's session state.
func (a *App) Status(id string) Status {
	s, _, _ := a.conn(id).snapshot()
	return s
}

// ServerInfo is what the profile's session reported when it connected.
func (a *App) ServerInfo(id string) (driver.ServerInfo, bool) {
	s, info, _ := a.conn(id).snapshot()
	return info, s == Connected
}

// ConnError is why the last connect failed, for the row's tooltip.
func (a *App) ConnError(id string) string {
	_, _, e := a.conn(id).snapshot()
	return e
}

// Connect opens the profile's session unless it is connecting or open.
func (a *App) Connect(id string) {
	p, ok := a.Profile(id)
	if !ok {
		return
	}
	a.connect(p)
}

func (a *App) connect(p model.Profile) {
	c := a.conn(p.ID)
	c.mu.Lock()
	if c.status == Connecting || c.status == Connected {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	c.status, c.err, c.cancelled = Connecting, "", false
	c.done = make(chan struct{})
	c.cancel = cancel
	done := c.done
	c.mu.Unlock()
	go a.runConnect(ctx, cancel, p, c, done)
}

// runConnect opens and pings a session the pool does not know yet. Until
// Adopt takes it, this goroutine owns it: a cancel or a later attempt makes
// the result stale, and the session closes here instead of landing in the
// pool after Disconnect.
func (a *App) runConnect(ctx context.Context, cancel context.CancelFunc, p model.Profile, c *connState, done chan struct{}) {
	defer cancel()
	password := a.password(p, c)
	conn, err := driver.Open(ctx, p, password, p.Database)
	var info driver.ServerInfo
	if err == nil {
		info, err = conn.Ping(ctx)
		if err != nil {
			conn.Close()
			conn = nil
		}
	}
	a.upd(func() { a.finishConnect(p, c, done, conn, password, info, err) })
}

// password is the one the dialog gave this session when there is one, else
// the keychain's item when the profile saves one. The session's comes first
// because the dialog writes the keychain in the background: a connect that
// started with a changed password would otherwise read the old item. A
// keychain read may wait on the user, so it happens off the window's thread.
func (a *App) password(p model.Profile, c *connState) string {
	c.mu.Lock()
	pw := c.password
	c.mu.Unlock()
	if pw != "" {
		return pw
	}
	if p.SavePassword {
		if pw, ok, err := secret.Load(p.ID, p.Username); err == nil && ok {
			return pw
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.password
}

func (a *App) finishConnect(p model.Profile, c *connState, done chan struct{}, conn driver.Conn, password string, info driver.ServerInfo, err error) {
	c.mu.Lock()
	stale := c.done != done
	if !stale {
		if err != nil {
			c.status, c.err = Failed, err.Error()
		} else {
			c.status, c.info = Connected, info
			c.err = ""
		}
		close(done)
		c.done, c.cancel = nil, nil
	}
	c.mu.Unlock()
	if stale {
		if conn != nil {
			go conn.Close()
		}
		return
	}
	if err != nil {
		a.tree.errs[Node{Kind: KindConnection, Conn: p.ID}] = err.Error()
		return
	}
	delete(a.tree.errs, Node{Kind: KindConnection, Conn: p.ID})
	a.pl.Adopt(p, password, conn)
}

// CancelConnect stops the attempt in flight. The session it may have just
// opened closes itself when it lands; waiting on it would keep the sidebar
// spinning.
func (a *App) CancelConnect(id string) {
	c := a.conn(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status != Connecting {
		return
	}
	a.stopAttemptLocked(c)
	c.status, c.err, c.cancelled = Disconnected, "", true
	// The row stays open and empty, and opening it again retries: without
	// a cached empty list the outline would ask for children every frame
	// and start the connection right back up.
	a.tree.kids[Node{Kind: KindConnection, Conn: id}] = []Node{}
}

// Reconnect drops the session and opens a fresh one with the saved profile.
func (a *App) Reconnect(id string) {
	p, ok := a.Profile(id)
	if !ok {
		return
	}
	c := a.conn(id)
	c.mu.Lock()
	if c.status == Connecting {
		a.stopAttemptLocked(c)
	}
	c.status, c.err = Disconnected, ""
	c.mu.Unlock()
	// The old sessions go now, not when the new one lands: a failed connect
	// must not leave sessions open under the profile it replaced.
	a.pl.Disconnect(id)
	a.dropConn(id)
	a.bump()
	a.connect(p)
}

// stopAttemptLocked cancels the attempt and wakes whoever waits for it.
// Clearing done tells the attempt's own result that it is stale.
func (a *App) stopAttemptLocked(c *connState) {
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	if c.done != nil {
		close(c.done)
		c.done = nil
	}
}

// connectionLost is the pool's report that a session died under the app
// (server restart, a socket a firewall reaped). Its session state is gone,
// including anything the user set on it, so the row drops to disconnected
// and the toast offers to reconnect.
func (a *App) connectionLost(id, reason string) {
	a.upd(func() {
		p, ok := a.Profile(id)
		if !ok {
			return
		}
		c := a.conn(id)
		c.mu.Lock()
		c.status, c.err, c.info = Disconnected, "", driver.ServerInfo{}
		c.mu.Unlock()
		a.dropConn(id)
		// An open row would ask for its children and reconnect on its own;
		// the error row waits for the user, who closes and opens it or takes
		// the toast's button.
		a.tree.errs[Node{Kind: KindConnection, Conn: id}] = "Connection lost: " + reason
		a.bump()
		a.toastAction("Connection lost: "+reason, "Reconnect", func() { a.Reconnect(p.ID) })
	})
}

// errCancelled is a connect the user stopped; the tree drops it silently.
var errCancelled = errors.New("connect cancelled")

// waitConnected blocks a worker until the attempt in flight on c ends.
func waitConnected(c *connState) error {
	for {
		c.mu.Lock()
		switch c.status {
		case Connected:
			c.mu.Unlock()
			return nil
		case Failed:
			err := errors.New(c.err)
			c.mu.Unlock()
			return err
		default:
			done, cancelled := c.done, c.cancelled
			c.mu.Unlock()
			if done == nil {
				if cancelled {
					return errCancelled
				}
				return errors.New("pool: not connected")
			}
			<-done
		}
	}
}
