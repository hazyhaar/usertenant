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

// testCatalog creates a temporary directory and an in-memory catalog database
// initialized with the schema. Returns (dataDir, catalogDB, cleanup).
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

func insertShard(t *testing.T, db *sql.DB, userID, spaceID, strategy string) {
	t.Helper()
	now := time.Now().UnixMilli()
	_, err := db.Exec(
		`INSERT INTO shards (user_id, space_id, name, strategy, endpoint, config, status, size_bytes, created_at, updated_at)
		 VALUES (?, ?, '', ?, '', '{}', 'active', 0, ?, ?)`,
		userID, spaceID, strategy, now, now)
	if err != nil {
		t.Fatalf("insert shard: %v", err)
	}
}

func TestInitCatalog(t *testing.T) {
	dataDir := t.TempDir()
	db, err := dbopen.Open(filepath.Join(dataDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := InitCatalog(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	// Verify tables exist.
	var name string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='shards'").Scan(&name)
	if err != nil {
		t.Fatalf("shards table not found: %v", err)
	}
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='users'").Scan(&name)
	if err != nil {
		t.Fatalf("users table not found: %v", err)
	}

	// Idempotent: calling again should not error.
	if err := InitCatalog(context.Background(), db); err != nil {
		t.Fatalf("second init should be idempotent: %v", err)
	}
}

func TestPoolNewAndClose(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB, WithIdleTimeout(time.Minute), WithMaxOpen(10))
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}

	stats := pool.Stats()
	if stats.OpenConns != 0 {
		t.Errorf("expected 0 open conns, got %d", stats.OpenConns)
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("close pool: %v", err)
	}

	// Operations after close should fail.
	_, err = pool.Resolve(context.Background(), "user1", "space1")
	if !errors.Is(err, ErrPoolClosed) {
		t.Errorf("expected ErrPoolClosed, got %v", err)
	}
}

func TestResolveLocal(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "user1", "space1", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Reload to pick up the inserted shard.
	if err := pool.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	db, err := pool.Resolve(context.Background(), "user1", "space1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if db == nil {
		t.Fatal("expected non-nil db")
	}

	// Verify the .db file was created.
	path := filepath.Join(dataDir, "user1", "space1.db")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected .db file at %s: %v", path, err)
	}

	// Second resolve should hit cache.
	db2, err := pool.Resolve(context.Background(), "user1", "space1")
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

func TestResolveNotFound(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = pool.Resolve(context.Background(), "nonexistent", "space")
	if !errors.Is(err, ErrSpaceNotFound) {
		t.Errorf("expected ErrSpaceNotFound, got %v", err)
	}
}

func TestResolveNoop(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "user1", "space1", "noop")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if err := pool.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, err = pool.Resolve(context.Background(), "user1", "space1")
	if !errors.Is(err, ErrFactoryFailed) {
		t.Errorf("expected ErrFactoryFailed wrapping ErrSpaceUnavailable, got %v", err)
	}
}

func TestResolveUnknownStrategy(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "user1", "space1", "quantum")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if err := pool.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, err = pool.Resolve(context.Background(), "user1", "space1")
	if !errors.Is(err, ErrFactoryNotFound) {
		t.Errorf("expected ErrFactoryNotFound, got %v", err)
	}
}

