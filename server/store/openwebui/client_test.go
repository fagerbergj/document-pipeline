package openwebui_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fagerbergj/document-pipeline/server/store/openwebui"
)

func TestUpsert(t *testing.T) {
	fileUploaded := false
	addedToKB := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/files/":
			json.NewEncoder(w).Encode([]any{}) // no existing files
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/files/":
			fileUploaded = true
			json.NewEncoder(w).Encode(map[string]string{"id": "file-123"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file/add"):
			addedToKB = true
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := openwebui.New(srv.URL, "token", "kb-1")
	err := c.Upsert(context.Background(), "550e8400-e29b-41d4-a716-446655440000",
		"Test Doc", "some text", map[string]any{"stage": "classify"})
	if err != nil {
		t.Fatal(err)
	}
	if !fileUploaded {
		t.Error("expected file to be uploaded")
	}
	if !addedToKB {
		t.Error("expected file to be added to knowledge base")
	}
}

func TestUpsert_ReplacesExisting(t *testing.T) {
	removed := false
	deleted := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/files/":
			json.NewEncoder(w).Encode([]map[string]string{
				{"id": "old-file", "filename": "550e8400-e29b-41d4-a716-446655440000.md"},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file/remove"):
			removed = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/files/"):
			deleted = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/files/":
			json.NewEncoder(w).Encode(map[string]string{"id": "new-file"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file/add"):
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := openwebui.New(srv.URL, "token", "kb-1")
	if err := c.Upsert(context.Background(), "550e8400-e29b-41d4-a716-446655440000", "T", "text", nil); err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Error("expected old file to be removed from KB")
	}
	if !deleted {
		t.Error("expected old file to be deleted")
	}
}

func TestUpsert_DuplicateWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/files/":
			json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/files/":
			json.NewEncoder(w).Encode(map[string]string{"id": "file-dup"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file/add"):
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"detail":"duplicate content"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := openwebui.New(srv.URL, "token", "kb-1")
	// Should not return error — duplicate is a warning only.
	if err := c.Upsert(context.Background(), "550e8400-e29b-41d4-a716-446655440000", "T", "text", nil); err != nil {
		t.Fatalf("expected no error on duplicate, got: %v", err)
	}
}

func TestDelete(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/files/":
			json.NewEncoder(w).Encode([]map[string]string{
				{"id": "file-abc", "filename": "550e8400-e29b-41d4-a716-446655440000.md"},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file/remove"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/files/"):
			deleted = true
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := openwebui.New(srv.URL, "token", "kb-1")
	if err := c.Delete(context.Background(), "550e8400-e29b-41d4-a716-446655440000"); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Error("expected file to be deleted")
	}
}
