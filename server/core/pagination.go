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
