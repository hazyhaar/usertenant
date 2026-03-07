// CLAUDE:SUMMARY CRUD operations on shards in the catalog database.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS CreateShard, DeleteShard, SetStrategy, EnsureShard, CreateSpace, DeleteSpace
package tenant

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// CreateShard inserts a new shard row into the catalog with strategy "local".
// The dossierID is provided by the caller (UUID v7). ownerID is stored for
// audit purposes only — it is not used for routing.
func (p *Pool) CreateShard(ctx context.Context, dossierID, ownerID, name string) error {
	now := time.Now().UnixMilli()

	_, err := p.catalogDB.ExecContext(ctx,
		`INSERT INTO shards (id, owner_id, name, strategy, endpoint, config, status, size_bytes, created_at, updated_at)
		 VALUES (?, ?, ?, 'local', '', '{}', 'active', 0, ?, ?)`,
		dossierID, ownerID, name, now, now)
	if err != nil {
		return fmt.Errorf("tenant: create shard: %w", err)
	}

	if p.onShardEvent != nil {
		p.onShardEvent("shard.created", dossierID,
			slog.String("owner_id", ownerID),
			slog.String("name", name))
	}

	// Reload snapshot so that Resolve sees the new shard immediately.
	// Watch (PRAGMA data_version) does not detect same-connection writes.
	return p.Reload(ctx)
}

// SetStrategy updates a shard's routing strategy, endpoint, and config.
// The Watch/Reload cycle will detect the change and close the old connection.
// The next Resolve will create a new connection using the new factory.
func (p *Pool) SetStrategy(ctx context.Context, dossierID, strategy, endpoint string, config json.RawMessage) error {
	now := time.Now().UnixMilli()
	if config == nil {
		config = json.RawMessage("{}")
	}

	res, err := p.catalogDB.ExecContext(ctx,
		`UPDATE shards SET strategy = ?, endpoint = ?, config = ?, updated_at = ?
		 WHERE id = ?`,
		strategy, endpoint, string(config), now, dossierID)
	if err != nil {
		return fmt.Errorf("tenant: set strategy: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrShardNotFound
	}

	return p.Reload(ctx)
}

// DeleteShard marks a shard as deleted, closes its connection if open, and
// removes the .db file if the strategy is "local".
// CLAUDE:WARN Takes mu.Lock, closes connection, deletes .db files from disk. Silent ignore on file removal errors.
func (p *Pool) DeleteShard(ctx context.Context, dossierID string) error {
	now := time.Now().UnixMilli()

	// Close the connection first, if open.
	p.mu.Lock()
	p.closeEntryLocked(dossierID)
	p.mu.Unlock()

	// Mark as deleted in catalog.
	res, err := p.catalogDB.ExecContext(ctx,
		`UPDATE shards SET status = 'deleted', updated_at = ? WHERE id = ?`,
		now, dossierID)
	if err != nil {
		return fmt.Errorf("tenant: delete shard: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrShardNotFound
	}

	// Remove local .db file if it exists (flat layout: {dataDir}/{dossierID}.db).
	path := filepath.Join(p.dataDir, dossierID+".db")
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")

	if p.onShardEvent != nil {
		p.onShardEvent("shard.deleted", dossierID)
	}

	return p.Reload(ctx)
}

// EnsureShard creates the shard if it doesn't exist in the catalog.
// If the shard already exists (same PK), it is a silent no-op.
// Use this instead of CreateShard when the caller doesn't know whether
// the shard was already registered (e.g. siftrag dossier creation).
func (p *Pool) EnsureShard(ctx context.Context, dossierID, ownerID, name string) error {
	now := time.Now().UnixMilli()
	res, err := p.catalogDB.ExecContext(ctx,
		`INSERT OR IGNORE INTO shards (id, owner_id, name, strategy, endpoint, config, status, size_bytes, created_at, updated_at)
		 VALUES (?, ?, ?, 'local', '', '{}', 'active', 0, ?, ?)`,
		dossierID, ownerID, name, now, now)
	if err != nil {
		return fmt.Errorf("tenant: ensure shard: %w", err)
	}
	if p.onShardEvent != nil {
		if n, _ := res.RowsAffected(); n > 0 {
			p.onShardEvent("shard.created", dossierID,
				slog.String("owner_id", ownerID),
				slog.String("name", name))
		}
	}
	return p.Reload(ctx)
}

// Legacy aliases for backward compatibility during migration.

// CreateSpace is a legacy alias. Prefer CreateShard.
func (p *Pool) CreateSpace(ctx context.Context, userID, spaceID, name string) error {
	return p.CreateShard(ctx, spaceID, userID, name)
}

// DeleteSpace is a legacy alias. Prefer DeleteShard.
func (p *Pool) DeleteSpace(ctx context.Context, userID, spaceID string) error {
	return p.DeleteShard(ctx, spaceID)
}
