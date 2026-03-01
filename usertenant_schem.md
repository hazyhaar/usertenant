# usertenant -- Technical Schema

**Purpose:** Go library for tenant routing with a pool of sharded SQLite connections -- one .db file per dossier (dossierID = universal routing key).

**Module:** `github.com/hazyhaar/usertenant`
**Package:** `tenant`
**No binaries** -- pure library, no `cmd/` directory.

---

## 1. Architecture Overview

```
╔══════════════════════════════════════════════════════════════════════════════════════╗
║                          usertenant LIBRARY ARCHITECTURE                            ║
╠══════════════════════════════════════════════════════════════════════════════════════╣
║                                                                                      ║
║  CONSUMER SERVICE (e.g. chrc, siftrag)                                               ║
║  ┌─────────────────────────────────────────────────────────────────────────────────┐  ║
║  │  ctx + dossierID ──▶ pool.Resolve(ctx, dossierID) ──▶ *sql.DB                  │  ║
║  │                      pool.ResolveWithWatch(ctx, id, interval, fn) ──▶ *sql.DB   │  ║
║  │                      pool.CreateShard(ctx, id, ownerID, name)                   │  ║
║  │                      pool.EnsureShard(ctx, id, ownerID, name)                   │  ║
║  │                      pool.DeleteShard(ctx, id)                                  │  ║
║  │                      pool.SetStrategy(ctx, id, strategy, endpoint, config)      │  ║
║  │                      pool.Stats() ──▶ PoolStats                                 │  ║
║  │                      pool.Close()                                               │  ║
║  │                      pool.RegisterFactory(strategy, factory)                    │  ║
║  │                      pool.Reload(ctx)                                           │  ║
║  │                      pool.Watch(ctx, interval)                                  │  ║
║  │                      AdminHandler(pool) ──▶ http.Handler                        │  ║
║  └─────────────────────────────────────────────────────────────────────────────────┘  ║
║                              │                                                        ║
║                              ▼                                                        ║
║  ╔═════════════════════════════════════════════════════════════════════════════════╗   ║
║  ║                              Pool                                              ║   ║
║  ║  ┌──────────────────────────────────────────────────────────────────────────┐  ║   ║
║  ║  │ dataDir      string              -- base directory for shard .db files   │  ║   ║
║  ║  │ catalogDB    *sql.DB             -- catalog database (shards table)      │  ║   ║
║  ║  │ factories    map[string]ShardFactory  -- strategy -> factory function    │  ║   ║
║  ║  │ conns        map[string]*entry   -- dossierID -> live connection cache   │  ║   ║
║  ║  │ shardSnap    map[string]shard    -- dossierID -> metadata snapshot       │  ║   ║
║  ║  │ idleTimeout  time.Duration       -- default: 5m                          │  ║   ║
║  ║  │ maxOpen      int                 -- default: 256                         │  ║   ║
║  ║  │ logger       *slog.Logger        -- structured logging                   │  ║   ║
║  ║  │ closeCh      chan struct{}        -- shutdown signal                      │  ║   ║
║  ║  │ closed       atomic.Bool          -- prevents use-after-close            │  ║   ║
║  ║  │                                                                          │  ║   ║
║  ║  │ ATOMIC COUNTERS:                                                         │  ║   ║
║  ║  │   totalResolves, cacheHits, cacheMisses,                                 │  ║   ║
║  ║  │   evictions, factoryErrors, reloads                                      │  ║   ║
║  ║  └──────────────────────────────────────────────────────────────────────────┘  ║   ║
║  ║                                                                                ║   ║
║  ║  BACKGROUND GOROUTINES (started in New()):                                     ║   ║
║  ║  ┌────────────────────────────────────────┐                                    ║   ║
║  ║  │ reapLoop()                             │                                    ║   ║
║  ║  │   interval = idleTimeout / 2           │                                    ║   ║
║  ║  │   (min 50ms)                           │                                    ║   ║
║  ║  │   - scans conns map                    │                                    ║   ║
║  ║  │   - skips entries with active watchers │                                    ║   ║
║  ║  │   - evicts if idle > idleTimeout       │                                    ║   ║
║  ║  │   - stops on closeCh                   │                                    ║   ║
║  ║  └────────────────────────────────────────┘                                    ║   ║
║  ╚═════════════════════════════════════════════════════════════════════════════════╝   ║
║                              │                                                        ║
║              ┌───────────────┼───────────────────────┐                                ║
║              │               │                       │                                ║
║              ▼               ▼                       ▼                                ║
║  ┌──────────────────┐  ┌──────────────┐  ┌──────────────────────────┐                 ║
║  │  _catalog.db     │  │ {id}.db      │  │ {id}.db                  │                 ║
║  │  (shards table)  │  │ (shard data) │  │ (shard data)             │                 ║
║  │  managed by      │  │ flat layout  │  │ opened by ShardFactory   │                 ║
║  │  consumer        │  │ in dataDir   │  │ via dbopen.Open()        │                 ║
║  └──────────────────┘  └──────────────┘  └──────────────────────────┘                 ║
║                                                                                      ║
╚══════════════════════════════════════════════════════════════════════════════════════╝
```

