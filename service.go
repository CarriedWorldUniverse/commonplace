// Package commonplace is the CWB knowledge pillar: a herald-authed HTTP
// service where aspects store, retrieve, and semantically search
// knowledge. SQLite (pure-Go, ncruces driver) holds content+metadata
// +FTS5; a sibling vector table holds embeddings for hybrid retrieval.
package commonplace

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed" // pure-Go WASM SQLite runtime
)

// Config carries the service's runtime configuration.
type Config struct {
	// DBPath is the on-disk location of commonplace.db (":memory:" ok for tests).
	DBPath string
	// Embedder produces vectors for store + search. Required.
	Embedder Embedder
}

// Service is the commonplace knowledge service.
type Service struct {
	cfg      Config
	db       *sql.DB
	embedder Embedder
}

// New opens (or creates) commonplace.db, applies the embedded schema,
// and returns a ready Service. applySchema is idempotent.
func New(ctx context.Context, cfg Config) (*Service, error) {
	if cfg.DBPath == "" {
		return nil, fmt.Errorf("commonplace.New: DBPath required")
	}
	if cfg.Embedder == nil {
		return nil, fmt.Errorf("commonplace.New: Embedder required")
	}
	dsn := "file:" + cfg.DBPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	db, err := driver.Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("commonplace.New: open %s: %w", cfg.DBPath, err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("commonplace.New: ping: %w", err)
	}
	if err := applySchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Service{cfg: cfg, db: db, embedder: cfg.Embedder}, nil
}

// Close releases the DB handle.
func (s *Service) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
