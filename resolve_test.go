package tenant

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func insertShardWithOwner(t *testing.T, catalogDB *sql.DB, dossierID, ownerID, strategy string) {
	t.Helper()
	now := time.Now().UnixMilli()
	_, err := catalogDB.Exec(
		`INSERT INTO shards (id, owner_id, name, strategy, endpoint, config, status, size_bytes, created_at, updated_at)
		 VALUES (?, ?, '', ?, '', '{}', 'active', 0, ?, ?)`,
		dossierID, ownerID, strategy, now, now)
	if err != nil {
		t.Fatalf("insert shard with owner: %v", err)
	}
}

func TestResolveWithOwner_OK(t *testing.T) {
	// WHAT: ResolveWithOwner succeeds when ownerID matches the catalog snapshot.
	// WHY: Ownership verification on the fast path prevents cross-tenant writes.
	dataDir, catalogDB := testCatalog(t)
	insertShardWithOwner(t, catalogDB, "dossier-own-1", "owner-a", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	db, err := pool.ResolveWithOwner(context.Background(), "dossier-own-1", "owner-a")
	if err != nil {
		t.Fatalf("ResolveWithOwner: %v", err)
	}
	if db == nil {
		t.Fatal("expected non-nil db")
	}
}

func TestResolveWithOwner_Mismatch(t *testing.T) {
	// WHAT: ResolveWithOwner returns ErrOwnershipMismatch for wrong ownerID.
	// WHY: Prevents HORAG/chrc from writing to a shard they don't own.
	dataDir, catalogDB := testCatalog(t)
	insertShardWithOwner(t, catalogDB, "dossier-own-2", "owner-a", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = pool.ResolveWithOwner(context.Background(), "dossier-own-2", "owner-b")
	if !errors.Is(err, ErrOwnershipMismatch) {
		t.Errorf("expected ErrOwnershipMismatch, got %v", err)
	}
}

func TestResolveWithOwner_CachePath(t *testing.T) {
	// WHAT: Ownership check runs even on cache hit (second resolve with different owner).
	// WHY: Cache must not bypass the ownership gate.
	dataDir, catalogDB := testCatalog(t)
	insertShardWithOwner(t, catalogDB, "dossier-own-3", "owner-a", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// First call: populate cache.
	_, err = pool.ResolveWithOwner(context.Background(), "dossier-own-3", "owner-a")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// Second call: wrong owner, should still fail.
	_, err = pool.ResolveWithOwner(context.Background(), "dossier-own-3", "owner-b")
	if !errors.Is(err, ErrOwnershipMismatch) {
		t.Errorf("expected ErrOwnershipMismatch on cache hit, got %v", err)
	}
}

func TestResolveWithOwner_EmptyOwnerID(t *testing.T) {
	// WHAT: ResolveWithOwner rejects empty ownerID.
	// WHY: Empty ownerID would bypass the check — must be explicit.
	dataDir, catalogDB := testCatalog(t)
	insertShardWithOwner(t, catalogDB, "dossier-own-4", "owner-a", "local")

	pool, err := New(dataDir, catalogDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = pool.ResolveWithOwner(context.Background(), "dossier-own-4", "")
	if err == nil {
		t.Fatal("expected error for empty ownerID")
	}
}
