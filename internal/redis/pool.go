package redis

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"seeker/internal/model"
)

// Config is what a client needs of a profile and its password.
type Config struct {
	ID       string
	Host     string
	Port     int
	Username string
	Password string
	SSL      bool
	// Timeout bounds the dial and the first PING.
	Timeout   time.Duration
	DefaultDB int
}

// ConfigOf is the Config of a saved Redis profile.
func ConfigOf(p model.Profile, password string) Config {
	return Config{
		ID: p.ID, Host: p.Host, Port: p.Port, Username: p.Username, Password: password, SSL: p.SSL,
		Timeout: time.Duration(max(1, p.ConnectTimeoutSecs)) * time.Second, DefaultDB: p.DefaultDB,
	}
}

// Info is what the server reported when the connection opened.
type Info struct {
	Product, Version string
	// Database is the profile's own, as "db0".
	Database string
}

// Pool keeps one client per (connection, logical database): a database is
// chosen per socket, so sharing one across databases would race on SELECT.
// Each client pools its own sockets, so the console and the key browser do
// not queue behind each other. A profile's config stays until it is closed,
// so a request for another database can open its client.
type Pool struct {
	mu      sync.Mutex
	cfgs    map[string]Config
	clients map[string]*goredis.Client
}

func NewPool() *Pool {
	return &Pool{cfgs: map[string]Config{}, clients: map[string]*goredis.Client{}}
}

func clientKey(id string, db int) string { return strconv.Itoa(len(id)) + ":" + id + strconv.Itoa(db) }

func newClient(cfg Config, db int) *goredis.Client {
	opt := &goredis.Options{
		Addr:         net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Username:     cfg.Username,
		Password:     cfg.Password,
		DB:           db,
		Protocol:     2, // arrays, not maps: the console shows replies as RESP2 does
		DialTimeout:  cfg.Timeout,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		MaxRetries:   1,
		PoolSize:     4,
	}
	if cfg.SSL {
		opt.TLSConfig = &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	}
	return goredis.NewClient(opt)
}

// connect opens a client and proves it with a PING, so a wrong password or
// host fails here and not on the first key list.
func connect(ctx context.Context, cfg Config, db int) (*goredis.Client, error) {
	c := newClient(cfg, db)
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

var versionRe = regexp.MustCompile(`(?m)^redis_version:(.+)$`)

func serverInfo(ctx context.Context, c *goredis.Client, db int) Info {
	info := Info{Product: "Redis", Database: "db" + strconv.Itoa(db)}
	if s, err := c.Info(ctx, "server").Result(); err == nil {
		if m := versionRe.FindStringSubmatch(s); m != nil {
			info.Version = strings.TrimSpace(m[1])
		}
	}
	return info
}

// Open connects the profile's default database, replacing a connection open
// under the same id.
func (p *Pool) Open(ctx context.Context, cfg Config) (Info, error) {
	p.Close(cfg.ID)
	c, err := connect(ctx, cfg, cfg.DefaultDB)
	if err != nil {
		return Info{}, fmt.Errorf("Redis connection failed: %w", err)
	}
	p.mu.Lock()
	p.cfgs[cfg.ID] = cfg
	p.clients[clientKey(cfg.ID, cfg.DefaultDB)] = c
	p.mu.Unlock()
	return serverInfo(ctx, c, cfg.DefaultDB), nil
}

// Test connects, reads the version and hangs up, keeping nothing.
func Test(ctx context.Context, cfg Config) (Info, error) {
	c, err := connect(ctx, cfg, cfg.DefaultDB)
	if err != nil {
		return Info{}, err
	}
	defer c.Close()
	return serverInfo(ctx, c, cfg.DefaultDB), nil
}

// ErrNotOpen is a request for a connection that is not open.
var ErrNotOpen = errors.New("Redis connection is not open")

// Client is the client of one logical database, opened on first use.
func (p *Pool) Client(ctx context.Context, id string, db int) (*goredis.Client, error) {
	key := clientKey(id, db)
	p.mu.Lock()
	if c := p.clients[key]; c != nil {
		p.mu.Unlock()
		return c, nil
	}
	cfg, ok := p.cfgs[id]
	p.mu.Unlock()
	if !ok {
		return nil, ErrNotOpen
	}
	c, err := connect(ctx, cfg, db)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Closed while it was opening, or opened by a request that got there
	// first: this one must not outlive the connection.
	if cur, ok := p.cfgs[id]; !ok || cur != cfg {
		c.Close()
		return nil, ErrNotOpen
	}
	if other := p.clients[key]; other != nil {
		c.Close()
		return other, nil
	}
	p.clients[key] = c
	return c, nil
}

// Server is the client for server-wide questions (CONFIG, INFO): the
// profile's own database, whose client is open.
func (p *Pool) Server(ctx context.Context, id string) (*goredis.Client, error) {
	p.mu.Lock()
	cfg, ok := p.cfgs[id]
	p.mu.Unlock()
	if !ok {
		return nil, ErrNotOpen
	}
	return p.Client(ctx, id, cfg.DefaultDB)
}

// Connected reports an open connection.
func (p *Pool) Connected(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.cfgs[id]
	return ok
}

// Close hangs up every client of the connection.
func (p *Pool) Close(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cfgs, id)
	prefix := strconv.Itoa(len(id)) + ":" + id
	for k, c := range p.clients {
		if strings.HasPrefix(k, prefix) {
			delete(p.clients, k)
			c.Close()
		}
	}
}

// CloseAll hangs up everything, as the app does on exit.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	ids := make([]string, 0, len(p.cfgs))
	for id := range p.cfgs {
		ids = append(ids, id)
	}
	p.mu.Unlock()
	for _, id := range ids {
		p.Close(id)
	}
}
