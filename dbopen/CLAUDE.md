# dbopen

Responsabilite: Utilitaire d'ouverture de bases SQLite avec PRAGMAs par defaut sensibles (WAL, busy_timeout, foreign_keys) et options fonctionnelles.
Depend de: `modernc.org/sqlite`
Dependants: `usertenant` (package parent)
Point d'entree: `dbopen.go` (Open)
Types cles: `Options`, `Option`
Invariants: Les defaults sont WAL mode, busy_timeout=5000ms, foreign_keys=ON. Les options fonctionnelles (WithReadOnly, WithBusyTimeout, WithJournalMode, WithForeignKeys) permettent de personnaliser.
NE PAS: Utiliser mattn/go-sqlite3 (toujours modernc.org/sqlite). Ouvrir des DBs sans WAL mode sauf raison explicite.
