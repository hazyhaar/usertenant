// CLAUDE:SUMMARY Shard resolution with cache-first lookup, LRU eviction, and optional change-watching.
// CLAUDE:DEPENDS usertenant/watch
// CLAUDE:EXPORTS Resolve, ResolveWithOwner, ResolveWithWatch
package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/hazyhaar/usertenant/watch"
)

// Resolve returns the *sql.DB for the given dossierID. It uses a cache-first
// approach: if a connection is already open, it returns it immediately.
// Otherwise it looks up the shard in the in-memory snapshot, calls the
// appropriate factory, and caches the result.
//
// Resolve never reads from _catalog.db directly. The snapshot is maintained
// by Watch/Reload.
// CLAUDE:WARN Double-checked lock, may evict oldest connection, calls ShardFactory (filesystem/network).
func (p *Pool) Resolve(ctx context.Context, dossierID string) (*sql.DB, error) {
	return p.resolve(ctx, dossierID, "")
}

// ResolveWithOwner is like Resolve but verifies that the shard's recorded
// owner_id matches the provided ownerID. Returns ErrOwnershipMismatch if
// the owner does not match.
func (p *Pool) ResolveWithOwner(ctx context.Context, dossierID, ownerID string) (*sql.DB, error) {
	if ownerID == "" {
		return nil, fmt.Errorf("tenant: ownerID must not be empty")
	}
	return p.resolve(ctx, dossierID, ownerID)
}

// resolve is the shared implementation for Resolve and ResolveWithOwner.
// If ownerID is non-empty, ownership is checked against the catalog snapshot.
func (p *Pool) resolve(ctx context.Context, dossierID, ownerID string) (*sql.DB, error) {
	if p.closed.Load() {
		return nil, ErrPoolClosed
	}

	p.totalResolves.Add(1)

	// 1. Fast path: RLock, check cache + owner.
	p.mu.RLock()
	if e, ok := p.conns[dossierID]; ok {
		if ownerID != "" {
			if s, snapOK := p.shardSnap[dossierID]; snapOK && s.OwnerID != ownerID {
				p.mu.RUnlock()
				return nil, ErrOwnershipMismatch
			}
		}
		e.lastUsed.Store(time.Now().UnixMilli())
		p.mu.RUnlock()
		p.cacheHits.Add(1)
		if p.onShardEvent != nil {
			p.onShardEvent("shard.resolved", dossierID)
		}
		return e.db, nil
	}
	p.mu.RUnlock()

	p.cacheMisses.Add(1)

	// 2. Check snapshot for shard metadata.
	p.mu.RLock()
	s, ok := p.shardSnap[dossierID]
	p.mu.RUnlock()
	if !ok {
		return nil, ErrShardNotFound
	}

	// Check ownership.
	if ownerID != "" && s.OwnerID != ownerID {
		return nil, ErrOwnershipMismatch
	}

	// Check status.
	switch s.Status {
	case "deleted":
		return nil, ErrShardDeleted
	case "archived":
		if s.Strategy != "archived" {
			return nil, ErrShardArchived
		}
		// If strategy is "archived", let the factory handle it.
	}

	// 3. Slow path: Lock, double-check, create.
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check: another goroutine may have created it.
	if e, found := p.conns[dossierID]; found {
		e.lastUsed.Store(time.Now().UnixMilli())
		return e.db, nil
	}

	// 4. Check maxOpen, evict if needed.
	if len(p.conns) >= p.maxOpen {
		if !p.evictOldestLocked() {
			return nil, ErrPoolExhausted
		}
	}

	// 5. Call factory.
	factory, ok := p.factories[s.Strategy]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrFactoryNotFound, s.Strategy)
	}

	db, closeFn, err := factory(p.dataDir, s.ID, s.Endpoint, s.Config)
	if err != nil {
		p.factoryErrors.Add(1)
		if p.onShardEvent != nil {
			p.onShardEvent("shard.error", dossierID, slog.String("error", err.Error()))
		}
		return nil, fmt.Errorf("%w: %v", ErrFactoryFailed, err)
	}

	if p.onShardEvent != nil {
		p.onShardEvent("shard.opened", dossierID, slog.String("strategy", s.Strategy))
	}

	// 6. Store in cache.
	e := &entry{
		db:       db,
		closeFn:  closeFn,
		strategy: s.Strategy,
	}
	e.lastUsed.Store(time.Now().UnixMilli())
	p.conns[dossierID] = e

	return db, nil
}

// ResolveWithWatch is a variant of Resolve that attaches a watch.Watcher to
// the returned database. The watcher polls PRAGMA data_version and calls
// onChange when the database is modified by another connection.
//
// The watcher lifecycle is tied to the connection entry: if the entry is
// evicted, reloaded, or the pool is closed, the watcher is cancelled via
// context cancellation.
// CLAUDE:WARN Launches watcher goroutine. Silent no-op if already watching.
func (p *Pool) ResolveWithWatch(ctx context.Context, dossierID string, interval time.Duration, onChange func() error) (*sql.DB, error) {
	db, err := p.Resolve(ctx, dossierID)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	e, ok := p.conns[dossierID]
	if !ok {
		p.mu.Unlock()
		return db, nil
	}

	// If already watching, don't start another watcher.
	if e.cancel != nil {
		p.mu.Unlock()
		return db, nil
	}

	watchCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	p.mu.Unlock()

	w := watch.New(db, interval, onChange, p.logger)
	go func() {
		if err := w.Run(watchCtx); err != nil && watchCtx.Err() == nil {
			p.logger.Error("tenant: watcher failed", "dossier_id", dossierID, "error", err)
		}
	}()

	return db, nil
}

// evictOldestLocked evicts the idle connection with the oldest lastUsed time.
// Must be called with p.mu held (write lock). Returns true if a connection was
// evicted, false if all connections are too recent.
func (p *Pool) evictOldestLocked() bool {
	var oldestKey string
	var oldestTime int64 = 1<<63 - 1 // max int64

	for k, e := range p.conns {
		t := e.lastUsed.Load()
		if t < oldestTime {
			oldestTime = t
			oldestKey = k
		}
	}

	if oldestTime == 1<<63-1 {
		return false
	}

	p.closeEntryLocked(oldestKey)
	p.evictions.Add(1)
	if p.onShardEvent != nil {
		p.onShardEvent("shard.evicted", oldestKey, slog.String("reason", "lru"))
	}
	return true
}

// closeEntryLocked closes and removes an entry from the pool.
// Must be called with p.mu held (write lock).
func (p *Pool) closeEntryLocked(key string) {
	e, ok := p.conns[key]
	if !ok {
		return
	}
	if e.cancel != nil {
		e.cancel()
	}
	if e.closeFn != nil {
		e.closeFn()
	}
	delete(p.conns, key)
}
