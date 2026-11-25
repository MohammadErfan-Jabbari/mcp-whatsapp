package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sealjay/mcp-whatsapp/internal/store"
)

// -- search_messages --------------------------------------------------------

type searchMessagesArgs struct {
	Query             string `json:"query"`
	ChatJID           string `json:"chat_jid,omitempty"`
	SenderPhoneNumber string `json:"sender_phone_number,omitempty"`
	After             string `json:"after,omitempty"`
	Before            string `json:"before,omitempty"`
	Limit             int    `json:"limit,omitempty"`
	Page              int    `json:"page,omitempty"`
}

func (s *Server) registerSearchMessages() {
	tool := mcp.NewTool("search_messages",
		mcp.WithDescription(offlineSafePrefix+"Full-text search of cached WhatsApp messages by the words in their text or attachment file name, best match first, optionally filtered by chat, sender and time range. Read-only; no side effects. Every query word must appear as a whole word, ignoring case and accents, and search operators or quotes have no special meaning; use list_messages for substring matches or to read a chat in time order. Returns a JSON array of message objects, each with a `snippet` that marks the matched words in [brackets] and a bm25 `rank` (lower is better)."),
		mcp.WithString("query", mcp.Required(), mcp.Description("words to find, in any order (edited messages match their latest text; messages deleted for everyone still match)")),
		mcp.WithString("chat_jid", mcp.Description("filter to messages in this chat ("+jidDesc+")")),
		mcp.WithString("sender_phone_number", mcp.Description("filter to messages sent by this phone or JID ("+jidDesc+")")),
		mcp.WithString("after", mcp.Description("ISO-8601 UTC lower bound on message timestamp (exclusive)")),
		mcp.WithString("before", mcp.Description("ISO-8601 UTC upper bound on message timestamp (exclusive)")),
		mcp.WithNumber("limit", mcp.DefaultNumber(20), mcp.Description("max messages to return (default 20, capped at 100 server-side)")),
		mcp.WithNumber("page", mcp.DefaultNumber(0), mcp.Description("zero-based page index into the ranked results (default 0)")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(false),
	)
	s.mcp.AddTool(tool, mcp.NewTypedToolHandler(func(ctx context.Context, _ mcp.CallToolRequest, a searchMessagesArgs) (*mcp.CallToolResult, error) {
		params := store.SearchParams{
			Query:       a.Query,
			ChatJID:     a.ChatJID,
			SenderPhone: a.SenderPhoneNumber,
			Limit:       min(a.Limit, 100),
			Page:        a.Page,
		}
		if a.After != "" {
			t, err := parseTimestamp(a.After)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid 'after': %v", err)), nil
			}
			params.After = t
		}
		if a.Before != "" {
			t, err := parseTimestamp(a.Before)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid 'before': %v", err)), nil
			}
			params.Before = t
		}
		return toolResult(s.client.Store().SearchMessages(ctx, params))
	}))
}
