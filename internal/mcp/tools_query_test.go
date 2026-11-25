package mcp

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// fakePairCache is a minimal pairingCache for exercising pairing_status
// without standing up the daemon's real PairCache.
type fakePairCache struct {
	paired  bool
	qr      string
	expired bool
}

func (f fakePairCache) Paired() bool  { return f.paired }
func (f fakePairCache) QR() string    { return f.qr }
func (f fakePairCache) Expired() bool { return f.expired }

// callPairingStatus invokes the pairing_status handler and decodes its JSON
// "setup_state" envelope into a map.
func callPairingStatus(t *testing.T, s *Server) map[string]any {
	t.Helper()
	tool, ok := s.MCP().ListTools()["pairing_status"]
	if !ok || tool == nil || tool.Handler == nil {
		t.Fatal("pairing_status not registered")
	}
	res, err := tool.Handler(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("pairing_status handler: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("pairing_status returned no content")
	}
	text, ok := mcp.AsTextContent(res.Content[0])
	if !ok {
		t.Fatalf("pairing_status content not text: %T", res.Content[0])
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(text.Text), &env); err != nil {
		t.Fatalf("decode envelope %q: %v", text.Text, err)
	}
	if env["type"] != "setup_state" {
		t.Errorf("type = %v, want setup_state", env["type"])
	}
	return env
}

// A nil pairing cache (smoke/tests) yields the error envelope without touching
// the WhatsApp client — so passing a nil client here must not panic.
func TestPairingStatus_NilCache(t *testing.T) {
	env := callPairingStatus(t, NewServer(nil, nil, false))
	if env["state"] != "error" {
		t.Errorf("state = %v, want error", env["state"])
	}
	if env["detail"] != "pairing cache unavailable" {
		t.Errorf("detail = %v, want pairing cache unavailable", env["detail"])
	}
}

// An unpaired cache with a pending QR yields awaiting_qr carrying the payload.
func TestPairingStatus_AwaitingQRWithPayload(t *testing.T) {
	env := callPairingStatus(t, NewServer(nil, fakePairCache{qr: "2@QRPAYLOAD"}, false))
	if env["state"] != "awaiting_qr" {
		t.Fatalf("state = %v, want awaiting_qr", env["state"])
	}
	if env["qr_payload"] != "2@QRPAYLOAD" {
		t.Errorf("qr_payload = %v, want 2@QRPAYLOAD", env["qr_payload"])
	}
	if env["detail"] != "Scan with WhatsApp → Linked devices" {
		t.Errorf("detail = %v, want the scan instruction", env["detail"])
	}
}

// An unpaired cache with no QR yet yields the generating-code envelope and
// omits qr_payload.
func TestPairingStatus_AwaitingQRGenerating(t *testing.T) {
	env := callPairingStatus(t, NewServer(nil, fakePairCache{}, false))
	if env["state"] != "awaiting_qr" {
		t.Fatalf("state = %v, want awaiting_qr", env["state"])
	}
	if _, ok := env["qr_payload"]; ok {
		t.Errorf("qr_payload should be absent, got %v", env["qr_payload"])
	}
	if env["detail"] != "Generating pairing code…" {
		t.Errorf("detail = %v, want generating message", env["detail"])
	}
}

// An unpaired cache whose pairing session expired keeps the awaiting_qr state
// (supervisors switch on it) and flags expired so the caller knows the QR
// will not appear until a new session starts.
func TestPairingStatus_AwaitingQRExpired(t *testing.T) {
	env := callPairingStatus(t, NewServer(nil, fakePairCache{expired: true}, false))
	if env["state"] != "awaiting_qr" {
		t.Fatalf("state = %v, want awaiting_qr", env["state"])
	}
	if env["expired"] != true {
		t.Errorf("expired = %v, want true", env["expired"])
	}
	if _, ok := env["qr_payload"]; ok {
		t.Errorf("qr_payload should be absent, got %v", env["qr_payload"])
	}
	if detail, _ := env["detail"].(string); !strings.Contains(detail, "expired") {
		t.Errorf("detail = %v, want an expiry message", env["detail"])
	}
}

type listMessagesJSON struct {
	Messages []struct {
		ID         string `json:"id"`
		ChatJID    string `json:"chat_jid"`
		ChatName   string `json:"chat_name"`
		Content    string `json:"content"`
		SenderName string `json:"sender_name"`
	} `json:"messages"`
	NextCursor string `json:"next_cursor"`
}

func listMessagesTool(t *testing.T, s *Server, args map[string]any) listMessagesJSON {
	t.Helper()
	out, isErr := callTool(t, s, "list_messages", args)
	if isErr {
		t.Fatalf("list_messages(%v) error: %s", args, out)
	}
	var page listMessagesJSON
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return page
}

// format=json returns the same page as the text format, as message objects
// plus the cursor, and a cursor walk visits each message once.
func TestListMessagesTool_JSONFormat(t *testing.T) {
	s := newStoreBackedReadOnlyServer(t)
	alice := "447700000001@s.whatsapp.net"

	first := listMessagesTool(t, s, map[string]any{"chat_jid": alice, "format": "json", "include_context": false, "limit": 1})
	if len(first.Messages) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %+v, want one message and a cursor", first)
	}
	m := first.Messages[0]
	if m.ID != "m2" || m.ChatJID != alice || m.ChatName != "Alice" || m.SenderName != "Alice" || m.Content != "gym bag ready, see you at the gym" {
		t.Fatalf("first message = %+v, want m2 from Alice", m)
	}
	second := listMessagesTool(t, s, map[string]any{"chat_jid": alice, "format": "json", "include_context": false, "limit": 1, "cursor": first.NextCursor})
	if len(second.Messages) != 1 || second.Messages[0].ID != "m1" || second.NextCursor != "" {
		t.Fatalf("second page = %+v, want m1 and no cursor", second)
	}

	out, _ := callTool(t, s, "list_messages", map[string]any{"chat_jid": "nobody@s.whatsapp.net", "format": "json"})
	var empty map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &empty); err != nil || string(empty["messages"]) != "[]" || len(empty) != 1 {
		t.Fatalf("empty page = %s, want only an empty messages array", out)
	}
}

