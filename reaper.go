package tenant

import "time"

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
				"user_id", key.UserID, "space_id", key.SpaceID,
				"idle_ms", idle)
			p.closeEntryLocked(key)
			p.evictions.Add(1)
		}
	}
}
