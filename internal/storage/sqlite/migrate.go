package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // CGO-free SQLite driver

	"github.com/routeflags/ai-citation-monitor/migrations"
)

// DB wraps the SQLite handle and the applied migration set.
type DB struct {
	sql  *sql.DB
	path string
}

// Open opens (and creates) the database, then applies pending migrations.
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("db path is empty")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			// The caller prints the database path, so name the step, not the
			// directory again; the wrapped error says which component failed and
			// why (MkdirAll stops at the first one it cannot create).
			return nil, fmt.Errorf("create the database directory: %w", err)
		}
	}
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db %s: %w", path, err)
	}
	handle.SetMaxOpenConns(1) // SQLite + modernc: one writer keeps behaviour predictable
	db := &DB{sql: handle, path: path}
	if err := db.Migrate(); err != nil {
		handle.Close()
		return nil, err
	}
	return db, nil
}

// Path is the file behind the handle.
func (d *DB) Path() string { return d.path }

// Close releases the handle.
func (d *DB) Close() error { return d.sql.Close() }

// SQL exposes the handle for callers that need a raw query (tests, verify).
func (d *DB) SQL() *sql.DB { return d.sql }

// Migrate applies every embedded migration that has not been applied yet.
// It is safe to run on a database created by an older build: migration 0001 is
// written with IF NOT EXISTS so it can adopt an existing file.
func (d *DB) Migrate() error {
	if err := d.ensureMigrationsTable(); err != nil {
		return err
	}
	applied, err := d.appliedMigrations()
	if err != nil {
		return err
	}
	files, err := migrationFiles()
	if err != nil {
		return err
	}
	for _, name := range files {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		if applied[version] {
			continue
		}
		if err := d.applyMigration(name, version); err != nil {
			return err
		}
	}
	return nil
}

// ensureMigrationsTable creates the bookkeeping table, which is the one object
// Migrate owns rather than a migration.
func (d *DB) ensureMigrationsTable() error {
	if _, err := d.sql.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

// appliedMigrations reads the versions already recorded.
func (d *DB) appliedMigrations() (map[int]bool, error) {
	applied := map[int]bool{}
	rows, err := d.sql.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return applied, nil
}

// migrationFiles lists the embedded migrations in the order they must be applied.
func migrationFiles() ([]string, error) {
	names, err := migrations.FS.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	files := make([]string, 0, len(names))
	for _, entry := range names {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	return files, nil
}

// applyMigration runs one file and records its version in the same transaction: a
// migration is either applied and recorded, or not at all.
func (d *DB) applyMigration(name string, version int) error {
	body, err := migrations.FS.ReadFile(name)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(string(body)); err != nil {
		tx.Rollback()
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))`,
		version,
	); err != nil {
		tx.Rollback()
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

func migrationVersion(name string) (int, error) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	head := base
	if index := strings.IndexAny(base, "_-"); index > 0 {
		head = base[:index]
	}
	version, err := strconv.Atoi(head)
	if err != nil {
		return 0, fmt.Errorf("migration %s: file name must start with a number", name)
	}
	return version, nil
}
