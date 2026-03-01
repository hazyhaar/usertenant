package tenant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hazyhaar/usertenant/dbopen"
)

// testCatalog creates a temporary directory and a catalog database
// initialized with the schema. Returns (dataDir, catalogDB).
func testCatalog(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dataDir := t.TempDir()

	catalogPath := filepath.Join(dataDir, "_catalog.db")
	db, err := dbopen.Open(catalogPath)
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := InitCatalog(context.Background(), db); err != nil {
		t.Fatalf("init catalog: %v", err)
	}

	return dataDir, db
}

func insertShard(t *testing.T, db *sql.DB, dossierID, strategy string) {
	t.Helper()
	now := time.Now().UnixMilli()
	_, err := db.Exec(
		`INSERT INTO shards (id, owner_id, name, strategy, endpoint, config, status, size_bytes, created_at, updated_at)
		 VALUES (?, '', '', ?, '', '{}', 'active', 0, ?, ?)`,
		dossierID, strategy, now, now)
	if err != nil {
		t.Fatalf("insert shard: %v", err)
	}
}

func TestInitCatalog(t *testing.T) {
	// WHAT: InitCatalog creates the shards table.
	// WHY: Catalog schema is required for all pool operations.
	dataDir := t.TempDir()
	db, err := dbopen.Open(filepath.Join(dataDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = InitCatalog(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}

	// Verify shards table exists.
	var name string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='shards'").Scan(&name)
	if err != nil {
		t.Fatalf("shards table not found: %v", err)
	}

	// Idempotent: calling again should not error.
	err = InitCatalog(context.Background(), db)
	if err != nil {
		t.Fatalf("second init should be idempotent: %v", err)
	}
}

func TestPoolNewAndClose(t *testing.T) {
	// WHAT: Pool creation and graceful shutdown.
	// WHY: Lifecycle must be clean (no leaks).
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB, WithIdleTimeout(time.Minute), WithMaxOpen(10))
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}

	stats := pool.Stats()
	if stats.OpenConns != 0 {
		t.Errorf("expected 0 open conns, got %d", stats.OpenConns)
	}

	err = pool.Close()
	if err != nil {
		t.Fatalf("close pool: %v", err)
	}

	// Operations after close should fail.
	_, err = pool.Resolve(context.Background(), "nonexistent")
	if !errors.Is(err, ErrPoolClosed) {
		t.Errorf("expected ErrPoolClosed, got %v", err)
	}
}

func TestResolve_ByDossierID(t *testing.T) {
	// WHAT: Resolve returns a DB for a known dossierID.
	// WHY: Core routing — dossierID is the universal key.
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "dossier-001", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	db, err := pool.Resolve(context.Background(), "dossier-001")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if db == nil {
		t.Fatal("expected non-nil db")
	}

	// Verify the .db file was created (flat layout).
	path := filepath.Join(dataDir, "dossier-001.db")
	_, err = os.Stat(path)
	if err != nil {
		t.Fatalf("expected .db file at %s: %v", path, err)
	}

	// Second resolve should hit cache.
	db2, err := pool.Resolve(context.Background(), "dossier-001")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if db != db2 {
		t.Error("expected same *sql.DB instance from cache")
	}

	stats := pool.Stats()
	if stats.CacheHits != 1 {
		t.Errorf("expected 1 cache hit, got %d", stats.CacheHits)
	}
	if stats.CacheMisses != 1 {
		t.Errorf("expected 1 cache miss, got %d", stats.CacheMisses)
	}
}

func TestResolve_NotFound(t *testing.T) {
	// WHAT: Resolve on unknown dossierID returns ErrShardNotFound.
	// WHY: Unknown dossierIDs must fail cleanly.
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = pool.Resolve(context.Background(), "nonexistent")
	if !errors.Is(err, ErrShardNotFound) {
		t.Errorf("expected ErrShardNotFound, got %v", err)
	}
}

