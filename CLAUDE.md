> **Protocole** — Avant toute tâche, lire [`../CLAUDE.md`](../CLAUDE.md) §Protocole de recherche.
> Commandes obligatoires : `Read <dossier>/CLAUDE.md` → `Grep "CLAUDE:SUMMARY"` → `Grep "CLAUDE:WARN" <fichier>`.
> **Interdit** : Bash(grep/cat/find) au lieu de Grep/Read. Ne jamais lire un fichier entier en première intention.

> **Schema technique** : voir [`usertenant_schem.md`](usertenant_schem.md) — lecture prioritaire avant tout code source.

# usertenant

Responsabilité: Bibliothèque Go de tenant routing avec pool de connexions SQLite shardées — un fichier SQLite par dossier (dossierID = clé universelle).
Module: `github.com/hazyhaar/usertenant`
Repo: `github.com/hazyhaar/usertenant` (privé)

Dépend de: `modernc.org/sqlite` uniquement
Dépendants: `chrc` (veille), HORAG, siftrag

## API publique

| Méthode | Signature |
|---------|-----------|
| `Resolve` | `(ctx, dossierID) → (*sql.DB, error)` |
| `ResolveWithOwner` | `(ctx, dossierID, ownerID) → (*sql.DB, error)` |
| `ResolveWithWatch` | `(ctx, dossierID, interval, onChange) → (*sql.DB, error)` |
| `CreateShard` | `(ctx, dossierID, ownerID, name) → error` |
| `EnsureShard` | `(ctx, dossierID, ownerID, name) → error` (idempotent) |
| `DeleteShard` | `(ctx, dossierID) → error` |
| `SetStrategy` | `(ctx, dossierID, strategy, endpoint, config) → error` |

Options: `WithIdleTimeout`, `WithMaxOpen`, `WithLogger`, `WithShardHook(func(op, dossierID string, attrs ...slog.Attr))`.
Legacy aliases : `CreateSpace`, `DeleteSpace` (redirigent vers CreateShard/DeleteShard).

## Schema

Voir `schema.sql` / `usertenant_schem.md`. Pas de table `users` — l'auth est gérée par le SaaS.

## Layout fichiers

`{dataDir}/{dossierID}.db` — flat, un fichier par shard.

## Types clés

- `Pool` — pool de connexions, cache LRU, reaper idle
- `ShardFactory` — `func(dataDir, dossierID, endpoint string, config json.RawMessage) (*sql.DB, func(), error)`
- `shard` — metadata snapshot (ID, OwnerID, Strategy, Status, ...)

## Build / Test

```bash
CGO_ENABLED=0 go build ./...
go test -race ./... -count=1
```

## Invariants

- Un fichier SQLite par dossier (sharding strict, layout flat)
- Pool avec reaper pour cleanup automatique
- Hot-reload de config via PRAGMA data_version
- `dossierID` est la seule clé de routing (pas de composite)
- `WithShardHook` : callback optionnel sur events lifecycle (resolved, opened, evicted, created, deleted, error)

## NE PAS

- Partager un fichier SQLite entre dossiers
- Utiliser `mattn/go-sqlite3` (toujours `modernc.org/sqlite`)
- Utiliser `(userID, spaceID)` — API migrée vers `dossierID` seul