---

## 2. Database Schema -- Catalog (_catalog.db)

```
╔══════════════════════════════════════════════════════════════════════════╗
║  TABLE: shards                                                          ║
╠══════════════╦═══════════╦═══════════════════════════════════════════════╣
║ Column       ║ Type      ║ Constraints / Default                        ║
╠══════════════╬═══════════╬═══════════════════════════════════════════════╣
║ id           ║ TEXT      ║ PRIMARY KEY (dossierID, UUID v7)             ║
║ owner_id     ║ TEXT      ║ NOT NULL DEFAULT ''   -- audit only          ║
║ name         ║ TEXT      ║ NOT NULL DEFAULT ''                          ║
║ strategy     ║ TEXT      ║ NOT NULL DEFAULT 'local'                     ║
║ endpoint     ║ TEXT      ║ NOT NULL DEFAULT ''                          ║
║ config       ║ TEXT      ║ NOT NULL DEFAULT '{}'  -- JSON               ║
║ status       ║ TEXT      ║ NOT NULL DEFAULT 'active'                    ║
║ size_bytes   ║ INTEGER   ║ NOT NULL DEFAULT 0                           ║
║ created_at   ║ INTEGER   ║ NOT NULL  -- epoch milliseconds              ║
║ updated_at   ║ INTEGER   ║ NOT NULL  -- epoch milliseconds              ║
╠══════════════╩═══════════╩═══════════════════════════════════════════════╣
║  INDEXES:                                                                ║
║    idx_shards_owner   ON shards(owner_id)                                ║
║    idx_shards_status  ON shards(status)                                  ║
╠══════════════════════════════════════════════════════════════════════════╣
║  STATUS VALUES: 'active' | 'deleted' | 'archived'                       ║
║  STRATEGY VALUES: 'local' | 'readonly' | 'noop' | 'archived' | custom  ║
╚══════════════════════════════════════════════════════════════════════════╝
```

**Notes:**
- `config` is stored as TEXT but is always valid JSON (json.RawMessage in Go).
- `owner_id` is purely informational (audit). It is NOT used for routing.
- The catalog database is opened by the consumer service, NOT by usertenant.
  The consumer passes `*sql.DB` to `tenant.New()`.
- `InitCatalog(ctx, db)` must be called once to create the table (idempotent via IF NOT EXISTS).

---

## 3. Shard File Layout on Disk

```
{dataDir}/
├── _catalog.db          <-- Managed by consumer service (contains `shards` table)
├── _catalog.db-wal      <-- WAL file (SQLite)
├── _catalog.db-shm      <-- Shared memory (SQLite)
├── {dossierID-1}.db     <-- Shard: one file per dossier (flat layout)
├── {dossierID-1}.db-wal
├── {dossierID-1}.db-shm
├── {dossierID-2}.db     <-- Another shard
├── {dossierID-2}.db-wal
└── ...

INVARIANT: One .db file per dossier. No subdirectories. No user-level nesting.
           dossierID is the sole routing key (no composite userID+spaceID).
```

---

## 4. Resolve Flow (Core Data Path)

```
   Resolve(ctx, dossierID)
           │
           ▼
   ┌─── closed? ──── YES ──▶ return ErrPoolClosed
   │       NO
   │       ▼
   │  totalResolves++
   │       │
   │       ▼
   │  ┌─ RLock ─┐
   │  │ conns[dossierID] exists? ──── YES ──▶ update lastUsed ──▶ cacheHits++ ──▶ return db
   │  └─────────┘                      │
   │       NO                          │
   │       ▼                           │
   │  cacheMisses++                    │
   │       │                           │
   │       ▼                           │
   │  ┌─ RLock ─┐                     │
   │  │ shardSnap[dossierID] exists?   │
   │  │   NO ──▶ return ErrShardNotFound
   │  │   YES ──▶ shard metadata       │
   │  └─────────┘                      │
   │       │                           │
   │       ▼                           │
   │  status == "deleted"? ──▶ return ErrShardDeleted
   │  status == "archived" && strategy != "archived"? ──▶ return ErrShardArchived
   │       │
   │       ▼
   │  ┌─ Lock (write) ─┐
   │  │ Double-check: conns[dossierID] exists? ── YES ──▶ return db
   │  │       NO
   │  │       ▼
   │  │ len(conns) >= maxOpen?
   │  │   YES ──▶ evictOldestLocked()
   │  │            failed? ──▶ return ErrPoolExhausted
   │  │       │
   │  │       ▼
   │  │ factories[strategy] exists?
   │  │   NO ──▶ return ErrFactoryNotFound
   │  │   YES ──▶ factory(dataDir, dossierID, endpoint, config)
   │  │            error? ──▶ factoryErrors++ ──▶ return ErrFactoryFailed
   │  │       │
   │  │       ▼
   │  │ Store entry{db, closeFn, lastUsed=now, strategy}
   │  │ conns[dossierID] = entry
   │  └────────────────────┘
   │       │
   │       ▼
   │  return db, nil
   └───────────────────
```

