//go:build unix

package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

var messageDBFiles = []string{"messages.db", "messages.db-wal", "messages.db-shm"}

// The -wal and -shm files hold recent message bodies, so Open keeps them 0600
// like messages.db under a permissive umask: on a fresh store, and on one an
// older build left with 0644 files after a crash.
func TestOpen_DatabaseSidecarsArePrivate(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	t.Run("fresh", func(t *testing.T) {
		dir := t.TempDir()
		assertPrivateAfterWrite(t, dir)
	})
	t.Run("left by a crash", func(t *testing.T) {
		dir := t.TempDir()
		copyOpenStore(t, t.TempDir(), dir)
		assertPrivateAfterWrite(t, dir)
		s, err := Open(dir)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer s.Close()
		var chats int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM chats WHERE jid = ?`, "447700000000@s.whatsapp.net").Scan(&chats); err != nil || chats != 1 {
			t.Fatalf("chat written before the crash: count %d, err %v", chats, err)
		}
	})
}

// copyOpenStore writes a chat to a store in src and copies its database files
// to dst as 0644 while the store is still open, the way a crash leaves them
// with unflushed frames in -wal.
func copyOpenStore(t *testing.T, src, dst string) {
	t.Helper()
	s, err := Open(src)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := s.StoreChat("447700000000@s.whatsapp.net", "Before crash", time.Now()); err != nil {
		t.Fatalf("StoreChat: %v", err)
	}
	for _, f := range messageDBFiles {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil || len(b) == 0 {
			t.Fatalf("read %s: %d bytes, err %v", f, len(b), err)
		}
		if err := os.WriteFile(filepath.Join(dst, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func assertPrivateAfterWrite(t *testing.T, dir string) {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := s.StoreChat("447700000001@s.whatsapp.net", "Alice", time.Now()); err != nil {
		t.Fatalf("StoreChat: %v", err)
	}
	for _, f := range messageDBFiles {
		info, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("stat %s: %v", f, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 600", f, got)
		}
	}
}

func TestEnsurePrivateDB(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	db := filepath.Join(dir, "whatsapp.db")
	if err := os.WriteFile(db+"-journal", []byte("left by an older build"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDB(db); err != nil {
		t.Fatalf("EnsurePrivateDB: %v", err)
	}
	for _, f := range []string{db, db + "-journal"} {
		info, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(f), got)
		}
	}
	if b, err := os.ReadFile(db + "-journal"); err != nil || string(b) != "left by an older build" {
		t.Fatalf("existing journal changed: %q, %v", b, err)
	}
}
