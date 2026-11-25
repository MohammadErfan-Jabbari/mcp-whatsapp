//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Files dropped into the media root are about to be sent from a private
// account, so serve creates it 0700 whatever the umask.
func TestResolveMediaRoot_CreatesPrivateDirectory(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	store := t.TempDir()
	custom := filepath.Join(t.TempDir(), "outbox", "files")
	for _, tc := range []struct{ env, want string }{
		{env: "", want: filepath.Join(store, "uploads")},
		{env: custom, want: custom},
	} {
		t.Setenv("WHATSAPP_MCP_MEDIA_ROOT", tc.env)
		got := resolveMediaRoot(store)
		if got != tc.want {
			t.Fatalf("resolveMediaRoot = %q, want %q", got, tc.want)
		}
		info, err := os.Stat(got)
		if err != nil {
			t.Fatalf("stat media root: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("%s mode = %o, want 700", got, perm)
		}
	}
}
