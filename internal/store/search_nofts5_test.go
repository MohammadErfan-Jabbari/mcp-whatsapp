//go:build !sqlite_fts5

package store

import (
	"strings"
	"testing"
)

// Message search needs SQLite's FTS5 module, which go-sqlite3 compiles in
// only with the sqlite_fts5 build tag. A binary built without it must say
// how to fix the build instead of failing on an opaque SQLite error.
func TestOpen_WithoutFTS5NamesTheBuildTag(t *testing.T) {
	s, err := Open(t.TempDir())
	if err == nil {
		s.Close()
		t.Fatal("Open succeeded without FTS5")
	}
	if !strings.Contains(err.Error(), "-tags sqlite_fts5") {
		t.Fatalf("Open error = %q, want it to name -tags sqlite_fts5", err)
	}
}
