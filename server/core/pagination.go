package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/fagerbergj/document-pipeline/server/core/model"
)

// EncodePageToken encodes a PageToken to a base64 JSON string.
func EncodePageToken(t model.PageToken) string {
	b, _ := json.Marshal(t)
	return base64.StdEncoding.EncodeToString(b)
}

// DecodePageToken decodes a base64 JSON string into a PageToken.
func DecodePageToken(s string) (model.PageToken, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return model.PageToken{}, fmt.Errorf("invalid page token: %w", err)
	}
	var t model.PageToken
	if err := json.Unmarshal(b, &t); err != nil {
		return model.PageToken{}, fmt.Errorf("invalid page token: %w", err)
	}
	return t, nil
}

// EncodeOffsetToken encodes a search-mode offset into an opaque page token string.
func EncodeOffsetToken(offset int) string {
	b, _ := json.Marshal(map[string]int{"offset": offset})
	return base64.StdEncoding.EncodeToString(b)
}

// DecodeOffsetToken decodes an opaque token back to an integer offset.
// Returns (0, false) if the token is not an offset token.
func DecodeOffsetToken(s string) (int, bool) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return 0, false
	}
	var m map[string]int
	if err := json.Unmarshal(b, &m); err != nil {
		return 0, false
	}
	v, ok := m["offset"]
	return v, ok
}