---

## 5. ResolveWithWatch Flow

```
   ResolveWithWatch(ctx, dossierID, interval, onChange)
           │
           ▼
   Resolve(ctx, dossierID)  ──── error? ──▶ return nil, err
           │
           ▼
   ┌─ Lock ─┐
   │ entry.cancel != nil? (already watching)
   │   YES ──▶ return db, nil  (idempotent)
   │   NO
   │   ▼
   │ watchCtx, cancel = context.WithCancel(ctx)
   │ entry.cancel = cancel
   └─────────┘
           │
           ▼
   go watch.New(db, interval, onChange, logger).Run(watchCtx)
           │
           ▼
   return db, nil

   Watcher lifecycle:
   - Tied to entry: eviction/reload/pool.Close() calls cancel()
   - Entries with active watchers are NEVER reaped by the idle reaper
```

---

## 6. Reload / Watch (Catalog Hot-Reload)

```
   ┌───────────────────────────────────────────────────────────────────────┐
   │                      Reload(ctx)                                      │
   │                                                                       │
   │  1. SELECT * FROM shards  ──▶  build newSnap map[string]shard         │
   │                                                                       │
   │  2. Lock (write)                                                      │
   │     For each conns[key]:                                              │
   │       - key NOT in newSnap?  ──▶  closeEntryLocked(key)  [removed]    │
   │       - fingerprint changed? ──▶  closeEntryLocked(key)  [changed]    │
   │     Update shardSnap = newSnap                                        │
   │     reloads++                                                         │
   │                                                                       │
   │  fingerprint = strategy + "|" + endpoint + "|" + config + "|" + status│
   │                                                                       │
   │  NOTE: New/unchanged shards are NOT opened here.                      │
   │        Connections are created lazily on next Resolve().               │
   └───────────────────────────────────────────────────────────────────────┘

   ┌───────────────────────────────────────────────────────────────────────┐
   │                      Watch(ctx, interval)                             │
   │  Blocking loop:                                                       │
   │                                                                       │
   │  1. Read initial PRAGMA data_version from catalogDB                   │
   │  2. Every `interval`:                                                 │
   │       PRAGMA data_version ──▶ changed?                                │
   │         YES ──▶ Reload(ctx)                                           │
   │         NO  ──▶ continue                                              │
   │  3. Stops on ctx.Done() or closeCh                                    │
   │                                                                       │
   │  NOTE: PRAGMA data_version detects changes by OTHER connections.      │
   │        Same-connection writes (CreateShard, SetStrategy, DeleteShard)  │
   │        call Reload() explicitly because Watch won't detect them.      │
   └───────────────────────────────────────────────────────────────────────┘
```

---

## 7. Built-in Shard Factories

```
╔════════════════╦═════════════════════════════════════════════════════════════════╗
║ Strategy       ║ Behavior                                                       ║
╠════════════════╬═════════════════════════════════════════════════════════════════╣
║ "local"        ║ dbopen.Open("{dataDir}/{dossierID}.db")                        ║
║                ║ Read-write, WAL mode, busy_timeout=5000, foreign_keys=ON       ║
║                ║ Default strategy for new shards via CreateShard/EnsureShard     ║
╠════════════════╬═════════════════════════════════════════════════════════════════╣
║ "readonly"     ║ dbopen.Open("{dataDir}/{dossierID}.db", dbopen.WithReadOnly()) ║
║                ║ mode=ro, PRAGMA query_only(1) -- writes are rejected           ║
╠════════════════╬═════════════════════════════════════════════════════════════════╣
║ "noop"         ║ Returns nil *sql.DB + ErrShardUnavailable                      ║
║                ║ Used for disabled/suspended shards                              ║
╠════════════════╬═════════════════════════════════════════════════════════════════╣
║ (custom)       ║ Registered via pool.RegisterFactory(strategy, fn)              ║
║                ║ Signature: func(dataDir, dossierID, endpoint string,           ║
║                ║                  config json.RawMessage) (*sql.DB, func(), err)║
╚════════════════╩═════════════════════════════════════════════════════════════════╝
```

---

## 8. HTTP Admin Endpoints (AdminHandler)

```
╔═════════════════════════════════════════╦════════╦═══════════════════════════════════════╗
║ Route                                   ║ Method ║ Description                           ║
╠═════════════════════════════════════════╬════════╬═══════════════════════════════════════╣
║ /pool/stats                             ║ GET    ║ Returns PoolStats JSON:               ║
║                                         ║        ║   open_conns, total_resolves,          ║
║                                         ║        ║   cache_hits, cache_misses,            ║
║                                         ║        ║   evictions, factory_errors, reloads   ║
╠═════════════════════════════════════════╬════════╬═══════════════════════════════════════╣
║ /shards                                 ║ GET    ║ Returns []shard JSON (all shards       ║
║                                         ║        ║ from in-memory snapshot)                ║
╠═════════════════════════════════════════╬════════╬═══════════════════════════════════════╣
║ /shards/{dossierID}                     ║ GET    ║ Returns single shard JSON              ║
║                                         ║        ║ 404 if not found                       ║
╠═════════════════════════════════════════╬════════╬═══════════════════════════════════════╣
║ /shards/{dossierID}/strategy            ║ POST   ║ Body: {"strategy":"...","endpoint":    ║
║                                         ║        ║        "...","config":{}}               ║
║                                         ║        ║ Updates strategy, triggers Reload()     ║
║                                         ║        ║ 400 if strategy empty or bad JSON       ║
║                                         ║        ║ 500 on DB/reload error                  ║
╚═════════════════════════════════════════╩════════╩═══════════════════════════════════════╝

Uses Go 1.22+ net/http pattern routing ("GET /path", "POST /path").
Reads from in-memory snapshot (not catalog DB) for GET operations.
POST triggers SetStrategy + immediate Reload for consistency.
Body limit: 1 MiB (io.LimitReader).
```

