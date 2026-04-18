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

func TestOffsetTokenRoundTrip(t *testing.T) {
	for _, offset := range []int{0, 20, 100, 999} {
		encoded := EncodeOffsetToken(offset)
		decoded, ok := DecodeOffsetToken(encoded)
		if !ok {
			t.Errorf("offset %d: decode returned ok=false", offset)
		}
		if decoded != offset {
			t.Errorf("offset %d: round-trip gave %d", offset, decoded)
		}
	}
}

func TestDecodeOffsetToken_NotAnOffsetToken(t *testing.T) {
	// A regular cursor token should not decode as an offset token.
	tok := model.PageToken{SortKey: "2024-01", LastID: "abc"}
	_, ok := DecodeOffsetToken(EncodePageToken(tok))
	if ok {
		t.Error("cursor token should not parse as offset token")
	}
}

func TestDecodeOffsetToken_Invalid(t *testing.T) {
	_, ok := DecodeOffsetToken("not-valid-base64!!!")
	if ok {
		t.Error("invalid base64 should return ok=false")
	}
}
