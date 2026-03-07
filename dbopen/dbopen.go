// CLAUDE:SUMMARY Opens SQLite databases with sensible default PRAGMAs (WAL, busy_timeout, foreign_keys).
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS Open, Option, WithReadOnly, WithBusyTimeout, WithJournalMode, WithForeignKeys, WithPragma
//
// Package dbopen provides utilities for opening SQLite databases with
// sensible defaults.
package dbopen

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Options configures how a database is opened.
type Options struct {
	ReadOnly     bool
	BusyTimeout  int // milliseconds, default 5000
	JournalMode  string
	ForeignKeys  bool
	ExtraPragmas []string
}

// Option is a functional option for Open.
type Option func(*Options)

// WithReadOnly opens the database in read-only mode (PRAGMA query_only=ON).
func WithReadOnly() Option {
	return func(o *Options) {
		o.ReadOnly = true
	}
}

// WithBusyTimeout sets the busy_timeout pragma in milliseconds.
func WithBusyTimeout(ms int) Option {
	return func(o *Options) {
		o.BusyTimeout = ms
	}
}

// WithJournalMode sets the journal_mode pragma.
func WithJournalMode(mode string) Option {
	return func(o *Options) {
		o.JournalMode = mode
	}
}

// WithForeignKeys enables or disables foreign key enforcement.
func WithForeignKeys(on bool) Option {
	return func(o *Options) {
		o.ForeignKeys = on
	}
}

// WithPragma adds a custom pragma statement to execute after opening.
func WithPragma(pragma string) Option {
	return func(o *Options) {
		o.ExtraPragmas = append(o.ExtraPragmas, pragma)
	}
}

func defaults() Options {
	return Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		ForeignKeys: true,
	}
}

// Open opens a SQLite database at path with the given options.
// It applies default pragmas (WAL, busy_timeout=5000, foreign_keys=ON)
// and any additional options provided.
func Open(path string, opts ...Option) (*sql.DB, error) {
	o := defaults()
	for _, fn := range opts {
		fn(&o)
	}

	// Build DSN with _txlock=immediate and _pragma= for all production pragmas.
	// _pragma= is applied per-connection by the modernc driver — critical because
	// database/sql pools connections, and post-Open db.Exec("PRAGMA ...") only
	// hits one connection (other connections get no busy_timeout → instant SQLITE_BUSY).
	dsn := path + "?_txlock=immediate"
	dsn += fmt.Sprintf("&_pragma=busy_timeout(%d)", o.BusyTimeout)
	dsn += fmt.Sprintf("&_pragma=journal_mode(%s)", o.JournalMode)
	dsn += "&_pragma=synchronous(NORMAL)"
	if o.ForeignKeys {
		dsn += "&_pragma=foreign_keys(1)"
	}
	if o.ReadOnly {
		dsn += "&mode=ro&_pragma=query_only(1)"
	}
	for _, p := range o.ExtraPragmas {
		dsn += "&_pragma=" + p
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("dbopen: open %s: %w", path, err)
	}

	// Force a connection so the file is created and pragmas are applied.
	// Without this, sql.Open is lazy — the file doesn't exist until the first query.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("dbopen: ping %s: %w", path, err)
	}

	return db, nil
}