---

## 9. All Types and Their Relationships

```
╔════════════════════════════════════════════════════════════════════════════════╗
║  TYPE MAP                                                                     ║
╠════════════════════════════════════════════════════════════════════════════════╣
║                                                                               ║
║  Pool (exported, core struct)                                                 ║
║    ├── dataDir: string                                                        ║
║    ├── catalogDB: *sql.DB  ◄── provided by consumer                           ║
║    ├── factories: map[string]ShardFactory                                     ║
║    │     ├── "local"    ──▶ localFactory                                      ║
║    │     ├── "readonly" ──▶ readonlyFactory                                   ║
║    │     ├── "noop"     ──▶ noopFactory                                       ║
║    │     └── (custom)   ──▶ registered via RegisterFactory()                  ║
║    ├── conns: map[string]*entry                                               ║
║    │     └── entry (unexported)                                               ║
║    │           ├── db: *sql.DB          -- the live shard connection           ║
║    │           ├── closeFn: func()      -- returned by factory                ║
║    │           ├── lastUsed: atomic.Int64  -- epoch ms, LRU tracking          ║
║    │           ├── strategy: string     -- for debug/logging                  ║
║    │           └── cancel: func()       -- nil unless watcher attached        ║
║    ├── shardSnap: map[string]shard                                            ║
║    │     └── shard (unexported)                                               ║
║    │           ├── ID: string           -- dossierID (PK)                     ║
║    │           ├── OwnerID: string      -- audit only                         ║
║    │           ├── Name: string                                               ║
║    │           ├── Strategy: string                                           ║
║    │           ├── Endpoint: string                                           ║
║    │           ├── Config: json.RawMessage                                    ║
║    │           ├── Status: string                                             ║
║    │           ├── SizeBytes: int64                                           ║
║    │           ├── CreatedAt: int64     -- epoch ms                           ║
║    │           ├── UpdatedAt: int64     -- epoch ms                           ║
║    │           └── fingerprint() string  -- diff key for reload               ║
║    ├── idleTimeout: time.Duration (default 5m)                                ║
║    ├── maxOpen: int (default 256)                                             ║
║    └── logger: *slog.Logger                                                   ║
║                                                                               ║
║  ShardFactory (exported, func type)                                           ║
║    func(dataDir, dossierID, endpoint string, config json.RawMessage)          ║
║        -> (*sql.DB, func(), error)                                            ║
║                                                                               ║
║  Option (exported, func type)                                                 ║
║    func(*Pool)                                                                ║
║    Implementations: WithIdleTimeout(d), WithMaxOpen(n), WithLogger(l)         ║
║                                                                               ║
║  PoolStats (exported, struct)                                                 ║
║    OpenConns, TotalResolves, CacheHits, CacheMisses,                          ║
║    Evictions, FactoryErrors, Reloads  (all JSON-tagged)                       ║
║                                                                               ║
╚════════════════════════════════════════════════════════════════════════════════╝
```

---

## 10. Sentinel Errors

```
╔═══════════════════════════╦═════════════════════════════════════════════════════╗
║ Error                     ║ When                                               ║
╠═══════════════════════════╬═════════════════════════════════════════════════════╣
║ ErrShardNotFound          ║ dossierID not in catalog snapshot                  ║
║ ErrShardArchived          ║ status="archived" and strategy != "archived"       ║
║ ErrShardDeleted           ║ status="deleted"                                   ║
║ ErrShardUnavailable       ║ Returned by noopFactory                            ║
║ ErrPoolExhausted          ║ maxOpen reached, no evictable entry                ║
║ ErrFactoryNotFound        ║ No factory registered for shard's strategy         ║
║ ErrFactoryFailed          ║ Factory returned an error during connection open   ║
║ ErrPoolClosed             ║ Operation attempted after pool.Close()             ║
╠═══════════════════════════╬═════════════════════════════════════════════════════╣
║ LEGACY ALIASES:           ║                                                    ║
║ ErrSpaceNotFound          ║ = ErrShardNotFound                                 ║
║ ErrSpaceArchived          ║ = ErrShardArchived                                 ║
║ ErrSpaceDeleted           ║ = ErrShardDeleted                                  ║
║ ErrSpaceUnavailable       ║ = ErrShardUnavailable                              ║
╚═══════════════════════════╩═════════════════════════════════════════════════════╝
```

