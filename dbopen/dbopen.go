// Package dbopen provides utilities for opening SQLite databases with
// sensible defaults.
package dbopen

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
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

	dsn := path
	if o.ReadOnly {
		dsn += "?mode=ro"
	}

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("dbopen: open %s: %w", path, err)
	}

	pragmas := []string{
		fmt.Sprintf("PRAGMA busy_timeout = %d", o.BusyTimeout),
		fmt.Sprintf("PRAGMA journal_mode = %s", o.JournalMode),
	}
	if o.ForeignKeys {
		pragmas = append(pragmas, "PRAGMA foreign_keys = ON")
	}
	if o.ReadOnly {
		pragmas = append(pragmas, "PRAGMA query_only = ON")
	}
	pragmas = append(pragmas, o.ExtraPragmas...)

	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("dbopen: pragma %q on %s: %w", p, path, err)
		}
	}

	return db, nil
}
