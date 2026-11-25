package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SearchParams holds the filters accepted by SearchMessages.
type SearchParams struct {
	Query       string
	ChatJID     string
	SenderPhone string
	After       time.Time
	Before      time.Time
	Limit       int
	Page        int
}

// SearchHit is one message matched by SearchMessages.
type SearchHit struct {
	Message
	Snippet string  `json:"snippet"`
	Rank    float64 `json:"rank"`
}

func ensureSearchIndex(db *sql.DB) error { return nil }

// SearchMessages is not implemented yet.
func (s *Store) SearchMessages(ctx context.Context, p SearchParams) ([]SearchHit, error) {
	return nil, errors.New("not implemented")
}
