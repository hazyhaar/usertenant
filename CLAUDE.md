# usertenant

Responsabilité: Bibliothèque Go de tenant routing avec pool de connexions SQLite shardées — un fichier SQLite par dossier (dossierID = clé universelle).
Module: `github.com/hazyhaar/usertenant`
Repo: `github.com/hazyhaar/usertenant` (privé)

Dépend de: `modernc.org/sqlite` uniquement
Dépendants: `chrc` (veille)

## API publique

| Méthode | Signature |
|---------|-----------|
| `Resolve` | `(ctx, dossierID) → (*sql.DB, error)` |
| `ResolveWithWatch` | `(ctx, dossierID, interval, onChange) → (*sql.DB, error)` |
| `CreateShard` | `(ctx, dossierID, ownerID, name) → error` |
| `DeleteShard` | `(ctx, dossierID) → error` |
| `SetStrategy` | `(ctx, dossierID, strategy, endpoint, config) → error` |

Legacy aliases : `CreateSpace`, `DeleteSpace` (redirigent vers CreateShard/DeleteShard).

## Schema catalog

```sql
CREATE TABLE shards (
    id TEXT PRIMARY KEY,         -- dossierID (UUID v7)
    owner_id TEXT NOT NULL,      -- audit only
    name, strategy, endpoint, config, status, size_bytes, created_at, updated_at
);
```

Pas de table `users` — l'auth est gérée par le SaaS, pas par usertenant.

## Layout fichiers

`{dataDir}/{dossierID}.db` — flat, un fichier par shard.

## Types clés

- `Pool` — pool de connexions, cache LRU, reaper idle
- `ShardFactory` — `func(dataDir, dossierID, endpoint string, config json.RawMessage) (*sql.DB, func(), error)`
- `shard` — metadata snapshot (ID, OwnerID, Strategy, Status, ...)

## Build / Test

```bash
CGO_ENABLED=0 go build ./...
go test ./... -count=1
```

## Invariants

- Un fichier SQLite par dossier (sharding strict, layout flat)
- Pool avec reaper pour cleanup automatique
- Hot-reload de config via PRAGMA data_version
- `dossierID` est la seule clé de routing (pas de composite)

## NE PAS

- Partager un fichier SQLite entre dossiers
- Utiliser `mattn/go-sqlite3` (toujours `modernc.org/sqlite`)
- Utiliser `(userID, spaceID)` — API migrée vers `dossierID` seul