---

## 11. Sub-package: dbopen

```
╔════════════════════════════════════════════════════════════════════════════════╗
║  Package: github.com/hazyhaar/usertenant/dbopen                               ║
║  File:    dbopen/dbopen.go                                                    ║
╠════════════════════════════════════════════════════════════════════════════════╣
║                                                                               ║
║  Open(path string, opts ...Option) (*sql.DB, error)                           ║
║                                                                               ║
║  Builds DSN with _pragma= for per-connection pragmas:                         ║
║    path?_txlock=immediate                                                     ║
║         &_pragma=busy_timeout(5000)         <-- configurable                  ║
║         &_pragma=journal_mode(WAL)          <-- configurable                  ║
║         &_pragma=foreign_keys(1)            <-- configurable                  ║
║    If ReadOnly:                                                               ║
║         &mode=ro&_pragma=query_only(1)                                        ║
║    If ExtraPragmas:                                                           ║
║         &_pragma={custom}                                                     ║
║                                                                               ║
║  After sql.Open(), immediately calls db.Ping() to force file creation         ║
║  and pragma application (sql.Open is lazy by default).                        ║
║                                                                               ║
║  Options:                                                                     ║
║    WithReadOnly()        -- mode=ro + query_only(1)                           ║
║    WithBusyTimeout(ms)   -- default 5000                                      ║
║    WithJournalMode(mode) -- default "WAL"                                     ║
║    WithForeignKeys(bool) -- default true                                      ║
║    WithPragma(string)    -- extra custom pragma                               ║
║                                                                               ║
║  Driver: modernc.org/sqlite (pure Go, CGO_ENABLED=0)                         ║
║  NEVER uses mattn/go-sqlite3                                                  ║
╚════════════════════════════════════════════════════════════════════════════════╝
```

---

## 12. Sub-package: watch

```
╔════════════════════════════════════════════════════════════════════════════════╗
║  Package: github.com/hazyhaar/usertenant/watch                                ║
║  File:    watch/watch.go                                                      ║
╠════════════════════════════════════════════════════════════════════════════════╣
║                                                                               ║
║  Watcher struct                                                               ║
║    db:       *sql.DB                                                          ║
║    interval: time.Duration                                                    ║
║    onChange: func() error                                                     ║
║    logger:   *slog.Logger                                                     ║
║                                                                               ║
║  New(db, interval, onChange, logger) *Watcher                                 ║
║  Run(ctx) error                                                               ║
║                                                                               ║
║  Mechanism:                                                                   ║
║    1. Read initial PRAGMA data_version                                        ║
║    2. Poll every `interval`                                                   ║
║    3. If data_version changed -> call onChange()                               ║
║    4. Block until ctx cancelled                                               ║
║                                                                               ║
║  Used by:                                                                     ║
║    - Pool.Watch() for catalog DB changes                                      ║
║    - Pool.ResolveWithWatch() for individual shard DB changes                  ║
║                                                                               ║
║  IMPORTANT: PRAGMA data_version only detects changes from OTHER               ║
║  connections. Same-connection writes are invisible to data_version.            ║
╚════════════════════════════════════════════════════════════════════════════════╝
```

---

## 13. Pool Lifecycle

```
   ┌──────────────────────────────────────────────────────────────────────────┐
   │  INITIALIZATION                                                          │
   │                                                                          │
   │  1. Consumer opens catalogDB via dbopen.Open(path)                       │
   │  2. InitCatalog(ctx, catalogDB) -- creates shards table                  │
   │  3. pool, err := tenant.New(dataDir, catalogDB, opts...)                 │
   │       ├── registers built-in factories (local, readonly, noop)           │
   │       ├── calls Reload(ctx) -- loads initial snapshot                    │
   │       └── starts reapLoop() goroutine                                    │
   │  4. (optional) pool.RegisterFactory("custom", fn)                        │
   │  5. (optional) go pool.Watch(ctx, 200*time.Millisecond)                  │
   └──────────────────────────────────────────────────────────────────────────┘
                               │
                               ▼
   ┌──────────────────────────────────────────────────────────────────────────┐
   │  RUNTIME                                                                 │
   │                                                                          │
   │  pool.Resolve(ctx, dossierID)       -- cache-first connection routing    │
   │  pool.ResolveWithWatch(ctx, ...)    -- resolve + attach data_version     │
   │  pool.CreateShard(ctx, ...)         -- INSERT + Reload                   │
   │  pool.EnsureShard(ctx, ...)         -- INSERT OR IGNORE + Reload         │
   │  pool.SetStrategy(ctx, ...)         -- UPDATE + Reload                   │
   │  pool.DeleteShard(ctx, ...)         -- close conn + UPDATE status        │
   │                                        + remove .db file + Reload        │
   │  pool.Stats()                       -- atomic counter snapshot           │
   │  AdminHandler(pool)                 -- mount HTTP admin endpoints        │
   └──────────────────────────────────────────────────────────────────────────┘
                               │
                               ▼
   ┌──────────────────────────────────────────────────────────────────────────┐
   │  SHUTDOWN                                                                │
   │                                                                          │
   │  pool.Close()                                                            │
   │    1. closed = true (atomic)                                             │
   │    2. close(closeCh) -- stops reapLoop + Watch                           │
   │    3. Lock                                                               │
   │    4. For each conn: cancel watcher, call closeFn, delete from map       │
   │    5. Idempotent via sync.Once                                           │
   └──────────────────────────────────────────────────────────────────────────┘
```

