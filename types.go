package tenant

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ShardFactory constructs a *sql.DB for a given shard. It receives the full
// context needed to open the connection and returns the database handle along
// with a close function. The close function is called when the pool evicts
// or closes the connection.
type ShardFactory func(dataDir, userID, spaceID, endpoint string, config json.RawMessage) (db *sql.DB, close func(), err error)

// shardKey uniquely identifies a shard by user and space.
type shardKey struct {
	UserID  string
	SpaceID string
}

// shard represents a row from the shards table in the catalog.
type shard struct {
	UserID    string
	SpaceID   string
	Name      string
	Strategy  string
	Endpoint  string
	Config    json.RawMessage
	Status    string
	SizeBytes int64
	CreatedAt int64
	UpdatedAt int64
}

// fingerprint returns a string used for diffing during reload. If any of these
// fields change, the connection must be recreated.
func (s shard) fingerprint() string {
	return s.Strategy + "|" + s.Endpoint + "|" + string(s.Config) + "|" + s.Status
}

// entry is a live connection in the pool.
type entry struct {
	db       *sql.DB
	closeFn  func()
	lastUsed atomic.Int64 // epoch ms, updated on each Resolve()
	strategy string
	cancel   func()       // cancel function for attached watchers
}

// PoolStats exposes pool metrics for observability.
type PoolStats struct {
	OpenConns     int   `json:"open_conns"`
	TotalResolves int64 `json:"total_resolves"`
	CacheHits     int64 `json:"cache_hits"`
	CacheMisses   int64 `json:"cache_misses"`
	Evictions     int64 `json:"evictions"`
	FactoryErrors int64 `json:"factory_errors"`
	Reloads       int64 `json:"reloads"`
}

// Pool is the core shard router. It resolves (userID, spaceID) pairs to
// *sql.DB connections using a catalog database and registered factories.
type Pool struct {
	dataDir   string
	catalogDB *sql.DB
	factories map[string]ShardFactory

	mu        sync.RWMutex
	conns     map[shardKey]*entry
	shardSnap map[shardKey]shard

	idleTimeout time.Duration
	maxOpen     int
	logger      *slog.Logger

	// stats
	totalResolves atomic.Int64
	cacheHits     atomic.Int64
	cacheMisses   atomic.Int64
	evictions     atomic.Int64
	factoryErrors atomic.Int64
	reloads       atomic.Int64

	closed   atomic.Bool
	closeCh  chan struct{}
	closeOnce sync.Once
}

// Option configures the Pool.
type Option func(*Pool)

// WithIdleTimeout sets how long an idle connection is kept open before eviction.
// Default: 5 minutes.
func WithIdleTimeout(d time.Duration) Option {
	return func(p *Pool) {
		p.idleTimeout = d
	}
}

// WithMaxOpen sets the maximum number of simultaneously open shard connections.
// Default: 256.
func WithMaxOpen(n int) Option {
	return func(p *Pool) {
		if n > 0 {
			p.maxOpen = n
		}
	}
}

// WithLogger sets a structured logger for the pool. Default: slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(p *Pool) {
		p.logger = l
	}
}
