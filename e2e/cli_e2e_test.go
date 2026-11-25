//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/sealjay/mcp-whatsapp/internal/store"
)

// generatedCLIURL is the daemon URL scripts/gen-cli.sh embeds by default.
const generatedCLIURL = "http://127.0.0.1:8765/mcp"

// cliFor returns the path of a copy of the generated client (bin/whatsapp.mjs,
// built by make cli) that calls h's daemon. The client embeds the URL it was
// generated against, so the copy swaps that URL for h's free port.
func cliFor(t *testing.T, h *harness) string {
	t.Helper()
	bundle := filepath.Join(repoRoot(t), "bin", "whatsapp.mjs")
	src, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatalf("read the generated client (run make cli): %v", err)
	}
	if !bytes.Contains(src, []byte(generatedCLIURL)) {
		t.Fatalf("%s does not embed %s", bundle, generatedCLIURL)
	}
	path := filepath.Join(t.TempDir(), "whatsapp.mjs")
	src = bytes.ReplaceAll(src, []byte(generatedCLIURL), []byte("http://"+h.addr+"/mcp"))
	if err := os.WriteFile(path, src, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// runCLI runs the client with -o json and returns its stdout and exit code.
func runCLI(t *testing.T, cli string, args ...string) ([]byte, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", append(append([]string{cli}, args...), "-o", "json")...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return out, 0
	case errors.As(err, &exitErr):
		return out, exitErr.ExitCode()
	default:
		t.Fatalf("run %v: %v (stderr: %s)", args, err, stderr.String())
		return nil, 0
	}
}

// cliJSON runs the client, expects success, and decodes its stdout into v.
func cliJSON(t *testing.T, cli string, v any, args ...string) {
	t.Helper()
	out, code := runCLI(t, cli, args...)
	if code != 0 {
		t.Fatalf("%v exited %d: %s", args, code, out)
	}
	if err := json.Unmarshal(out, v); err != nil {
		t.Fatalf("%v printed %q, not the expected JSON: %v", args, out, err)
	}
}

type cliMessage struct {
	ID      string `json:"id"`
	ChatJID string `json:"chat_jid"`
	Content string `json:"content"`
}

func ids(ms []cliMessage) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	slices.Sort(out)
	return out
}

func TestCLI_EmptyArchiveReturnsJSON(t *testing.T) {
	h := newHarness(t, "-read-only")
	cli := cliFor(t, h)

	var chats []json.RawMessage
	cliJSON(t, cli, &chats, "list-chats")
	if chats == nil || len(chats) != 0 {
		t.Fatalf("list-chats = %v, want []", chats)
	}

	var list struct{ Messages []cliMessage }
	cliJSON(t, cli, &list, "list-messages", "--format", "json")
	if list.Messages == nil || len(list.Messages) != 0 {
		t.Fatalf("list-messages = %+v, want an empty messages array", list)
	}

	var hits []cliMessage
	cliJSON(t, cli, &hits, "search-messages", "--query", "gym")
	if hits == nil || len(hits) != 0 {
		t.Fatalf("search-messages = %+v, want []", hits)
	}

	// A failed call exits 1 and still prints JSON: the MCP result with isError.
	out, code := runCLI(t, cli, "get-message-context", "--message-id", "missing")
	var failed struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(out, &failed); code != 1 || err != nil || !failed.IsError {
		t.Fatalf("get-message-context on a missing message: exit %d, output %q", code, out)
	}
}

func TestCLI_ArchiveReadsReturnJSON(t *testing.T) {
	const alice, gym = "447700900001@s.whatsapp.net", "120363000000000001@g.us"
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)
	for jid, name := range map[string]string{alice: "Alice", gym: "Gym group"} {
		if err := st.StoreChat(jid, name, base); err != nil {
			t.Fatal(err)
		}
	}
	for i, m := range []store.Message{
		{ID: "m1", ChatJID: alice, Sender: "447700900001", Content: "see you at the gym at seven"},
		{ID: "m2", ChatJID: alice, Sender: "447700900099", IsFromMe: true, Content: "bring the blue towel"},
		{ID: "m3", ChatJID: alice, Sender: "447700900001", Content: "the gym closes early on Sunday"},
		{ID: "m4", ChatJID: gym, Sender: "447700900002", IsGroup: true, Content: "leg day moved to Friday"},
	} {
		m.Timestamp = base.Add(time.Duration(i) * time.Minute)
		if err := st.StoreMessage(context.Background(), m, nil, nil, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	h := newHarnessWithStore(t, dir, "-read-only")
	cli := cliFor(t, h)

	var chats []struct {
		JID  string `json:"jid"`
		Name string `json:"name"`
	}
	cliJSON(t, cli, &chats, "list-chats")
	if len(chats) != 2 {
		t.Fatalf("list-chats = %+v, want Alice and Gym group", chats)
	}

	var list struct{ Messages []cliMessage }
	cliJSON(t, cli, &list, "list-messages", "--format", "json", "--chat-jid", alice, "--include-context", "false")
	if got := ids(list.Messages); !slices.Equal(got, []string{"m1", "m2", "m3"}) {
		t.Fatalf("list-messages in Alice's chat = %v, want m1 m2 m3", got)
	}

	var hits []cliMessage
	cliJSON(t, cli, &hits, "search-messages", "--query", "gym")
	if got := ids(hits); !slices.Equal(got, []string{"m1", "m3"}) {
		t.Fatalf("search-messages gym = %v, want m1 m3", got)
	}

	var ctx struct {
		Message cliMessage
		Before  []cliMessage
		After   []cliMessage
	}
	cliJSON(t, cli, &ctx, "get-message-context", "--message-id", "m2", "--before", "1", "--after", "1")
	if ctx.Message.Content != "bring the blue towel" || !slices.Equal(ids(ctx.Before), []string{"m1"}) || !slices.Equal(ids(ctx.After), []string{"m3"}) {
		t.Fatalf("get-message-context m2 = %+v, want m1 before and m3 after", ctx)
	}
}
