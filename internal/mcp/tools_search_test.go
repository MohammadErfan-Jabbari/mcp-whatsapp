package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sealjay/mcp-whatsapp/internal/client"
	"github.com/sealjay/mcp-whatsapp/internal/store"
)

// newStoreBackedReadOnlyServer serves a real on-disk store holding two chats
// and four messages through the read-only MCP server.
func newStoreBackedReadOnlyServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for jid, name := range map[string]string{"447700000001@s.whatsapp.net": "Alice", "123456789@g.us": "Team"} {
		if err := st.StoreChat(jid, name, base); err != nil {
			t.Fatal(err)
		}
	}
	for i, m := range []store.Message{
		{ID: "m1", ChatJID: "447700000001@s.whatsapp.net", Sender: "447700000001", Content: "the gym opens at seven"},
		{ID: "m2", ChatJID: "447700000001@s.whatsapp.net", Sender: "447700000001", Content: "gym bag ready, see you at the gym"},
		{ID: "m3", ChatJID: "123456789@g.us", Sender: "447700000002", Content: "gym schedule for the whole team this week"},
		{ID: "m4", ChatJID: "123456789@g.us", Sender: "447700000002", Content: "lunch at noon"},
	} {
		m.Timestamp = base.Add(time.Duration(i) * time.Hour)
		if err := st.StoreMessage(ctx, m, nil, nil, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	c, err := client.New(ctx, client.Config{
		StoreDir: dir,
		Store:    st,
		Logger:   client.NewStderrLogger("test", "ERROR", false),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Disconnect)
	return NewReadOnlyServer(c, nil, false)
}

// callTool invokes a registered tool's handler and returns its first text
// block and whether the result is an error.
func callTool(t *testing.T, s *Server, name string, args map[string]any) (string, bool) {
	t.Helper()
	tool, ok := s.MCP().ListTools()[name]
	if !ok || tool == nil || tool.Handler == nil {
		t.Fatalf("%s not registered", name)
	}
	res, err := tool.Handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name, Arguments: args}})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return firstText(t, res), res.IsError
}

type searchHitJSON struct {
	ID       string  `json:"id"`
	ChatJID  string  `json:"chat_jid"`
	ChatName string  `json:"chat_name"`
	Content  string  `json:"content"`
	Snippet  string  `json:"snippet"`
	Rank     float64 `json:"rank"`
}

func searchTool(t *testing.T, s *Server, args map[string]any) []searchHitJSON {
	t.Helper()
	out, isErr := callTool(t, s, "search_messages", args)
	if isErr {
		t.Fatalf("search_messages(%v) error: %s", args, out)
	}
	var hits []searchHitJSON
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return hits
}

func TestSearchMessagesTool_ReturnsRankedJSON(t *testing.T) {
	s := newStoreBackedReadOnlyServer(t)

	hits := searchTool(t, s, map[string]any{"query": "gym"})
	if len(hits) != 3 || hits[0].ID != "m2" {
		t.Fatalf("hits = %+v, want 3 with m2 (gym twice) first", hits)
	}
	h := hits[0]
	if h.ChatJID != "447700000001@s.whatsapp.net" || h.ChatName != "Alice" || h.Content == "" || h.Snippet == "" || h.Rank == 0 {
		t.Fatalf("hit missing fields: %+v", h)
	}

	if hits := searchTool(t, s, map[string]any{"query": "gym", "chat_jid": "123456789@g.us"}); len(hits) != 1 || hits[0].ID != "m3" {
		t.Fatalf("chat filter = %+v, want only m3", hits)
	}
	if hits := searchTool(t, s, map[string]any{"query": "gym", "before": "2026-01-01T09:30:00Z"}); len(hits) != 1 || hits[0].ID != "m1" {
		t.Fatalf("before filter = %+v, want only m1", hits)
	}
	if hits := searchTool(t, s, map[string]any{"query": "gym", "limit": 1, "page": 1}); len(hits) != 1 || hits[0].ID == "m2" {
		t.Fatalf("page 1 of limit 1 = %+v, want one hit other than m2", hits)
	}
	if out, _ := callTool(t, s, "search_messages", map[string]any{"query": "walrus"}); out != "[]" {
		t.Fatalf("no hits = %q, want []", out)
	}
	if hits := searchTool(t, s, map[string]any{"query": `gym" OR "lunch`}); len(hits) != 0 {
		t.Fatalf("hostile query = %+v, want no hits", hits)
	}
}

func TestSearchMessagesTool_RejectsBadArguments(t *testing.T) {
	s := newStoreBackedReadOnlyServer(t)
	for _, args := range []map[string]any{
		{},
		{"query": "  "},
		{"query": "gym", "after": "yesterday"},
		{"query": "gym", "before": "2026-13-01"},
	} {
		if out, isErr := callTool(t, s, "search_messages", args); !isErr {
			t.Errorf("search_messages(%v) = %s, want an error", args, out)
		}
	}
}
