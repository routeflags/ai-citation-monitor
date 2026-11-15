package cli

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite" // same driver as the tool
)

// sqlHandle runs one statement against the test database.
type sqlHandle struct {
	t     *testing.T
	db    *sql.DB
	query string
}

func openForTest(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
}

func (h *sqlHandle) int64() int64 {
	h.t.Helper()
	var value int64
	if err := h.db.QueryRow(h.query).Scan(&value); err != nil {
		h.t.Fatalf("%s: %v", h.query, err)
	}
	return value
}

func (h *sqlHandle) exec() {
	h.t.Helper()
	if _, err := h.db.Exec(h.query); err != nil {
		h.t.Fatalf("%s: %v", h.query, err)
	}
}

func (h *sqlHandle) close() { h.db.Close() }
