package core

import (
	"testing"

	"github.com/fagerbergj/document-pipeline/server/core/model"
)

func TestPageTokenRoundTrip(t *testing.T) {
	tok := model.PageToken{SortKey: "2024-01", LastID: "abc-123"}
	encoded := EncodePageToken(tok)
	decoded, err := DecodePageToken(encoded)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if decoded.SortKey != tok.SortKey || decoded.LastID != tok.LastID {
		t.Errorf("round-trip mismatch: got %+v, want %+v", decoded, tok)
	}
}

func TestDecodePageToken_Invalid(t *testing.T) {
	_, err := DecodePageToken("not-base64!!!")
	if err == nil {
		t.Error("expected error for invalid token")
	}
}