---

## 14. CRUD Operations Detail

```
╔══════════════════╦════════════════════════════════════════════════════════════════╗
║ Operation        ║ SQL + Side Effects                                            ║
╠══════════════════╬════════════════════════════════════════════════════════════════╣
║ CreateShard      ║ INSERT INTO shards VALUES(id, ownerID, name,                  ║
║                  ║   'local', '', '{}', 'active', 0, now, now)                   ║
║                  ║ Then: Reload(ctx)                                              ║
╠══════════════════╬════════════════════════════════════════════════════════════════╣
║ EnsureShard      ║ INSERT OR IGNORE INTO shards VALUES(...)                      ║
║                  ║ Same defaults as CreateShard                                   ║
║                  ║ Then: Reload(ctx)                                              ║
║                  ║ NOTE: does NOT overwrite existing rows                         ║
╠══════════════════╬════════════════════════════════════════════════════════════════╣
║ SetStrategy      ║ UPDATE shards SET strategy=?, endpoint=?, config=?,           ║
║                  ║   updated_at=? WHERE id=?                                     ║
║                  ║ Returns ErrShardNotFound if 0 rows affected                   ║
║                  ║ Then: Reload(ctx)                                              ║
╠══════════════════╬════════════════════════════════════════════════════════════════╣
║ DeleteShard      ║ 1. closeEntryLocked(dossierID) -- close live connection       ║
║                  ║ 2. UPDATE shards SET status='deleted', updated_at=? WHERE id=?║
║                  ║ 3. os.Remove({dataDir}/{dossierID}.db)                        ║
║                  ║    os.Remove({dataDir}/{dossierID}.db-wal)                    ║
║                  ║    os.Remove({dataDir}/{dossierID}.db-shm)                    ║
║                  ║ 4. Reload(ctx)                                                ║
║                  ║ Returns ErrShardNotFound if 0 rows affected                   ║
╚══════════════════╩════════════════════════════════════════════════════════════════╝

Legacy Aliases (backward compat):
  CreateSpace(ctx, userID, spaceID, name) -> CreateShard(ctx, spaceID, userID, name)
  DeleteSpace(ctx, userID, spaceID)       -> DeleteShard(ctx, spaceID)
  NOTE: parameter order swap -- userID becomes ownerID, spaceID becomes dossierID
```

---

## 15. Eviction Policies

```
╔═══════════════════════════════════════════════════════════════════════════╗
║  TWO EVICTION MECHANISMS:                                                ║
╠═══════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  1. IDLE REAPER (reapLoop goroutine)                                     ║
║     ┌─────────────────────────────────────────────────────────────────┐  ║
║     │ Runs every: idleTimeout / 2  (min 50ms)                        │  ║
║     │ Evicts if:  now - lastUsed > idleTimeout                       │  ║
║     │ Skips:      entries with cancel != nil (active watcher)        │  ║
║     │ Effect:     closeEntryLocked() + evictions++                   │  ║
║     └─────────────────────────────────────────────────────────────────┘  ║
║                                                                          ║
║  2. LRU EVICTION ON maxOpen (in Resolve)                                 ║
║     ┌─────────────────────────────────────────────────────────────────┐  ║
║     │ Triggered:  len(conns) >= maxOpen during Resolve                │  ║
║     │ Evicts:     entry with smallest lastUsed (oldest idle)         │  ║
║     │ If none evictable: return ErrPoolExhausted                     │  ║
║     │ Effect:     closeEntryLocked() + evictions++                   │  ║
║     └─────────────────────────────────────────────────────────────────┘  ║
║                                                                          ║
║  closeEntryLocked(key):                                                  ║
║    1. Cancel watcher if present (entry.cancel())                         ║
║    2. Call closeFn() (factory-provided cleanup, typically db.Close())     ║
║    3. delete(conns, key)                                                 ║
╚═══════════════════════════════════════════════════════════════════════════╝
```

---

## 16. Concurrency Model

