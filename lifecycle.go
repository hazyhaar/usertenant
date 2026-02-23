package tenant

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CreateSpace inserts a new shard row into the catalog with strategy "local".
// The user directory is created if it does not exist. The actual .db file is
// created lazily on the first Resolve.
func (p *Pool) CreateSpace(ctx context.Context, userID, spaceID, name string) error {
	now := time.Now().UnixMilli()

	_, err := p.catalogDB.ExecContext(ctx,
		`INSERT INTO shards (user_id, space_id, name, strategy, endpoint, config, status, size_bytes, created_at, updated_at)
		 VALUES (?, ?, ?, 'local', '', '{}', 'active', 0, ?, ?)`,
		userID, spaceID, name, now, now)
	if err != nil {
		return fmt.Errorf("tenant: create space: %w", err)
	}

	// Ensure user directory exists.
	dir := filepath.Join(p.dataDir, userID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("tenant: mkdir %s: %w", dir, err)
	}

	return nil
}

// CreateUser inserts a new user row into the catalog.
func (p *Pool) CreateUser(ctx context.Context, userID, name string) error {
	now := time.Now().UnixMilli()

	_, err := p.catalogDB.ExecContext(ctx,
		`INSERT INTO users (id, name, status, created_at) VALUES (?, ?, 'active', ?)`,
		userID, name, now)
	if err != nil {
		return fmt.Errorf("tenant: create user: %w", err)
	}

	return nil
}

// SetStrategy updates a shard's routing strategy, endpoint, and config.
// The Watch/Reload cycle will detect the change and close the old connection.
// The next Resolve will create a new connection using the new factory.
//
// If the shard is being migrated, the caller is responsible for copying data
// before changing the strategy.
func (p *Pool) SetStrategy(ctx context.Context, userID, spaceID, strategy, endpoint string, config json.RawMessage) error {
	now := time.Now().UnixMilli()
	if config == nil {
		config = json.RawMessage("{}")
	}

	res, err := p.catalogDB.ExecContext(ctx,
		`UPDATE shards SET strategy = ?, endpoint = ?, config = ?, updated_at = ?
		 WHERE user_id = ? AND space_id = ?`,
		strategy, endpoint, string(config), now, userID, spaceID)
	if err != nil {
		return fmt.Errorf("tenant: set strategy: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSpaceNotFound
	}

	return nil
}

// DeleteSpace marks a shard as deleted, closes its connection if open, and
// removes the .db file if the strategy is "local".
func (p *Pool) DeleteSpace(ctx context.Context, userID, spaceID string) error {
	now := time.Now().UnixMilli()

	// Close the connection first, if open.
	key := shardKey{UserID: userID, SpaceID: spaceID}
	p.mu.Lock()
	p.closeEntryLocked(key)
	p.mu.Unlock()

	// Mark as deleted in catalog.
	res, err := p.catalogDB.ExecContext(ctx,
		`UPDATE shards SET status = 'deleted', updated_at = ? WHERE user_id = ? AND space_id = ?`,
		now, userID, spaceID)
	if err != nil {
		return fmt.Errorf("tenant: delete space: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSpaceNotFound
	}

	// Remove local .db file if it exists.
	path := filepath.Join(p.dataDir, userID, spaceID+".db")
	_ = os.Remove(path)
	// Also remove WAL and SHM files.
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")

	return nil
}