func TestMaxOpenEviction(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)

	// Insert 3 shards, pool max is 2.
	insertShard(t, catalogDB, "user1", "s1", "local")
	insertShard(t, catalogDB, "user1", "s2", "local")
	insertShard(t, catalogDB, "user1", "s3", "local")

	pool, err := New(dataDir, catalogDB, WithMaxOpen(2), WithIdleTimeout(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if err := pool.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Open first two.
	if _, err := pool.Resolve(context.Background(), "user1", "s1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond) // Ensure different lastUsed times.
	if _, err := pool.Resolve(context.Background(), "user1", "s2"); err != nil {
		t.Fatal(err)
	}

	// Third should evict the oldest (s1).
	time.Sleep(time.Millisecond)
	if _, err := pool.Resolve(context.Background(), "user1", "s3"); err != nil {
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
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "user1", "space1", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Resolve to open a connection.
	db1, err := pool.Resolve(context.Background(), "user1", "space1")
	if err != nil {
		t.Fatal(err)
	}

	// Change the strategy in the catalog.
	now := time.Now().UnixMilli()
	_, err = catalogDB.Exec(
		`UPDATE shards SET strategy = 'readonly', updated_at = ? WHERE user_id = 'user1' AND space_id = 'space1'`,
		now)
	if err != nil {
		t.Fatal(err)
	}

	// Reload should detect the change and close the old connection.
	if err := pool.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Resolve again should return a different connection.
	db2, err := pool.Resolve(context.Background(), "user1", "space1")
	if err != nil {
		t.Fatal(err)
	}
	if db1 == db2 {
		t.Error("expected different *sql.DB after strategy change")
	}
}

func TestReloadRemovesShard(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "user1", "space1", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Open connection.
	if _, err := pool.Resolve(context.Background(), "user1", "space1"); err != nil {
		t.Fatal(err)
	}

	// Remove the shard from catalog.
	if _, err := catalogDB.Exec("DELETE FROM shards WHERE user_id = 'user1' AND space_id = 'space1'"); err != nil {
		t.Fatal(err)
	}

	// Reload should close the connection.
	if err := pool.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	stats := pool.Stats()
	if stats.OpenConns != 0 {
		t.Errorf("expected 0 open conns after removal, got %d", stats.OpenConns)
	}

	// Resolve should now fail.
	_, err = pool.Resolve(context.Background(), "user1", "space1")
	if !errors.Is(err, ErrSpaceNotFound) {
		t.Errorf("expected ErrSpaceNotFound, got %v", err)
	}
}

func TestCreateAndDeleteSpace(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Create.
	if err := pool.CreateSpace(ctx, "user1", "space1", "My Space"); err != nil {
		t.Fatal(err)
	}

	// Reload to pick up the change.
	if err := pool.Reload(ctx); err != nil {
		t.Fatal(err)
	}

	// Resolve should work.
	db, err := pool.Resolve(ctx, "user1", "space1")
	if err != nil {
		t.Fatalf("resolve after create: %v", err)
	}
	if db == nil {
		t.Fatal("expected non-nil db")
	}

	// Delete.
	if err := pool.DeleteSpace(ctx, "user1", "space1"); err != nil {
		t.Fatal(err)
	}

	// Reload to pick up deletion.
	if err := pool.Reload(ctx); err != nil {
		t.Fatal(err)
	}

	// Resolve should fail with deleted status.
	_, err = pool.Resolve(ctx, "user1", "space1")
	if !errors.Is(err, ErrSpaceDeleted) {
		t.Errorf("expected ErrSpaceDeleted, got %v", err)
	}

	// .db file should be removed.
	path := filepath.Join(dataDir, "user1", "space1.db")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected .db file to be removed, stat: %v", err)
	}
}

func TestSetStrategy(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Create a space.
	if err := pool.CreateSpace(ctx, "user1", "space1", "test"); err != nil {
		t.Fatal(err)
	}
	if err := pool.Reload(ctx); err != nil {
		t.Fatal(err)
	}

	// Change strategy.
	if err := pool.SetStrategy(ctx, "user1", "space1", "readonly", "", nil); err != nil {
		t.Fatal(err)
	}

	// Verify in DB.
	var strategy string
	err = catalogDB.QueryRow("SELECT strategy FROM shards WHERE user_id = 'user1' AND space_id = 'space1'").Scan(&strategy)
	if err != nil {
		t.Fatal(err)
	}
	if strategy != "readonly" {
		t.Errorf("expected strategy 'readonly', got %q", strategy)
	}

	// SetStrategy on nonexistent shard should fail.
	err = pool.SetStrategy(ctx, "ghost", "space", "local", "", nil)
	if !errors.Is(err, ErrSpaceNotFound) {
		t.Errorf("expected ErrSpaceNotFound, got %v", err)
	}
}

func TestCreateUser(t *testing.T) {
	_, catalogDB := testCatalog(t)

	pool, err := New(t.TempDir(), catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if err := pool.CreateUser(context.Background(), "u1", "Alice"); err != nil {
		t.Fatal(err)
	}

	var name string
	err = catalogDB.QueryRow("SELECT name FROM users WHERE id = 'u1'").Scan(&name)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Alice" {
		t.Errorf("expected name 'Alice', got %q", name)
	}
}

func TestRegisterFactory(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "user1", "space1", "custom")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	called := false
	pool.RegisterFactory("custom", func(dataDir, userID, spaceID, endpoint string, config json.RawMessage) (*sql.DB, func(), error) {
		called = true
		// Use local factory under the hood for testing.
		return localFactory(dataDir, userID, spaceID, endpoint, config)
	})

	if err := pool.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	db, err := pool.Resolve(context.Background(), "user1", "space1")
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
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "user1", "space1", "local")

	pool, err := New(dataDir, catalogDB, WithIdleTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if err := pool.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Resolve to open a connection.
	if _, err := pool.Resolve(context.Background(), "user1", "space1"); err != nil {
		t.Fatal(err)
	}

	if stats := pool.Stats(); stats.OpenConns != 1 {
		t.Fatalf("expected 1 open conn, got %d", stats.OpenConns)
	}

	// Wait for reaper to run (idleTimeout + reaper interval).
	time.Sleep(200 * time.Millisecond)

	if stats := pool.Stats(); stats.OpenConns != 0 {
		t.Errorf("expected 0 open conns after reaper, got %d", stats.OpenConns)
	}
}

func TestDeleteSpaceNotFound(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	err = pool.DeleteSpace(context.Background(), "ghost", "space")
	if !errors.Is(err, ErrSpaceNotFound) {
		t.Errorf("expected ErrSpaceNotFound, got %v", err)
	}
}

func TestResolveDeletedShard(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)

	now := time.Now().UnixMilli()
	_, err := catalogDB.Exec(
		`INSERT INTO shards (user_id, space_id, name, strategy, endpoint, config, status, size_bytes, created_at, updated_at)
		 VALUES ('u1', 's1', '', 'local', '', '{}', 'deleted', 0, ?, ?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = pool.Resolve(context.Background(), "u1", "s1")
	if !errors.Is(err, ErrSpaceDeleted) {
		t.Errorf("expected ErrSpaceDeleted, got %v", err)
	}
}

func TestMultipleShards(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)

	for i := 0; i < 5; i++ {
		insertShard(t, catalogDB, "user1", "space"+string(rune('a'+i)), "local")
	}

	pool, err := New(dataDir, catalogDB, WithMaxOpen(10))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if err := pool.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		spaceID := "space" + string(rune('a'+i))
		db, err := pool.Resolve(context.Background(), "user1", spaceID)
		if err != nil {
			t.Fatalf("resolve %s: %v", spaceID, err)
		}
		if db == nil {
			t.Fatalf("nil db for %s", spaceID)
		}
	}

	stats := pool.Stats()
	if stats.OpenConns != 5 {
		t.Errorf("expected 5 open conns, got %d", stats.OpenConns)
	}
	if stats.CacheMisses != 5 {
		t.Errorf("expected 5 misses, got %d", stats.CacheMisses)
	}
}

// Admin handler tests

func TestAdminStats(t *testing.T) {
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
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "user1", "space1", "local")
	insertShard(t, catalogDB, "user2", "space2", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	handler := AdminHandler(pool)

	// List all.
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

func TestAdminListShardsByUser(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "user1", "space1", "local")
	insertShard(t, catalogDB, "user1", "space2", "local")
	insertShard(t, catalogDB, "user2", "space3", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	handler := AdminHandler(pool)

	req := httptest.NewRequest("GET", "/shards/user1", nil)
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
		t.Errorf("expected 2 shards for user1, got %d", len(shards))
	}
}

func TestAdminSetStrategy(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)
	insertShard(t, catalogDB, "user1", "space1", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	handler := AdminHandler(pool)

	body := `{"strategy": "readonly", "endpoint": ""}`
	req := httptest.NewRequest("POST", "/shards/user1/space1/strategy", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify strategy changed.
	var strategy string
	err = catalogDB.QueryRow("SELECT strategy FROM shards WHERE user_id = 'user1' AND space_id = 'space1'").Scan(&strategy)
	if err != nil {
		t.Fatal(err)
	}
	if strategy != "readonly" {
		t.Errorf("expected 'readonly', got %q", strategy)
	}
}

func TestLifecycleFullCycle(t *testing.T) {
	dataDir, catalogDB := testCatalog(t)

	pool, err := New(dataDir, catalogDB, WithIdleTimeout(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx := context.Background()

	// 1. Create user.
	if err := pool.CreateUser(ctx, "user1", "Test User"); err != nil {
		t.Fatal(err)
	}

	// 2. Create space.
	if err := pool.CreateSpace(ctx, "user1", "space1", "Workspace"); err != nil {
		t.Fatal(err)
	}
	if err := pool.Reload(ctx); err != nil {
		t.Fatal(err)
	}

	// 3. Resolve and use.
	db, err := pool.Resolve(ctx, "user1", "space1")
	if err != nil {
		t.Fatal(err)
	}

	// Create a table in the space to verify it's usable.
	_, err = db.Exec("CREATE TABLE docs (id TEXT PRIMARY KEY, content TEXT)")
	if err != nil {
		t.Fatalf("create table in space: %v", err)
	}
	_, err = db.Exec("INSERT INTO docs (id, content) VALUES ('d1', 'hello')")
	if err != nil {
		t.Fatalf("insert into space: %v", err)
	}

	// 4. Set strategy to readonly.
	if err := pool.SetStrategy(ctx, "user1", "space1", "readonly", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := pool.Reload(ctx); err != nil {
		t.Fatal(err)
	}

	// Resolve again — should get a readonly connection.
	db2, err := pool.Resolve(ctx, "user1", "space1")
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

	// 5. Delete.
	if err := pool.DeleteSpace(ctx, "user1", "space1"); err != nil {
		t.Fatal(err)
	}
	if err := pool.Reload(ctx); err != nil {
		t.Fatal(err)
	}

	// Should be deleted.
	_, err = pool.Resolve(ctx, "user1", "space1")
	if !errors.Is(err, ErrSpaceDeleted) {
		t.Errorf("expected ErrSpaceDeleted, got %v", err)
	}
}
