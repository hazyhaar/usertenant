# usertenant

Bibliothèque Go de tenant routing avec pool de connexions SQLite shardées — un fichier SQLite par dossier.

## Prérequis

- Go 1.24+

## Build

```bash
CGO_ENABLED=0 go build ./...
```

## Test

```bash
go test -race -count=1 ./...
```

## Dépendances

- `modernc.org/sqlite` — driver SQLite pur Go (pas de CGO)

## Usage

```go
pool := usertenant.NewPool(dataDir, opts...)
db, err := pool.Resolve(ctx, dossierID)
```

| Méthode | Description |
|---------|-------------|
| `Resolve` | Ouvre ou récupère la connexion pour un dossier |
| `CreateShard` | Crée un nouveau shard SQLite |
| `EnsureShard` | Crée si inexistant (idempotent) |
| `DeleteShard` | Supprime un shard |
| `SetStrategy` | Configure la stratégie de réplication |

Layout fichiers : `{dataDir}/{dossierID}.db` — flat, un fichier par shard.
