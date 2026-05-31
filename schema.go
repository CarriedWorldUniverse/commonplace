package commonplace

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed schema.sql
var schemaSQL string

// applySchema runs the embedded DDL statement-by-statement. Idempotent:
// every statement is IF NOT EXISTS, so re-running on an existing DB is a
// no-op. Splitting on ';' is safe because schema.sql contains no triggers
// or semicolon-bearing string literals.
func applySchema(ctx context.Context, db *sql.DB) error {
	// Strip line comments first: inline `--` comments can contain ';'
	// (e.g. "-- ULID-ish; opaque id"), which would otherwise split a
	// statement mid-comment. Comment-free SQL splits safely on ';'.
	for _, raw := range strings.Split(stripSQLComments(schemaSQL), ";") {
		stmt := strings.TrimSpace(raw)
		if stmt == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("commonplace.applySchema: %s: %w", firstLine(stmt), err)
		}
	}
	return nil
}

// stripSQLComments removes line-level `--` comments. The commented-out
// entry_edge block must not reach the DB as a statement.
func stripSQLComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
