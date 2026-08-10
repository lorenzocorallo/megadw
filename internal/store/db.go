package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// DB is the application's SQLite handle. SQLite is kept behind this small
// wrapper so repositories share one migration and transaction boundary.
type DB struct {
	*sql.DB
	path                   string
	writeTransactions      atomic.Uint64
	checkpointTransactions atomic.Uint64
}

// Stats is a low-cardinality release diagnostic snapshot. It is intentionally
// process-local and is not exposed as a metrics endpoint; the resource
// benchmark uses it to verify that progress is checkpointed in bounded
// transactions.
type Stats struct {
	WriteTransactions      uint64
	CheckpointTransactions uint64
}

// Open opens or creates a SQLite database, applies the required durability
// pragmas, and runs all pending migrations before returning.
func Open(ctx context.Context, path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("database path is required")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	// SQLite PRAGMAs such as foreign_keys and busy_timeout are connection
	// scoped. Keep one bounded connection until a connector can apply them to
	// every future connection; this preserves the integrity contract even when
	// requests arrive concurrently. A plain :memory: database is connection
	// local for the same reason.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	database := &DB{DB: sqlDB, path: path}
	closeOnError := func(err error) (*DB, error) {
		_ = sqlDB.Close()
		return nil, err
	}
	for _, pragma := range []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA synchronous = NORMAL`,
	} {
		if _, err := sqlDB.ExecContext(ctx, pragma); err != nil {
			return closeOnError(fmt.Errorf("configure SQLite (%s): %w", pragma, err))
		}
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return closeOnError(fmt.Errorf("ping SQLite: %w", err))
	}
	if err := migrate(ctx, sqlDB); err != nil {
		return closeOnError(err)
	}
	return database, nil
}

// Path returns the path used to open the database.
func (d *DB) Path() string {
	if d == nil {
		return ""
	}
	return d.path
}

// Stats returns successful write-transaction counters since the database was
// opened. Read queries and per-progress writes outside WithTx are not counted
// as transactions; checkpoint transactions are a subset of write transactions.
func (d *DB) Stats() Stats {
	if d == nil {
		return Stats{}
	}
	return Stats{
		WriteTransactions:      d.writeTransactions.Load(),
		CheckpointTransactions: d.checkpointTransactions.Load(),
	}
}

// WithTx executes fn in a transaction and rolls back when fn returns an
// error. It is used for multi-row state changes that must be atomic.
func (d *DB) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if d == nil || d.DB == nil {
		return fmt.Errorf("database is nil")
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	d.writeTransactions.Add(1)
	return nil
}