```
╔═══════════════════════════════════════════════════════════════════════════╗
║  LOCKING STRATEGY                                                        ║
╠═══════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  sync.RWMutex (p.mu) protects:                                           ║
║    - conns map                                                           ║
║    - shardSnap map                                                       ║
║    - factories map                                                       ║
║                                                                          ║
║  RLock used for:                                                         ║
║    - Cache lookup in Resolve (fast path)                                 ║
║    - Snapshot lookup in Resolve                                          ║
║    - AdminHandler GET endpoints                                          ║
║    - Stats() open conn count                                             ║
║                                                                          ║
║  Lock (write) used for:                                                  ║
║    - Creating new connections (Resolve slow path)                        ║
║    - Eviction (reaper + LRU)                                             ║
║    - Reload (snapshot swap + stale conn close)                           ║
║    - RegisterFactory                                                     ║
║    - Close                                                               ║
║    - DeleteShard (closeEntryLocked)                                      ║
║    - ResolveWithWatch (attaching cancel func)                            ║
║                                                                          ║
║  Double-checked locking in Resolve:                                      ║
║    RLock -> check cache -> RUnlock                                       ║
║    Lock  -> re-check cache -> create if needed -> Unlock                 ║
║                                                                          ║
║  Atomic counters (no lock needed):                                       ║
║    totalResolves, cacheHits, cacheMisses,                                ║
║    evictions, factoryErrors, reloads, closed                             ║
║    entry.lastUsed                                                        ║
║                                                                          ║
║  Goroutines:                                                             ║
║    - reapLoop (1, started in New, stopped by closeCh)                    ║
║    - Watch (0-1, started by consumer, stopped by ctx or closeCh)         ║
║    - per-shard watcher (0-N, started by ResolveWithWatch,                ║
║      stopped by entry.cancel)                                            ║
╚═══════════════════════════════════════════════════════════════════════════╝
```

---

## 17. External Dependencies

```
╔═══════════════════════════════════════════════════════════════╗
║  go.mod: github.com/hazyhaar/usertenant                      ║
║  Go version: 1.23.0                                          ║
╠═══════════════════════════════════════════════════════════════╣
║  DIRECT:                                                     ║
║    modernc.org/sqlite v1.34.5   (pure Go SQLite driver)      ║
║                                                              ║
║  INDIRECT (via modernc.org/sqlite):                          ║
║    github.com/dustin/go-humanize v1.0.1                      ║
║    github.com/google/uuid v1.6.0                             ║
║    github.com/mattn/go-isatty v0.0.20                        ║
║    github.com/ncruces/go-strftime v0.1.9                     ║
║    github.com/remyoudompheng/bigfft v0.0.0-20230129          ║
║    golang.org/x/sys v0.22.0                                  ║
║    modernc.org/libc v1.55.3                                  ║
║    modernc.org/mathutil v1.6.0                               ║
║    modernc.org/memory v1.8.0                                 ║
║                                                              ║
║  INTERNAL sub-packages (same module):                        ║
║    github.com/hazyhaar/usertenant/dbopen                     ║
║    github.com/hazyhaar/usertenant/watch                      ║
║                                                              ║
║  hazyhaar/pkg sub-packages used: NONE                        ║
║  (usertenant is a standalone library with zero hazyhaar/pkg  ║
║   dependency -- only modernc.org/sqlite)                     ║
╚═══════════════════════════════════════════════════════════════╝
```

---

## 18. Configuration

```
╔═══════════════════════════════════════════════════════════════════════════╗
║  Pool Configuration (via functional options to New())                     ║
╠═══════════════════╦═══════════════╦═══════════════════════════════════════╣
║ Option            ║ Default       ║ Effect                               ║
╠═══════════════════╬═══════════════╬═══════════════════════════════════════╣
║ WithIdleTimeout(d)║ 5 minutes     ║ Idle connections evicted after d     ║
║ WithMaxOpen(n)    ║ 256           ║ Max simultaneous open shard DBs      ║
║ WithLogger(l)     ║ slog.Default()║ Structured logger for pool events    ║
╠═══════════════════╩═══════════════╩═══════════════════════════════════════╣
║                                                                          ║
║  dbopen Configuration (via functional options to Open())                 ║
╠═══════════════════╦═══════════════╦═══════════════════════════════════════╣
║ Option            ║ Default       ║ Effect                               ║
╠═══════════════════╬═══════════════╬═══════════════════════════════════════╣
║ WithBusyTimeout(n)║ 5000 ms       ║ PRAGMA busy_timeout                  ║
║ WithJournalMode(m)║ "WAL"         ║ PRAGMA journal_mode                  ║
║ WithForeignKeys(b)║ true          ║ PRAGMA foreign_keys                  ║
║ WithReadOnly()    ║ false         ║ mode=ro + PRAGMA query_only(1)       ║
║ WithPragma(s)     ║ none          ║ Additional custom pragma             ║
╠═══════════════════╩═══════════════╩═══════════════════════════════════════╣
║                                                                          ║
║  No env vars. No CLI flags. No config files.                             ║
║  All configuration is programmatic via Go function calls.                ║
╚═══════════════════════════════════════════════════════════════════════════╝
```

---

## 19. Source File Map

