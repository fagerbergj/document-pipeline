package postgres

import (
	"fmt"
	"strings"

	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/model"
)

// sortConfig describes how to ORDER and continue a keyset cursor for one sort mode.
type sortConfig struct {
	order string // ORDER BY fragment
	// cursorWhere returns the keyset WHERE fragment using two positional
	// placeholders starting at $start (consumes $start and $start+1).
	cursorWhere func(start int) string
}

func gtCursor(prefix string) func(int) string {
	return func(start int) string {
		return fmt.Sprintf("(%s.created_at, %s.id) > ($%d, $%d)", prefix, prefix, start, start+1)
	}
}

func ltCursor(prefix string) func(int) string {
	return func(start int) string {
		return fmt.Sprintf("(%s.created_at, %s.id) < ($%d, $%d)", prefix, prefix, start, start+1)
	}
}

func gtTitleCursor(start int) string {
	return fmt.Sprintf("(LOWER(COALESCE(d.title,'')), d.id) > ($%d, $%d)", start, start+1)
}

func ltTitleCursor(start int) string {
	return fmt.Sprintf("(LOWER(COALESCE(d.title,'')), d.id) < ($%d, $%d)", start, start+1)
}

var docSortMap = map[string]sortConfig{
	"pipeline":     {order: "d.created_at ASC, d.id ASC", cursorWhere: gtCursor("d")},
	"created_asc":  {order: "d.created_at ASC, d.id ASC", cursorWhere: gtCursor("d")},
	"created_desc": {order: "d.created_at DESC, d.id DESC", cursorWhere: ltCursor("d")},
	"title_asc":    {order: "LOWER(COALESCE(d.title,'')) ASC, d.id ASC", cursorWhere: gtTitleCursor},
	"title_desc":   {order: "LOWER(COALESCE(d.title,'')) DESC, d.id DESC", cursorWhere: ltTitleCursor},
}

func gtCursorBare(start int) string {
	return fmt.Sprintf("(created_at, id) > ($%d, $%d)", start, start+1)
}

func ltCursorBare(start int) string {
	return fmt.Sprintf("(created_at, id) < ($%d, $%d)", start, start+1)
}

var jobSortMap = map[string]sortConfig{
	"pipeline":     {order: "created_at ASC, id ASC", cursorWhere: gtCursorBare},
	"created_asc":  {order: "created_at ASC, id ASC", cursorWhere: gtCursorBare},
	"created_desc": {order: "created_at DESC, id DESC", cursorWhere: ltCursorBare},
}

// inClause returns "($start,$start+1,...,$start+n-1)" with n positional placeholders.
func inClause(start, n int) string {
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = fmt.Sprintf("$%d", start+i)
	}
	return "(" + strings.Join(parts, ",") + ")"
}

// encodeToken encodes (sortKey, lastID) into a base64 page token string.
func encodeToken(sortKey, lastID string) *string {
	s := core.EncodePageToken(model.PageToken{SortKey: sortKey, LastID: lastID})
	return &s
}
