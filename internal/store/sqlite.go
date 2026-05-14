package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
	_ "modernc.org/sqlite"
)

// SQLiteStore centralizes persistence setup.
// Keeping DB lifecycle here avoids accidental multi-connection fragmentation.
type SQLiteStore struct {
	db     *sql.DB
	logger zerolog.Logger
}

func NewSQLite(ctx context.Context, path string, logger zerolog.Logger) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Conservative pool settings improve stability on SQLite where write
	// serialization and file locks can otherwise create noisy contention.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(10 * time.Minute)
	db.SetConnMaxLifetime(60 * time.Minute)

	store := &SQLiteStore{db: db, logger: logger.With().Str("component", "store.sqlite").Logger()}
	if err := store.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := store.bootstrapSchema(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("bootstrap sqlite schema: %w", err)
	}
	return store, nil
}

func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) PingContext(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *SQLiteStore) bootstrapSchema(ctx context.Context) error {
	// Minimal schema for step 1 foundation. Full migrations are added in later steps,
	// but these tables ensure restart-safe listener and execution metadata exists now.
	const schema = `
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS listeners (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  mint TEXT NOT NULL,
  symbol TEXT,
  auto_snipe_enabled INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sniping_configs (
  listener_id TEXT PRIMARY KEY,
  buy_amount_sol REAL NOT NULL,
  slippage_pct REAL NOT NULL,
  priority_fee_micro_lamports INTEGER NOT NULL,
  jito_tip_sol REAL NOT NULL,
  dry_run INTEGER NOT NULL DEFAULT 1,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS snipe_executions (
  id TEXT PRIMARY KEY,
  listener_id TEXT NOT NULL,
  status TEXT NOT NULL,
  bundle_id TEXT,
  mev_protection_outcome TEXT,
  error TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS volume_snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  mint TEXT NOT NULL,
  window_start DATETIME NOT NULL,
  window_end DATETIME NOT NULL,
  volume_sol REAL NOT NULL,
  unique_wallets INTEGER NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}
