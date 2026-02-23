package tenant

import (
	"context"
	"database/sql"
	"fmt"
)

const catalogDDL = `
CREATE TABLE IF NOT EXISTS users (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'active',
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS shards (
    user_id    TEXT NOT NULL,
    space_id   TEXT NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    strategy   TEXT NOT NULL DEFAULT 'local',
    endpoint   TEXT NOT NULL DEFAULT '',
    config     TEXT NOT NULL DEFAULT '{}',
    status     TEXT NOT NULL DEFAULT 'active',
    size_bytes INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, space_id)
);

CREATE INDEX IF NOT EXISTS idx_shards_user ON shards(user_id);
CREATE INDEX IF NOT EXISTS idx_shards_status ON shards(status);
`

// InitCatalog creates the users and shards tables in the catalog database
// if they do not already exist.
func InitCatalog(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, catalogDDL)
	if err != nil {
		return fmt.Errorf("tenant: init catalog: %w", err)
	}
	return nil
}
