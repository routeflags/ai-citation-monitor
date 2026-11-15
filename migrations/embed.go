// Package migrations embeds the SQL migrations that this tool owns.
//
// Migrations are forward-only: a destructive change adds a new file instead of
// editing an applied one. The applied set is tracked in schema_migrations.
package migrations

import "embed"

// FS holds every .sql file in this directory, name-sorted by the loader.
//
//go:embed *.sql
var FS embed.FS
