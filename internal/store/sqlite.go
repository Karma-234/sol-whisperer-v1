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

type SpikeEventRecord struct {
	ID            string
	Mint          string
	Ratio         float64
	WindowVolume  float64
	BaselinePer5m float64
	UniqueWallets int
	CreatedAt     time.Time
}

type ListenerRecord struct {
	ID               string
	UserID           string
	Mint             string
	Symbol           string
	AutoSnipeEnabled bool
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

CREATE UNIQUE INDEX IF NOT EXISTS idx_listeners_user_mint
ON listeners(user_id, mint);

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

CREATE TABLE IF NOT EXISTS spike_events (
  id TEXT PRIMARY KEY,
  mint TEXT NOT NULL,
  ratio REAL NOT NULL,
  window_volume_sol REAL NOT NULL,
  baseline_per5m_sol REAL NOT NULL,
  unique_wallets INTEGER NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func (s *SQLiteStore) InsertVolumeSnapshot(ctx context.Context, mint string, windowStart time.Time, windowEnd time.Time, volumeSOL float64, uniqueWallets int) error {
	const query = `
INSERT INTO volume_snapshots (mint, window_start, window_end, volume_sol, unique_wallets)
VALUES (?, ?, ?, ?, ?)
`
	_, err := s.db.ExecContext(ctx, query, mint, windowStart.UTC(), windowEnd.UTC(), volumeSOL, uniqueWallets)
	if err != nil {
		return fmt.Errorf("insert volume snapshot: %w", err)
	}
	return nil
}

func (s *SQLiteStore) InsertSpikeEvent(ctx context.Context, rec SpikeEventRecord) error {
	const query = `
INSERT INTO spike_events (id, mint, ratio, window_volume_sol, baseline_per5m_sol, unique_wallets, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
`
	_, err := s.db.ExecContext(ctx, query, rec.ID, rec.Mint, rec.Ratio, rec.WindowVolume, rec.BaselinePer5m, rec.UniqueWallets, rec.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("insert spike event: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetRecentSpikeEvents(ctx context.Context, limit int) ([]SpikeEventRecord, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT id, mint, ratio, window_volume_sol, baseline_per5m_sol, unique_wallets, created_at
FROM spike_events
ORDER BY created_at DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent spike events: %w", err)
	}
	defer rows.Close()

	result := make([]SpikeEventRecord, 0, limit)
	for rows.Next() {
		var r SpikeEventRecord
		if scanErr := rows.Scan(&r.ID, &r.Mint, &r.Ratio, &r.WindowVolume, &r.BaselinePer5m, &r.UniqueWallets, &r.CreatedAt); scanErr != nil {
			return nil, fmt.Errorf("scan spike event row: %w", scanErr)
		}
		result = append(result, r)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate spike event rows: %w", rowsErr)
	}
	return result, nil
}

func (s *SQLiteStore) UpsertListener(ctx context.Context, rec ListenerRecord) error {
	const query = `
INSERT INTO listeners (id, user_id, mint, symbol, auto_snipe_enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(user_id, mint) DO UPDATE SET
  symbol = excluded.symbol,
  auto_snipe_enabled = excluded.auto_snipe_enabled,
  updated_at = CURRENT_TIMESTAMP
`

	auto := 0
	if rec.AutoSnipeEnabled {
		auto = 1
	}
	_, err := s.db.ExecContext(ctx, query, rec.ID, rec.UserID, rec.Mint, rec.Symbol, auto)
	if err != nil {
		return fmt.Errorf("upsert listener: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DeleteListener(ctx context.Context, userID string, mint string) error {
	const query = `DELETE FROM listeners WHERE user_id = ? AND mint = ?`
	_, err := s.db.ExecContext(ctx, query, userID, mint)
	if err != nil {
		return fmt.Errorf("delete listener: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListActiveListenerMints(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT mint FROM listeners`)
	if err != nil {
		return nil, fmt.Errorf("query active listener mints: %w", err)
	}
	defer rows.Close()

	out := make([]string, 0, 64)
	for rows.Next() {
		var mint string
		if scanErr := rows.Scan(&mint); scanErr != nil {
			return nil, fmt.Errorf("scan active listener mint: %w", scanErr)
		}
		out = append(out, mint)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate active listener mints: %w", rowsErr)
	}
	return out, nil
}
