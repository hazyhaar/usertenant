// CLAUDE:SUMMARY Polling watcher that detects SQLite changes via PRAGMA data_version and fires a callback.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS Watcher, New, Run
//
// Package watch provides a polling watcher that monitors SQLite databases
// for changes using PRAGMA data_version. When data_version changes (meaning
// another connection modified the database), the watcher calls a user-provided
// callback.
package watch

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// Watcher polls a SQLite database's PRAGMA data_version and calls onChange
// when the version changes, indicating the database was modified by another
// connection.
type Watcher struct {
	db       *sql.DB
	interval time.Duration
	onChange func() error
	logger   *slog.Logger
}

// New creates a new Watcher that polls db every interval and calls onChange
// when data_version changes.
func New(db *sql.DB, interval time.Duration, onChange func() error, logger *slog.Logger) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Watcher{
		db:       db,
		interval: interval,
		onChange: onChange,
		logger:   logger,
	}
}

// Run starts the polling loop. It blocks until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) error {
	lastVersion, err := w.dataVersion(ctx)
	if err != nil {
		return fmt.Errorf("watch: initial data_version: %w", err)
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			v, err := w.dataVersion(ctx)
			if err != nil {
				w.logger.Warn("watch: data_version poll failed", "error", err)
				continue
			}
			if v != lastVersion {
				lastVersion = v
				if err := w.onChange(); err != nil {
					w.logger.Error("watch: onChange callback failed", "error", err)
				}
			}
		}
	}
}

func (w *Watcher) dataVersion(ctx context.Context) (int64, error) {
	var v int64
	err := w.db.QueryRowContext(ctx, "PRAGMA data_version").Scan(&v)
	return v, err
}
