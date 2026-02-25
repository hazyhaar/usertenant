# watch

Responsabilite: Watcher polling qui detecte les changements SQLite via PRAGMA data_version et appelle un callback quand la version change (modification par une autre connexion).
Depend de: `database/sql` uniquement
Dependants: `usertenant` (package parent, pour hot-reload des configs tenant)
Point d'entree: `watch.go` (New, Run)
Types cles: `Watcher`
Invariants: Utilise PRAGMA data_version (pas inotify/fsnotify) pour la portabilite. Bloque jusqu'a annulation du context. L'intervalle de poll est configurable.
NE PAS: Utiliser fsnotify/inotify (le PRAGMA data_version est plus fiable pour SQLite). Appeler Run() sans context cancellable.
