// Package migrate applies versioned SQL migrations from the embedded filesystem
// to a PostgreSQL database. It follows the same directory convention used by
// the Java/Flyway services in the AgentHub platform:
//
//	db/migrations/public/{version}/V{version}.{seq}__description.sql   → agenthub schema
//	db/migrations/schemas/{version}/V{version}.{seq}__description.sql  → ah_{tenantId} schema
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"sort"
	"strings"

	embeddb "github.com/agenthub/mcp-client-runtime/db"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const createMigrationsTable = `
CREATE TABLE IF NOT EXISTS _schema_migrations (
    id          SERIAL      PRIMARY KEY,
    filename    VARCHAR(255) NOT NULL UNIQUE,
    applied_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
)`

// Migrator applies SQL migrations from the embedded filesystem.
type Migrator struct {
	db           *sql.DB
	tenantSchema string
}

// New creates a Migrator for the given database connection and tenant ID.
// The tenant schema is derived as "ah_" + tenantID.
func New(db *sql.DB, tenantID string) *Migrator {
	return &Migrator{
		db:           db,
		tenantSchema: "ah_" + tenantID,
	}
}

// Run applies all pending migrations.
// Public migrations run against the "agenthub" schema.
// Schema migrations run against the tenant schema (ah_{tenantId}).
func (m *Migrator) Run(ctx context.Context) error {
	log.Printf("migrate: running public migrations (schema=agenthub)")
	if err := m.applyDir(ctx, "migrations/public", "agenthub"); err != nil {
		return fmt.Errorf("public migrations: %w", err)
	}

	log.Printf("migrate: running schema migrations (schema=%s)", m.tenantSchema)
	if err := m.applyDir(ctx, "migrations/schemas", m.tenantSchema); err != nil {
		return fmt.Errorf("schema migrations (schema=%s): %w", m.tenantSchema, err)
	}

	return nil
}

// applyDir collects all .sql files under dir (recursively, sorted lexicographically),
// then applies any that have not been recorded in _schema_migrations.
func (m *Migrator) applyDir(ctx context.Context, dir string, schema string) error {
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Close()

	// Pin the connection's search_path for the duration of this function.
	if _, err := conn.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		// Schema may not exist yet for tenant schemas on first run — try creating it.
		if strings.Contains(schema, "ah_") {
			if _, err2 := m.db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema); err2 != nil {
				return fmt.Errorf("create schema %s: %w", schema, err2)
			}
			if _, err = conn.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
				return fmt.Errorf("set search_path to %s: %w", schema, err)
			}
		} else {
			return fmt.Errorf("set search_path to %s: %w", schema, err)
		}
	}

	// Ensure the migrations tracking table exists in this schema.
	if _, err := conn.ExecContext(ctx, createMigrationsTable); err != nil {
		return fmt.Errorf("create _schema_migrations: %w", err)
	}

	// Collect and sort SQL files.
	files, err := collectSQLFiles(dir)
	if err != nil {
		return err
	}

	for _, path := range files {
		filename := filepath.Base(path)

		// Skip already-applied migrations.
		var count int
		if err := conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM _schema_migrations WHERE filename = $1", filename,
		).Scan(&count); err != nil {
			return fmt.Errorf("check %s: %w", filename, err)
		}
		if count > 0 {
			continue
		}

		// Read SQL content from the embedded FS.
		content, err := embeddb.FS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		// Apply in a transaction and record.
		if err := applyMigration(ctx, conn, filename, string(content)); err != nil {
			return err
		}

		log.Printf("migrate: applied %s → %s", filename, schema)
	}

	return nil
}

// applyMigration executes sql inside a transaction and records it in _schema_migrations.
func applyMigration(ctx context.Context, conn *sql.Conn, filename, sqlContent string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for %s: %w", filename, err)
	}

	if _, err := tx.ExecContext(ctx, sqlContent); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("execute %s: %w", filename, err)
	}

	if _, err := tx.ExecContext(ctx,
		"INSERT INTO _schema_migrations (filename) VALUES ($1)", filename,
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record %s: %w", filename, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", filename, err)
	}
	return nil
}

// collectSQLFiles walks dir in the embedded FS and returns .sql file paths sorted
// lexicographically (which preserves the V{version}.{seq} ordering convention).
func collectSQLFiles(dir string) ([]string, error) {
	var files []string

	err := fs.WalkDir(embeddb.FS, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".sql") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", dir, err)
	}

	sort.Strings(files)
	return files, nil
}