// With include_context, the JSON array holds the surrounding messages too,
// the same messages in the same order as the text format lists them.
func TestListMessagesTool_JSONFormatMatchesTextWithContext(t *testing.T) {
	s := newStoreBackedReadOnlyServer(t)
	args := map[string]any{"chat_jid": "447700000001@s.whatsapp.net", "query": "seven"}
	text, _ := callTool(t, s, "list_messages", args)
	var want []string
	for _, m := range regexp.MustCompile(`\[ID: ([^\]]+)\]`).FindAllStringSubmatch(text, -1) {
		want = append(want, m[1])
	}
	args["format"] = "json"
	var got []string
	for _, m := range listMessagesTool(t, s, args).Messages {
		got = append(got, m.ID)
	}
	if len(want) < 2 || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("JSON ids = %v, text ids = %v; want the same match plus context", got, want)
	}
}

func TestListMessagesTool_FormatDefaultsToText(t *testing.T) {
	s := newStoreBackedReadOnlyServer(t)
	for _, args := range []map[string]any{
		{"chat_jid": "447700000001@s.whatsapp.net", "include_context": false},
		{"chat_jid": "447700000001@s.whatsapp.net", "include_context": false, "format": "text"},
	} {
		out, isErr := callTool(t, s, "list_messages", args)
		if isErr || json.Valid([]byte(out)) || !strings.Contains(out, "[ID: m2]") {
			t.Fatalf("list_messages(%v) = %q, want the text format", args, out)
		}
	}
	if out, isErr := callTool(t, s, "list_messages", map[string]any{"format": "xml"}); !isErr {
		t.Fatalf("format xml = %q, want an error", out)
	}
}

// search_messages hits carry the sender's display name, as list_messages
// JSON and the text format do.
func TestSearchMessagesTool_HitsCarrySenderName(t *testing.T) {
	s := newStoreBackedReadOnlyServer(t)
	out, isErr := callTool(t, s, "search_messages", map[string]any{"query": "seven"})
	if isErr {
		t.Fatalf("search_messages error: %s", out)
	}
	var hits []struct {
		ID         string `json:"id"`
		SenderName string `json:"sender_name"`
	}
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if len(hits) != 1 || hits[0].ID != "m1" || hits[0].SenderName != "Alice" {
		t.Fatalf("hits = %+v, want m1 from Alice", hits)
	}
}

// Tools documented to return a JSON array return [] when nothing matches,
// not null, so a JSON client can decode the result as an array.
func TestListTools_NoMatchReturnsEmptyArray(t *testing.T) {
	s := newStoreBackedReadOnlyServer(t)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"list_chats", map[string]any{"query": "nobody"}},
		{"search_contacts", map[string]any{"query": "nobody"}},
		{"search_messages", map[string]any{"query": "nobody"}},
	} {
		out, isErr := callTool(t, s, tc.tool, tc.args)
		if isErr || strings.TrimSpace(out) != "[]" {
			t.Errorf("%s(%v) = %q, want []", tc.tool, tc.args, out)
		}
	}
}