func TestResolve_Deleted(t *testing.T) {
	// WHAT: Resolve on deleted shard returns ErrShardDeleted.
	// WHY: Deleted shards must be inaccessible.
	dataDir, catalogDB := testCatalog(t)

	now := time.Now().UnixMilli()
	_, err := catalogDB.Exec(
		`INSERT INTO shards (id, owner_id, name, strategy, endpoint, config, status, size_bytes, created_at, updated_at)
		 VALUES ('del-001', '', '', 'local', '', '{}', 'deleted', 0, ?, ?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = pool.Resolve(context.Background(), "del-001")
	if !errors.Is(err, ErrShardDeleted) {
		t.Errorf("expected ErrShardDeleted, got %v", err)
	}
}

func TestResolveNoop(t *testing.T) {
	// WHAT: Resolve on noop strategy returns ErrFactoryFailed.
	// WHY: Disabled shards should fail at factory level.
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "noop-001", "noop")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = pool.Resolve(context.Background(), "noop-001")
	if !errors.Is(err, ErrFactoryFailed) {
		t.Errorf("expected ErrFactoryFailed, got %v", err)
	}
}

func TestResolveUnknownStrategy(t *testing.T) {
	// WHAT: Resolve with unknown strategy returns ErrFactoryNotFound.
	// WHY: Missing factories must be reported.
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "quantum-001", "quantum")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = pool.Resolve(context.Background(), "quantum-001")
	if !errors.Is(err, ErrFactoryNotFound) {
		t.Errorf("expected ErrFactoryNotFound, got %v", err)
	}
}

func TestCreateShard_And_Resolve(t *testing.T) {
	// WHAT: CreateShard then Resolve returns a working DB.
	// WHY: Full create→resolve lifecycle.
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx := context.Background()

	err = pool.CreateShard(ctx, "dossier-a", "owner-1", "My Dossier")
	if err != nil {
		t.Fatal(err)
	}

	err = pool.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}

	db, err := pool.Resolve(ctx, "dossier-a")
	if err != nil {
		t.Fatalf("resolve after create: %v", err)
	}
	if db == nil {
		t.Fatal("expected non-nil db")
	}

	// Verify the flat path.
	path := filepath.Join(dataDir, "dossier-a.db")
	_, err = os.Stat(path)
	if err != nil {
		t.Fatalf("expected .db file at %s: %v", path, err)
	}
}

func TestDeleteShard(t *testing.T) {
	// WHAT: DeleteShard soft-deletes and removes the file.
	// WHY: Deletion must be clean (no orphaned files).
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Create, reload, resolve (creates the file).
	err = pool.CreateShard(ctx, "dossier-del", "owner-1", "Delete Me")
	if err != nil {
		t.Fatal(err)
	}
	err = pool.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Resolve(ctx, "dossier-del")
	if err != nil {
		t.Fatal(err)
	}

	// Delete.
	err = pool.DeleteShard(ctx, "dossier-del")
	if err != nil {
		t.Fatal(err)
	}
	err = pool.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Resolve should fail.
	_, err = pool.Resolve(ctx, "dossier-del")
	if !errors.Is(err, ErrShardDeleted) {
		t.Errorf("expected ErrShardDeleted, got %v", err)
	}

	// .db file should be removed.
	path := filepath.Join(dataDir, "dossier-del.db")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected .db file to be removed, stat: %v", err)
	}
}

func TestDeleteShard_NotFound(t *testing.T) {
	// WHAT: DeleteShard on unknown dossierID returns ErrShardNotFound.
	// WHY: Deleting nonexistent shards must fail cleanly.
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	err = pool.DeleteShard(context.Background(), "ghost")
	if !errors.Is(err, ErrShardNotFound) {
		t.Errorf("expected ErrShardNotFound, got %v", err)
	}
}

func TestSetStrategy(t *testing.T) {
	// WHAT: SetStrategy changes a shard's routing strategy.
	// WHY: Strategy changes must persist and be detected by reload.
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx := context.Background()

	err = pool.CreateShard(ctx, "dossier-routing", "owner-1", "test")
	if err != nil {
		t.Fatal(err)
	}
	err = pool.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}

	err = pool.SetStrategy(ctx, "dossier-routing", "readonly", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	var strategy string
	err = catalogDB.QueryRow("SELECT strategy FROM shards WHERE id = 'dossier-routing'").Scan(&strategy)
	if err != nil {
		t.Fatal(err)
	}
	if strategy != "readonly" {
		t.Errorf("expected strategy 'readonly', got %q", strategy)
	}

	// SetStrategy on nonexistent shard should fail.
	err = pool.SetStrategy(ctx, "ghost", "local", "", nil)
	if !errors.Is(err, ErrShardNotFound) {
		t.Errorf("expected ErrShardNotFound, got %v", err)
	}
}

func TestMaxOpenEviction(t *testing.T) {
	// WHAT: When maxOpen is reached, the oldest connection is evicted.
	// WHY: Pool must respect connection limits.
	dataDir, catalogDB := testCatalog(t)

	insertShard(t, catalogDB, "s1", "local")
	insertShard(t, catalogDB, "s2", "local")
	insertShard(t, catalogDB, "s3", "local")

	pool, err := New(dataDir, catalogDB, WithMaxOpen(2), WithIdleTimeout(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if _, err := pool.Resolve(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err := pool.Resolve(context.Background(), "s2"); err != nil {
		t.Fatal(err)
	}

	// Third should evict the oldest (s1).
	time.Sleep(time.Millisecond)
	if _, err := pool.Resolve(context.Background(), "s3"); err != nil {
		t.Fatal(err)
	}

	stats := pool.Stats()
	if stats.OpenConns != 2 {
		t.Errorf("expected 2 open conns, got %d", stats.OpenConns)
	}
	if stats.Evictions < 1 {
		t.Errorf("expected at least 1 eviction, got %d", stats.Evictions)
	}
}

func TestReloadDiff(t *testing.T) {
	// WHAT: Reload detects fingerprint changes and closes stale connections.
	// WHY: Hot-reload must rebuild connections on config change.
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "dossier-diff", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	db1, err := pool.Resolve(context.Background(), "dossier-diff")
	if err != nil {
		t.Fatal(err)
	}

	// Change the strategy in the catalog.
	now := time.Now().UnixMilli()
	_, err = catalogDB.Exec(
		`UPDATE shards SET strategy = 'readonly', updated_at = ? WHERE id = 'dossier-diff'`, now)
	if err != nil {
		t.Fatal(err)
	}

	err = pool.Reload(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	db2, err := pool.Resolve(context.Background(), "dossier-diff")
	if err != nil {
		t.Fatal(err)
	}
	if db1 == db2 {
		t.Error("expected different *sql.DB after strategy change")
	}
}

func TestReload_PicksUpNewShards(t *testing.T) {
	// WHAT: Reload detects shards added after pool creation.
	// WHY: Hot-reload must discover new shards.
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Initially no shards.
	_, err = pool.Resolve(context.Background(), "new-shard")
	if !errors.Is(err, ErrShardNotFound) {
		t.Fatalf("expected ErrShardNotFound, got %v", err)
	}

	// Insert directly into catalog.
	insertShard(t, catalogDB, "new-shard", "local")

	err = pool.Reload(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Now it should resolve.
	db, err := pool.Resolve(context.Background(), "new-shard")
	if err != nil {
		t.Fatalf("resolve after reload: %v", err)
	}
	if db == nil {
		t.Fatal("expected non-nil db")
	}
}

func TestReloadRemovesShard(t *testing.T) {
	// WHAT: Removing a shard from catalog closes its connection.
	// WHY: Catalog is authoritative — removed shards must be inaccessible.
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "removable", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = pool.Resolve(context.Background(), "removable")
	if err != nil {
		t.Fatal(err)
	}

	_, err = catalogDB.Exec("DELETE FROM shards WHERE id = 'removable'")
	if err != nil {
		t.Fatal(err)
	}

	err = pool.Reload(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	stats := pool.Stats()
	if stats.OpenConns != 0 {
		t.Errorf("expected 0 open conns after removal, got %d", stats.OpenConns)
	}

	_, err = pool.Resolve(context.Background(), "removable")
	if !errors.Is(err, ErrShardNotFound) {
		t.Errorf("expected ErrShardNotFound, got %v", err)
	}
}

func TestLocalFactory_FlatPath(t *testing.T) {
	// WHAT: localFactory creates files at {dataDir}/{dossierID}.db (flat).
	// WHY: No more user subdirectories — flat layout is the new convention.
	dataDir := t.TempDir()

	db, closeFn, err := localFactory(dataDir, "my-dossier-id", "", nil)
	if err != nil {
		t.Fatalf("localFactory: %v", err)
	}
	defer closeFn()

	if db == nil {
		t.Fatal("expected non-nil db")
	}

	path := filepath.Join(dataDir, "my-dossier-id.db")
	_, err = os.Stat(path)
	if err != nil {
		t.Fatalf("expected file at %s: %v", path, err)
	}
}

func TestRegisterFactory(t *testing.T) {
	// WHAT: Custom factory is called on resolve.
	// WHY: Extensibility via factory registration.
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "custom-001", "custom")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	called := false
	pool.RegisterFactory("custom", func(dataDir, dossierID, endpoint string, config json.RawMessage) (*sql.DB, func(), error) {
		called = true
		return localFactory(dataDir, dossierID, endpoint, config)
	})

	err = pool.Reload(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	db, err := pool.Resolve(context.Background(), "custom-001")
	if err != nil {
		t.Fatal(err)
	}
	if db == nil {
		t.Fatal("expected non-nil db")
	}
	if !called {
		t.Error("custom factory was not called")
	}
}

func TestReaper(t *testing.T) {
	// WHAT: Idle connections are reaped after timeout.
	// WHY: Prevents resource leaks from abandoned shards.
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "reap-001", "local")

	pool, err := New(dataDir, catalogDB, WithIdleTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if _, err := pool.Resolve(context.Background(), "reap-001"); err != nil {
		t.Fatal(err)
	}

	if stats := pool.Stats(); stats.OpenConns != 1 {
		t.Fatalf("expected 1 open conn, got %d", stats.OpenConns)
	}

	// Wait for reaper.
	time.Sleep(200 * time.Millisecond)

	if stats := pool.Stats(); stats.OpenConns != 0 {
		t.Errorf("expected 0 open conns after reaper, got %d", stats.OpenConns)
	}
}

func TestMultipleShards(t *testing.T) {
	// WHAT: Multiple shards resolve independently.
	// WHY: Multi-tenant isolation.
	dataDir, catalogDB := testCatalog(t)

	for i := 0; i < 5; i++ {
		insertShard(t, catalogDB, "dossier-"+string(rune('a'+i)), "local")
	}

	pool, err := New(dataDir, catalogDB, WithMaxOpen(10))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	for i := 0; i < 5; i++ {
		id := "dossier-" + string(rune('a'+i))
		db, err := pool.Resolve(context.Background(), id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		if db == nil {
			t.Fatalf("nil db for %s", id)
		}
	}

	stats := pool.Stats()
	if stats.OpenConns != 5 {
		t.Errorf("expected 5 open conns, got %d", stats.OpenConns)
	}
}

// --- Admin handler tests ---

func TestAdminStats(t *testing.T) {
	// WHAT: GET /pool/stats returns JSON stats.
	// WHY: Observability endpoint.
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	handler := AdminHandler(pool)
	req := httptest.NewRequest("GET", "/pool/stats", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var stats PoolStats
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal stats: %v", err)
	}
}

func TestAdminListShards(t *testing.T) {
	// WHAT: GET /shards returns all shards.
	// WHY: Admin must see all shards.
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "d1", "local")
	insertShard(t, catalogDB, "d2", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	handler := AdminHandler(pool)
	req := httptest.NewRequest("GET", "/shards", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var shards []shard
	if err := json.Unmarshal(w.Body.Bytes(), &shards); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(shards) != 2 {
		t.Errorf("expected 2 shards, got %d", len(shards))
	}
}

func TestAdminGetShard(t *testing.T) {
	// WHAT: GET /shards/{dossierID} returns a single shard.
	// WHY: Admin must be able to inspect one shard.
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "inspect-me", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	handler := AdminHandler(pool)
	req := httptest.NewRequest("GET", "/shards/inspect-me", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var s shard
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.ID != "inspect-me" {
		t.Errorf("expected ID 'inspect-me', got %q", s.ID)
	}
}

func TestAdminGetShard_NotFound(t *testing.T) {
	// WHAT: GET /shards/{dossierID} returns 404 for unknown shard.
	// WHY: Admin must get a clear signal for missing shards.
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	handler := AdminHandler(pool)
	req := httptest.NewRequest("GET", "/shards/ghost", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestAdminSetStrategy(t *testing.T) {
	// WHAT: POST /shards/{dossierID}/strategy updates strategy.
	// WHY: Admin must be able to change routing strategy.
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "strategy-admin", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	handler := AdminHandler(pool)

	body := `{"strategy": "readonly", "endpoint": ""}`
	req := httptest.NewRequest("POST", "/shards/strategy-admin/strategy", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var strategy string
	err = catalogDB.QueryRow("SELECT strategy FROM shards WHERE id = 'strategy-admin'").Scan(&strategy)
	if err != nil {
		t.Fatal(err)
	}
	if strategy != "readonly" {
		t.Errorf("expected 'readonly', got %q", strategy)
	}
}

func TestLifecycleFullCycle(t *testing.T) {
	// WHAT: Full cycle: create → resolve → use → set strategy → delete.
	// WHY: End-to-end lifecycle validation.
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB, WithIdleTimeout(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx := context.Background()

	// 1. Create shard.
	err = pool.CreateShard(ctx, "dossier-full", "owner-1", "Workspace")
	if err != nil {
		t.Fatal(err)
	}
	err = pool.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Resolve and use.
	db, err := pool.Resolve(ctx, "dossier-full")
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec("CREATE TABLE docs (id TEXT PRIMARY KEY, content TEXT)")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, err = db.Exec("INSERT INTO docs (id, content) VALUES ('d1', 'hello')")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// 3. Set strategy to readonly.
	err = pool.SetStrategy(ctx, "dossier-full", "readonly", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}

	db2, err := pool.Resolve(ctx, "dossier-full")
	if err != nil {
		t.Fatal(err)
	}

	// Read should work.
	var content string
	err = db2.QueryRow("SELECT content FROM docs WHERE id = 'd1'").Scan(&content)
	if err != nil {
		t.Fatalf("read from readonly: %v", err)
	}
	if content != "hello" {
		t.Errorf("expected 'hello', got %q", content)
	}

	// Write should fail on readonly.
	_, err = db2.Exec("INSERT INTO docs (id, content) VALUES ('d2', 'world')")
	if err == nil {
		t.Error("expected error writing to readonly database")
	}

	// 4. Delete.
	err = pool.DeleteShard(ctx, "dossier-full")
	if err != nil {
		t.Fatal(err)
	}
	err = pool.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, err = pool.Resolve(ctx, "dossier-full")
	if !errors.Is(err, ErrShardDeleted) {
		t.Errorf("expected ErrShardDeleted, got %v", err)
	}
}

// --- EnsureShard tests ---

func TestEnsureShard_Idempotent(t *testing.T) {
	// WHAT: EnsureShard creates the shard on first call, no-ops on second.
	// WHY: Services that don't know if the catalog entry exists (e.g. siftrag) must not error on duplicates.
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx := context.Background()

	// First call: creates the shard.
	err = pool.EnsureShard(ctx, "dossier-ensure", "owner-1", "My Dossier")
	if err != nil {
		t.Fatalf("first EnsureShard: %v", err)
	}

	// Second call: same PK, should not error.
	err = pool.EnsureShard(ctx, "dossier-ensure", "owner-1", "My Dossier")
	if err != nil {
		t.Fatalf("second EnsureShard: %v", err)
	}

	// Verify only one row exists.
	var count int
	err = catalogDB.QueryRow("SELECT COUNT(*) FROM shards WHERE id = 'dossier-ensure'").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 shard row, got %d", count)
	}

	// Verify Resolve works after EnsureShard.
	db, err := pool.Resolve(ctx, "dossier-ensure")
	if err != nil {
		t.Fatalf("resolve after EnsureShard: %v", err)
	}
	if db == nil {
		t.Fatal("expected non-nil db")
	}
}

func TestEnsureShard_DoesNotOverwrite(t *testing.T) {
	// WHAT: EnsureShard on existing shard does not overwrite name/owner.
	// WHY: INSERT OR IGNORE must not modify existing rows.
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Create with original values.
	err = pool.CreateShard(ctx, "dossier-nooverwrite", "owner-original", "Original Name")
	if err != nil {
		t.Fatal(err)
	}

	// EnsureShard with different values.
	err = pool.EnsureShard(ctx, "dossier-nooverwrite", "owner-new", "New Name")
	if err != nil {
		t.Fatal(err)
	}

	// Verify original values are preserved.
	var ownerID, name string
	err = catalogDB.QueryRow("SELECT owner_id, name FROM shards WHERE id = 'dossier-nooverwrite'").Scan(&ownerID, &name)
	if err != nil {
		t.Fatal(err)
	}
	if ownerID != "owner-original" {
		t.Errorf("expected owner 'owner-original', got %q", ownerID)
	}
	if name != "Original Name" {
		t.Errorf("expected name 'Original Name', got %q", name)
	}
}

// --- Legacy alias tests ---

func TestLegacyAliases(t *testing.T) {
	// WHAT: Old error names still work as aliases.
	// WHY: Backward compatibility during migration.
	if ErrSpaceNotFound != ErrShardNotFound {
		t.Error("ErrSpaceNotFound should alias ErrShardNotFound")
	}
	if ErrSpaceDeleted != ErrShardDeleted {
		t.Error("ErrSpaceDeleted should alias ErrShardDeleted")
	}
	if ErrSpaceArchived != ErrShardArchived {
		t.Error("ErrSpaceArchived should alias ErrShardArchived")
	}
	if ErrSpaceUnavailable != ErrShardUnavailable {
		t.Error("ErrSpaceUnavailable should alias ErrShardUnavailable")
	}
}
