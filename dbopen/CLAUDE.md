# dbopen

Responsabilite: Utilitaire d'ouverture de bases SQLite avec PRAGMAs DSN `_pragma=` (per-connection), `_txlock=immediate`, et options fonctionnelles.
Depend de: `modernc.org/sqlite`
Dependants: `usertenant` (package parent), HORAG (via usertenant pool)
Point d'entree: `dbopen.go` (Open)
Types cles: `Options`, `Option`
Invariants:
- Pragmas appliques via DSN `_pragma=` — chaque connexion du pool les recoit
- DSN type : `path?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)`
- `_txlock=immediate` : toutes les transactions utilisent `BEGIN IMMEDIATE`
- Defaults : WAL mode, busy_timeout=5000ms, foreign_keys=ON
- Options fonctionnelles : WithReadOnly, WithBusyTimeout, WithJournalMode, WithForeignKeys, WithPragma
NE PAS:
- Utiliser mattn/go-sqlite3 (toujours modernc.org/sqlite)
- Appeler `db.Exec("PRAGMA ...")` dans le code client — les pragmas sont dans le DSN
- Utiliser `db.Begin()` — double violation (pas de context + DEFERRED)
- Ouvrir des DBs sans WAL mode sauf raison explicite
Pourquoi DSN et pas Exec:
- `database/sql` pool les connexions. `db.Exec("PRAGMA ...")` ne touche qu'UNE connexion.
- Les shards ouverts par usertenant ont N connexions — sans DSN, certaines n'ont pas busy_timeout.
- Bug prod 2026-02-25 : 4 workers HORAG, SQLITE_BUSY instantane sur les shards.
