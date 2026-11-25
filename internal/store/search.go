package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// searchSchema indexes message text and attachment file names with SQLite
// FTS5. The index is external-content: it holds only the full-text index and
// reads text back from messages by rowid, and the triggers keep it in step
// with every insert, update and delete. messages has no INTEGER PRIMARY KEY,
// so a VACUUM may renumber its rowids; nothing here runs one, and after a
// manual VACUUM `INSERT INTO messages_fts(messages_fts) VALUES('rebuild')`
// realigns the index.
const searchSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
	content, filename,
	content='messages', content_rowid='rowid',
	tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER IF NOT EXISTS messages_fts_ai AFTER INSERT ON messages BEGIN
	INSERT INTO messages_fts(rowid, content, filename) VALUES (new.rowid, new.content, new.filename);
END;

CREATE TRIGGER IF NOT EXISTS messages_fts_ad AFTER DELETE ON messages BEGIN
	INSERT INTO messages_fts(messages_fts, rowid, content, filename) VALUES ('delete', old.rowid, old.content, old.filename);
END;

CREATE TRIGGER IF NOT EXISTS messages_fts_au AFTER UPDATE OF content, filename ON messages BEGIN
	INSERT INTO messages_fts(messages_fts, rowid, content, filename) VALUES ('delete', old.rowid, old.content, old.filename);
	INSERT INTO messages_fts(rowid, content, filename) VALUES (new.rowid, new.content, new.filename);
END;
`

// errNoFTS5 explains the failure of a binary built without SQLite FTS5,
// which go-sqlite3 compiles in only under the sqlite_fts5 build tag.
var errNoFTS5 = errors.New("this binary was built without SQLite FTS5, which message search needs; build it with -tags sqlite_fts5 (make build does)")

// ensureSearchIndex creates the search index and its triggers if they are
// missing. When it creates the index it also fills it from the messages
// already stored, in the same transaction, so a store from before search
// existed is searchable from its first open and a crash cannot leave it
// half-built.
func ensureSearchIndex(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existing int
	if err := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'messages_fts'").Scan(&existing); err != nil {
		return err
	}
	if _, err := tx.Exec(searchSchema); err != nil {
		if strings.Contains(err.Error(), "no such module: fts5") {
			return errNoFTS5
		}
		return err
	}
	if existing == 0 {
		if _, err := tx.Exec("INSERT INTO messages_fts(messages_fts) VALUES ('rebuild')"); err != nil {
			return fmt.Errorf("index existing messages: %w", err)
		}
	}
	return tx.Commit()
}

// SearchParams holds the filters accepted by SearchMessages. Zero values mean
// "unset" except for Limit, which defaults to 20 when <= 0. Page is a
// zero-based offset in pages of Limit hits.
type SearchParams struct {
	Query       string
	ChatJID     string
	SenderPhone string
	After       time.Time
	Before      time.Time
	Limit       int
	Page        int
}

// SearchHit is a message matched by SearchMessages. Snippet is the matching
// passage with each hit word in [brackets]; Rank is the bm25 score, lower for
// a better match.
type SearchHit struct {
	Message
	Snippet string  `json:"snippet"`
	Rank    float64 `json:"rank"`
}

// SearchMessages returns the messages whose text or attachment file name
// contains every word of p.Query, best match first. Words match whole tokens,
// ignoring case and accents; FTS5 query syntax in p.Query has no effect.
// Ties in rank fall back to newest first.
func (s *Store) SearchMessages(ctx context.Context, p SearchParams) ([]SearchHit, error) {
	match := ftsQuery(p.Query)
	if match == "" {
		return nil, errors.New("query: required")
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}
	page := max(p.Page, 0)
	hits := []SearchHit{}
	if page > math.MaxInt/limit {
		return hits, nil
	}

	where := []string{"messages_fts MATCH ?"}
	args := []any{match}
	if p.ChatJID != "" {
		where = append(where, "messages.chat_jid = ?")
		args = append(args, p.ChatJID)
	}
	if p.SenderPhone != "" {
		where = append(where, "messages.sender = ?")
		args = append(args, p.SenderPhone)
	}
	if !p.After.IsZero() {
		where = append(where, listMessageTimeKey+" > ?")
		args = append(args, utcTimestampText(p.After)[:29])
	}
	if !p.Before.IsZero() {
		where = append(where, listMessageTimeKey+" < ?")
		args = append(args, utcTimestampText(p.Before)[:29])
	}
	args = append(args, limit, page*limit)

	rows, err := s.db.QueryContext(ctx,
		"SELECT "+listMessageColumns+", snippet(messages_fts, -1, '[', ']', '…', 12), messages_fts.rank"+
			" FROM messages_fts"+
			" JOIN messages ON messages.rowid = messages_fts.rowid"+
			" JOIN chats ON messages.chat_jid = chats.jid"+
			" WHERE "+strings.Join(where, " AND ")+
			" ORDER BY messages_fts.rank, "+listMessageTimeKey+" DESC, messages.id DESC, messages.chat_jid DESC"+
			" LIMIT ? OFFSET ?",
		args...)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			snippet sql.NullString
			rank    float64
		)
		m, err := s.scanListMessageRow(withExtraColumns{rows, []any{&snippet, &rank}})
		if err != nil {
			return nil, err
		}
		hits = append(hits, SearchHit{Message: m, Snippet: snippet.String, Rank: rank})
	}
	return hits, rows.Err()
}

// withExtraColumns lets scanListMessageRow read a row that carries more
// columns after listMessageColumns, scanning those into extra.
type withExtraColumns struct {
	row   *sql.Rows
	extra []any
}

func (w withExtraColumns) Scan(dest ...any) error {
	return w.row.Scan(append(dest, w.extra...)...)
}

// ftsQuery turns free text into an FTS5 query that matches every word
// literally: each whitespace-separated word becomes an FTS5 string, with any
// double quote doubled, so operators such as OR, NEAR, *, ^ and column
// filters are plain text. A NUL byte ends an FTS5 string early, so it
// separates words like a space.
func ftsQuery(q string) string {
	words := strings.Fields(strings.ReplaceAll(q, "\x00", " "))
	for i, w := range words {
		words[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	return strings.Join(words, " ")
}
