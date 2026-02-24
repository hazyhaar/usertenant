# CLAUDE.md — usertenant

> **Règle n°1** — Un bug trouvé en audit mais pas par un test est d'abord une faille de test. Écrire le test rouge, puis fixer. Pas de fix sans test.

## Ce que c'est

Bibliothèque Go de tenant routing avec pool de connexions SQLite shardées. Gère l'isolation multi-tenant via un fichier SQLite par tenant.

**Module** : `github.com/hazyhaar/usertenant`
**Repo** : `github.com/hazyhaar/usertenant` (privé)

## Build / Test

```bash
CGO_ENABLED=0 go build ./...
go test ./... -count=1
```

## Particularités

- Un fichier SQLite par tenant (sharding)
- Pool de connexions avec reaper pour cleanup
- Hot-reload de config tenant
- Dépend uniquement de `modernc.org/sqlite`
