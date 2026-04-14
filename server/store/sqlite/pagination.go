package sqlite

import (
	"strings"

	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/model"
)

// sortConfig describes how to ORDER and continue a keyset cursor for one sort mode.
type sortConfig struct {
	order       string // ORDER BY fragment
	cursorWhere string // keyset WHERE fragment, with two "?" placeholders: (sort_val, id)
}

var docSortMap = map[string]sortConfig{
	"pipeline":     {order: "d.created_at ASC, d.id ASC", cursorWhere: "(d.created_at, d.id) > (?, ?)"},
	"created_asc":  {order: "d.created_at ASC, d.id ASC", cursorWhere: "(d.created_at, d.id) > (?, ?)"},
	"created_desc": {order: "d.created_at DESC, d.id DESC", cursorWhere: "(d.created_at, d.id) < (?, ?)"},
	"title_asc":    {order: "LOWER(COALESCE(d.title,'')) ASC, d.id ASC", cursorWhere: "(LOWER(COALESCE(d.title,'')), d.id) > (?, ?)"},
	"title_desc":   {order: "LOWER(COALESCE(d.title,'')) DESC, d.id DESC", cursorWhere: "(LOWER(COALESCE(d.title,'')), d.id) < (?, ?)"},
}

var jobSortMap = map[string]sortConfig{
	"pipeline":     {order: "created_at ASC, id ASC", cursorWhere: "(created_at, id) > (?, ?)"},
	"created_asc":  {order: "created_at ASC, id ASC", cursorWhere: "(created_at, id) > (?, ?)"},
	"created_desc": {order: "created_at DESC, id DESC", cursorWhere: "(created_at, id) < (?, ?)"},
}

// inClause returns "(?,?,?)" with n placeholders.
func inClause(n int) string {
	return "(" + strings.Repeat("?,", n-1) + "?)"
}

// encodeToken encodes (sortKey, lastID) into a base64 page token string.
func encodeToken(sortKey, lastID string) *string {
	s := core.EncodePageToken(model.PageToken{SortKey: sortKey, LastID: lastID})
	return &s
}
