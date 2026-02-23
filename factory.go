package tenant

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hazyhaar/usertenant/dbopen"
)

// localFactory opens a SQLite database at {dataDir}/{userID}/{spaceID}.db.
// It creates the user directory if it does not exist.
func localFactory(dataDir, userID, spaceID, _ string, _ json.RawMessage) (*sql.DB, func(), error) {
	dir := filepath.Join(dataDir, userID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("tenant: mkdir %s: %w", dir, err)
	}

	path := filepath.Join(dir, spaceID+".db")
	db, err := dbopen.Open(path)
	if err != nil {
		return nil, nil, err
	}

	return db, func() { db.Close() }, nil
}

// readonlyFactory opens a SQLite database in read-only mode.
func readonlyFactory(dataDir, userID, spaceID, _ string, _ json.RawMessage) (*sql.DB, func(), error) {
	path := filepath.Join(dataDir, userID, spaceID+".db")
	db, err := dbopen.Open(path, dbopen.WithReadOnly())
	if err != nil {
		return nil, nil, err
	}

	return db, func() { db.Close() }, nil
}

// noopFactory returns a nil *sql.DB. Any operation on the space will fail
// with ErrSpaceUnavailable.
func noopFactory(_, _, _, _ string, _ json.RawMessage) (*sql.DB, func(), error) {
	return nil, func() {}, ErrSpaceUnavailable
}
