// CLAUDE:SUMMARY Catalog DDL (shards table) and InitCatalog bootstrap function.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS InitCatalog
package tenant

import (
	"context"
	"database/sql"
	"fmt"
)

const catalogDDL = `
CREATE TABLE IF NOT EXISTS shards (
    id         TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL DEFAULT '',
    strategy   TEXT NOT NULL DEFAULT 'local',
    endpoint   TEXT NOT NULL DEFAULT '',
    config     TEXT NOT NULL DEFAULT '{}',
    status     TEXT NOT NULL DEFAULT 'active',
    size_bytes INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_shards_owner ON shards(owner_id);
CREATE INDEX IF NOT EXISTS idx_shards_status ON shards(status);
`

// InitCatalog creates the shards table in the catalog database
// if it does not already exist.
func InitCatalog(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, catalogDDL)
	if err != nil {
		return fmt.Errorf("tenant: init catalog: %w", err)
	}
	return nil
}
