package tenant

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Reload reads the shards table from the catalog database, computes a diff
// against the current in-memory snapshot, and closes connections whose
// fingerprint (strategy + endpoint + config + status) has changed.
//
// New or unchanged shards are simply stored in the snapshot; their connections
// are created lazily on the next Resolve().
func (p *Pool) Reload(ctx context.Context) error {
	rows, err := p.catalogDB.QueryContext(ctx,
		`SELECT user_id, space_id, name, strategy, endpoint, config, status, size_bytes, created_at, updated_at
		 FROM shards`)
	if err != nil {
		return fmt.Errorf("tenant: reload query: %w", err)
	}
	defer rows.Close()

	newSnap := make(map[shardKey]shard)
	for rows.Next() {
		var s shard
		var configStr string
		if err := rows.Scan(&s.UserID, &s.SpaceID, &s.Name, &s.Strategy, &s.Endpoint,
			&configStr, &s.Status, &s.SizeBytes, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return fmt.Errorf("tenant: reload scan: %w", err)
		}
		s.Config = json.RawMessage(configStr)
		newSnap[shardKey{UserID: s.UserID, SpaceID: s.SpaceID}] = s
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("tenant: reload rows: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Diff: close connections whose fingerprint changed or were removed.
	for key, e := range p.conns {
		newShard, exists := newSnap[key]
		if !exists {
			// Shard removed from catalog.
			p.logger.Debug("tenant: shard removed, closing connection",
				"user_id", key.UserID, "space_id", key.SpaceID)
			p.closeEntryLocked(key)
			continue
		}

		oldShard, oldExists := p.shardSnap[key]
		if oldExists && oldShard.fingerprint() != newShard.fingerprint() {
			// Fingerprint changed — close old connection so next Resolve
			// rebuilds with the new factory/config.
			p.logger.Debug("tenant: shard changed, closing connection",
				"user_id", key.UserID, "space_id", key.SpaceID,
				"old_strategy", e.strategy, "new_strategy", newShard.Strategy)
			p.closeEntryLocked(key)
		}
	}

	p.shardSnap = newSnap
	p.reloads.Add(1)
	return nil
}

// Watch starts a polling loop that checks PRAGMA data_version on the catalog
// database and calls Reload when a change is detected. It blocks until ctx
// is cancelled.
func (p *Pool) Watch(ctx context.Context, interval time.Duration) error {
	var lastVersion int64
	if err := p.catalogDB.QueryRowContext(ctx, "PRAGMA data_version").Scan(&lastVersion); err != nil {
		return fmt.Errorf("tenant: watch initial data_version: %w", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.closeCh:
			return nil
		case <-ticker.C:
			var v int64
			if err := p.catalogDB.QueryRowContext(ctx, "PRAGMA data_version").Scan(&v); err != nil {
				p.logger.Warn("tenant: watch poll failed", "error", err)
				continue
			}
			if v != lastVersion {
				lastVersion = v
				if err := p.Reload(ctx); err != nil {
					p.logger.Error("tenant: reload failed", "error", err)
				}
			}
		}
	}
}
