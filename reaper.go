// CLAUDE:SUMMARY Background goroutine that evicts idle shard connections exceeding the timeout.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS (package-private)
package tenant

import (
	"log/slog"
	"time"
)

// reapLoop runs in a goroutine and periodically evicts idle connections.
// It runs every idleTimeout/2 and closes connections whose lastUsed time
// exceeds idleTimeout. Connections with an attached watcher (cancel != nil)
// are not reaped.
func (p *Pool) reapLoop() {
	interval := p.idleTimeout / 2
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.closeCh:
			return
		case <-ticker.C:
			p.reap()
		}
	}
}

// CLAUDE:WARN Takes mu.Lock, closes idle connections. Entries with active watchers are never reaped.
func (p *Pool) reap() {
	now := time.Now().UnixMilli()
	threshold := p.idleTimeout.Milliseconds()

	p.mu.Lock()
	defer p.mu.Unlock()

	for key, e := range p.conns {
		// Don't reap entries with active watchers.
		if e.cancel != nil {
			continue
		}

		idle := now - e.lastUsed.Load()
		if idle > threshold {
			p.logger.Debug("tenant: reaping idle shard",
				"dossier_id", key,
				"idle_ms", idle)
			p.closeEntryLocked(key)
			p.evictions.Add(1)
			if p.onShardEvent != nil {
				p.onShardEvent("shard.evicted", key, slog.String("reason", "idle"))
			}
		}
	}
}
