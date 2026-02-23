package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// New creates a new Pool. It opens the catalog database, loads the initial
// shard snapshot, and registers the built-in factories (local, readonly, noop).
// The caller must call Close() when done.
func New(dataDir string, catalogDB *sql.DB, opts ...Option) (*Pool, error) {
	p := &Pool{
		dataDir:     dataDir,
		catalogDB:   catalogDB,
		factories:   make(map[string]ShardFactory),
		conns:       make(map[shardKey]*entry),
		shardSnap:   make(map[shardKey]shard),
		idleTimeout: 5 * time.Minute,
		maxOpen:     256,
		logger:      slog.Default(),
		closeCh:     make(chan struct{}),
	}

	for _, o := range opts {
		o(p)
	}

	// Register built-in factories.
	p.factories["local"] = localFactory
	p.factories["readonly"] = readonlyFactory
	p.factories["noop"] = noopFactory

	// Load initial snapshot.
	if err := p.Reload(context.Background()); err != nil {
		return nil, fmt.Errorf("tenant: initial reload: %w", err)
	}

	// Start the idle reaper.
	go p.reapLoop()

	return p, nil
}

// RegisterFactory registers a ShardFactory for the given strategy name.
// This must be called before Watch/Reload so that the factory is available
// when connections are created. It overwrites any existing factory for the
// same strategy.
func (p *Pool) RegisterFactory(strategy string, f ShardFactory) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.factories[strategy] = f
}

// Stats returns a snapshot of pool metrics.
func (p *Pool) Stats() PoolStats {
	p.mu.RLock()
	openConns := len(p.conns)
	p.mu.RUnlock()

	return PoolStats{
		OpenConns:     openConns,
		TotalResolves: p.totalResolves.Load(),
		CacheHits:     p.cacheHits.Load(),
		CacheMisses:   p.cacheMisses.Load(),
		Evictions:     p.evictions.Load(),
		FactoryErrors: p.factoryErrors.Load(),
		Reloads:       p.reloads.Load(),
	}
}

// Close shuts down the pool: closes all open connections and stops the reaper.
func (p *Pool) Close() error {
	var firstErr error
	p.closeOnce.Do(func() {
		p.closed.Store(true)
		close(p.closeCh)

		p.mu.Lock()
		defer p.mu.Unlock()

		for k, e := range p.conns {
			if e.cancel != nil {
				e.cancel()
			}
			if e.closeFn != nil {
				e.closeFn()
			}
			delete(p.conns, k)
		}
	})
	return firstErr
}
