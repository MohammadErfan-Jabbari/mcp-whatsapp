package store

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// searchIDs runs a search and returns the hit IDs in result order.
func searchIDs(t *testing.T, s *Store, p SearchParams) []string {
	t.Helper()
	hits, err := s.SearchMessages(context.Background(), p)
	if err != nil {
		t.Fatalf("SearchMessages(%+v): %v", p, err)
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	return ids
}

// sortedIDs returns the hit IDs sorted, for assertions that ignore rank.
func sortedIDs(t *testing.T, s *Store, p SearchParams) string {
	t.Helper()
	ids := searchIDs(t, s, p)
	slices.Sort(ids)
	return strings.Join(ids, ",")
}

func TestSearchMessages_MatchesWordsInTextAndFileNames(t *testing.T) {
	s := openTestStore(t)
	if got := sortedIDs(t, s, SearchParams{Query: "bob"}); got != "b1,b2" {
		t.Fatalf("search bob = %s, want b1,b2", got)
	}
	// a4 says "see attached" and carries pic.jpg; a5 says "nice pic".
	if got := sortedIDs(t, s, SearchParams{Query: "pic"}); got != "a4,a5" {
		t.Fatalf("search pic = %s, want a4,a5", got)
	}
	// Every word must appear, in any case and order.
	if got := sortedIDs(t, s, SearchParams{Query: "ALICE hello"}); got != "a1" {
		t.Fatalf("search ALICE hello = %s, want a1", got)
	}
	if got := searchIDs(t, s, SearchParams{Query: "walrus"}); len(got) != 0 {
		t.Fatalf("search walrus = %v, want no hits", got)
	}
}

func TestSearchMessages_RanksBestMatchFirst(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	chat := "447700000001@s.whatsapp.net"
	for _, m := range []Message{
		{ID: "r1", Content: "the budget is somewhere in this long message about many other things entirely"},
		{ID: "r2", Content: "budget budget budget"},
	} {
		m.ChatJID, m.Sender, m.Timestamp = chat, "447700000001", time.Now()
		if err := s.StoreMessage(ctx, m, nil, nil, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := s.SearchMessages(ctx, SearchParams{Query: "budget"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "r2" || hits[1].ID != "r1" {
		t.Fatalf("hits = %+v, want r2 then r1", hits)
	}
	if hits[0].Rank >= hits[1].Rank {
		t.Fatalf("rank r2 = %v, r1 = %v, want r2 lower (better)", hits[0].Rank, hits[1].Rank)
	}
	if !strings.Contains(hits[1].Snippet, "[budget]") {
		t.Fatalf("snippet %q does not mark the hit", hits[1].Snippet)
	}
	if hits[0].ChatName != "Alice" || hits[0].SenderPhone != "447700000001" || hits[0].Content != "budget budget budget" {
		t.Fatalf("hit lost message fields: %+v", hits[0])
	}
}

func TestSearchMessages_Filters(t *testing.T) {
	s := openTestStore(t)
	jan3 := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		p    SearchParams
		want string
	}{
		{"chat", SearchParams{Query: "bob", ChatJID: "447700000002@s.whatsapp.net"}, "b1,b2"},
		{"other chat", SearchParams{Query: "bob", ChatJID: "123456789@g.us"}, ""},
		{"sender", SearchParams{Query: "bob", SenderPhone: "me"}, "b2"},
		{"after", SearchParams{Query: "bob", After: jan3}, "b2"},
		{"before", SearchParams{Query: "bob", Before: jan3}, "b1"},
		{"page past the end", SearchParams{Query: "bob", Limit: 1, Page: 2}, ""},
		{"page that overflows", SearchParams{Query: "bob", Limit: 2, Page: math.MaxInt}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sortedIDs(t, s, tc.p); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	if got := searchIDs(t, s, SearchParams{Query: "bob", Limit: 1}); len(got) != 1 {
		t.Fatalf("limit 1 = %v, want one hit", got)
	}
}

// Pages walk the ranked list without repeating or skipping a hit.
func TestSearchMessages_PagesCoverEveryHitOnce(t *testing.T) {
	s := openTestStore(t)
	all := searchIDs(t, s, SearchParams{Query: "bob"})
	var paged []string
	for page := 0; page < 3; page++ {
		paged = append(paged, searchIDs(t, s, SearchParams{Query: "bob", Limit: 1, Page: page})...)
	}
	if strings.Join(paged, ",") != strings.Join(all, ",") {
		t.Fatalf("pages = %v, want %v", paged, all)
	}
}

func TestSearchMessages_FollowsNewAndEditedMessages(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	chat := "447700000001@s.whatsapp.net"
	if err := s.StoreMessage(ctx, Message{ID: "n1", ChatJID: chat, Sender: "447700000001", Content: "a walrus walked in", Timestamp: time.Now()}, nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if got := sortedIDs(t, s, SearchParams{Query: "walrus"}); got != "n1" {
		t.Fatalf("search walrus after insert = %q, want n1", got)
	}
	applied, err := s.ApplyEdit(ctx, chat, "a1", Author{Senders: []string{"447700000001@s.whatsapp.net"}}, "goodbye narwhal", time.Now())
	if err != nil || !applied {
		t.Fatalf("ApplyEdit = %v, %v", applied, err)
	}
	if got := sortedIDs(t, s, SearchParams{Query: "narwhal"}); got != "a1" {
		t.Fatalf("search narwhal after edit = %q, want a1", got)
	}
	if got := searchIDs(t, s, SearchParams{Query: "hello alice"}); len(got) != 0 {
		t.Fatalf("pre-edit text still matches: %v", got)
	}
	if _, err := s.DB().ExecContext(ctx, "DELETE FROM messages WHERE id = 'n1'"); err != nil {
		t.Fatal(err)
	}
	if got := searchIDs(t, s, SearchParams{Query: "walrus"}); len(got) != 0 {
		t.Fatalf("deleted message still matches: %v", got)
	}
}

// User text is matched as plain words: FTS5 operators, column filters and
// stray quotes neither raise a syntax error nor change what matches.
func TestSearchMessages_QuerySyntaxIsInert(t *testing.T) {
	s := openTestStore(t)
	for _, q := range []string{
		`OR hello`, `hello OR`, `"unbalanced`, `hello"alice`, `NEAR(hello alice)`,
		`content:hello`, `{content}: hello`, `hello*`, `*`, `-hello`, `(hello`, `AND`,
		`^hello`, `hello + alice`, `!!!`, `'; DROP TABLE messages; --`,
	} {
		if _, err := s.SearchMessages(context.Background(), SearchParams{Query: q}); err != nil {
			t.Errorf("query %q: %v", q, err)
		}
	}
	if got := searchIDs(t, s, SearchParams{Query: "hello OR bob"}); len(got) != 0 {
		t.Errorf("OR acted as an operator: %v", got)
	}
	if got := searchIDs(t, s, SearchParams{Query: "content:hello"}); len(got) != 0 {
		t.Errorf("column filter acted as an operator: %v", got)
	}
	if got := sortedIDs(t, s, SearchParams{Query: `"hello" "alice"`}); got != "a1" {
		t.Errorf("quoted words = %q, want a1", got)
	}
	for _, q := range []string{"", "  \t"} {
		if _, err := s.SearchMessages(context.Background(), SearchParams{Query: q}); err == nil {
			t.Errorf("blank query %q accepted", q)
		}
	}
}

// A store created before search existed has messages but no index. Open
// builds the index from them once, and keeps it current from then on.
func TestOpen_IndexesMessagesStoredBeforeSearchExisted(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	chat := "447700000001@s.whatsapp.net"

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StoreChat(chat, "Alice", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreMessage(ctx, Message{ID: "old", ChatJID: chat, Sender: "447700000001", Content: "dinner on friday", Timestamp: time.Now()}, nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Turn the file back into what an older build left behind.
	raw, err := sql.Open("sqlite3", "file:"+filepath.Join(dir, "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	var objects []string
	rows, err := raw.Query("SELECT type || ' ' || name FROM sqlite_master WHERE name LIKE 'messages_fts%' AND type IN ('table', 'trigger') AND sql IS NOT NULL")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			t.Fatal(err)
		}
		objects = append(objects, o)
	}
	rows.Close()
	for _, o := range objects {
		kind, name, _ := strings.Cut(o, " ")
		if kind == "table" && name != "messages_fts" {
			continue // FTS5 shadow tables go with their virtual table
		}
		if _, err := raw.Exec("DROP " + strings.ToUpper(kind) + " " + name); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	if len(objects) == 0 {
		t.Fatal("no search objects found to drop")
	}

	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := searchIDs(t, s, SearchParams{Query: "friday"}); len(got) != 1 || got[0] != "old" {
		t.Fatalf("search friday after upgrade = %v, want old", got)
	}
	if err := s.StoreMessage(ctx, Message{ID: "new", ChatJID: chat, Sender: "447700000001", Content: "lunch on friday", Timestamp: time.Now()}, nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if got := sortedIDs(t, s, SearchParams{Query: "friday"}); got != "new,old" {
		t.Fatalf("search friday after a new message = %q, want new,old", got)
	}
}
