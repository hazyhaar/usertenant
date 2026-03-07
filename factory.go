// CLAUDE:SUMMARY Built-in shard factories: local (read-write), readonly, and noop (disabled).
// CLAUDE:DEPENDS usertenant/dbopen
// CLAUDE:EXPORTS (package-private)
package tenant

import (
	"database/sql"
	"encoding/json"
	"path/filepath"

	"github.com/hazyhaar/usertenant/dbopen"
)

// localFactory opens a SQLite database at {dataDir}/{dossierID}.db (flat layout).
func localFactory(dataDir, dossierID, _ string, _ json.RawMessage) (*sql.DB, func(), error) {
	path := filepath.Join(dataDir, dossierID+".db")
	db, err := dbopen.Open(path)
	if err != nil {
		return nil, nil, err
	}

	return db, func() { db.Close() }, nil
}

// readonlyFactory opens a SQLite database in read-only mode.
func readonlyFactory(dataDir, dossierID, _ string, _ json.RawMessage) (*sql.DB, func(), error) {
	path := filepath.Join(dataDir, dossierID+".db")
	db, err := dbopen.Open(path, dbopen.WithReadOnly())
	if err != nil {
		return nil, nil, err
	}

	return db, func() { db.Close() }, nil
}

// noopFactory returns a nil *sql.DB. Any operation on the shard will fail
// with ErrShardUnavailable.
func noopFactory(_, _, _ string, _ json.RawMessage) (*sql.DB, func(), error) {
	return nil, func() {}, ErrShardUnavailable
}