```
╔════════════════════════════════════════════════════════════════════════════════╗
║  FILE LAYOUT                                                                  ║
╠════════════════════════════════════════════════════════════════════════════════╣
║                                                                               ║
║  usertenant/                                                                  ║
║  ├── CLAUDE.md             Project manifest for AI agents                     ║
║  ├── go.mod                Module: github.com/hazyhaar/usertenant             ║
║  ├── go.sum                                                                   ║
║  │                                                                            ║
║  ├── types.go              Pool, ShardFactory, shard, entry, PoolStats,       ║
║  │                         Option, WithIdleTimeout, WithMaxOpen, WithLogger   ║
║  ├── schema.go             catalogDDL constant, InitCatalog()                 ║
║  ├── errors.go             8 sentinel errors + 4 legacy aliases               ║
║  ├── pool.go               New(), RegisterFactory(), Stats(), Close()         ║
║  ├── resolve.go            Resolve(), ResolveWithWatch(), evictOldestLocked(),║
║  │                         closeEntryLocked()                                 ║
║  ├── factory.go            localFactory, readonlyFactory, noopFactory         ║
║  ├── lifecycle.go          CreateShard, EnsureShard, SetStrategy, DeleteShard,║
║  │                         CreateSpace (legacy), DeleteSpace (legacy)         ║
║  ├── reload.go             Reload(), Watch()                                  ║
║  ├── reaper.go             reapLoop(), reap()                                 ║
║  ├── admin.go              AdminHandler(), writeJSON()                        ║
║  ├── tenant_test.go        Comprehensive test suite (25+ test functions)      ║
║  │                                                                            ║
║  ├── dbopen/                                                                  ║
║  │   └── dbopen.go         Open(), Options, WithReadOnly, WithBusyTimeout,    ║
║  │                         WithJournalMode, WithForeignKeys, WithPragma       ║
║  │                                                                            ║
║  └── watch/                                                                   ║
║      └── watch.go          Watcher, New(), Run(), dataVersion()               ║
║                                                                               ║
║  NO cmd/ directory. NO binaries. Pure library.                                ║
╚════════════════════════════════════════════════════════════════════════════════╝
```

---

## 20. Consumer Integration Pattern

```
   ┌────────────────────────────────────────────────────────────────────────────┐
   │  TYPICAL CONSUMER CODE (e.g. chrc, siftrag)                               │
   │                                                                           │
   │  import (                                                                 │
   │      tenant "github.com/hazyhaar/usertenant"                              │
   │      "github.com/hazyhaar/usertenant/dbopen"                              │
   │  )                                                                        │
   │                                                                           │
   │  // 1. Open catalog                                                       │
   │  catalogDB, _ := dbopen.Open("/data/shards/_catalog.db")                  │
   │  tenant.InitCatalog(ctx, catalogDB)                                       │
   │                                                                           │
   │  // 2. Create pool                                                        │
   │  pool, _ := tenant.New("/data/shards", catalogDB,                         │
   │      tenant.WithIdleTimeout(10*time.Minute),                              │
   │      tenant.WithMaxOpen(128),                                             │
   │  )                                                                        │
   │  defer pool.Close()                                                       │
   │                                                                           │
   │  // 3. (optional) Register custom factories                               │
   │  pool.RegisterFactory("remote", myRemoteFactory)                          │
   │                                                                           │
   │  // 4. (optional) Start catalog watcher                                   │
   │  go pool.Watch(ctx, 200*time.Millisecond)                                 │
   │                                                                           │
   │  // 5. (optional) Mount admin HTTP                                        │
   │  mux.Handle("/admin/", http.StripPrefix("/admin", tenant.AdminHandler(p)))│
   │                                                                           │
   │  // 6. Route requests                                                     │
   │  db, _ := pool.Resolve(ctx, dossierID)                                    │
   │  db.QueryRow("SELECT ...")                                                │
   │                                                                           │
   │  // 7. Manage shards                                                      │
   │  pool.CreateShard(ctx, newDossierID, ownerID, name)                       │
   │  pool.EnsureShard(ctx, dossierID, ownerID, name) // idempotent            │
   │  pool.DeleteShard(ctx, dossierID)                                         │
   │  pool.SetStrategy(ctx, dossierID, "readonly", "", nil)                    │
   └────────────────────────────────────────────────────────────────────────────┘
```

---

## 21. Key Invariants and Constraints

```
1. ONE FILE PER DOSSIER         -- {dataDir}/{dossierID}.db, flat layout, no nesting
2. dossierID IS THE ONLY KEY    -- no composite routing (userID+spaceID is legacy)
3. CATALOG IS AUTHORITATIVE     -- shardSnap is always rebuilt from SELECT * FROM shards
4. CONNECTIONS ARE LAZY         -- created on first Resolve(), not on Reload()
5. FINGERPRINT DIFFING          -- strategy|endpoint|config|status change = close + rebuild
6. WATCHERS PREVENT REAPING     -- entries with cancel != nil survive idle reaper
7. SAME-CONN WRITES INVISIBLE   -- PRAGMA data_version misses them, Reload() called explicitly
8. CGO_ENABLED=0               -- modernc.org/sqlite only, never mattn/go-sqlite3
9. PRAGMAS VIA DSN _pragma=    -- per-connection in pool, not post-Open db.Exec("PRAGMA")
10. POOL CLOSE IS IDEMPOTENT    -- sync.Once guards Close()
```

---

*Generated 2026-03-01. No sub-schema files (no cmd/ directories found).*
