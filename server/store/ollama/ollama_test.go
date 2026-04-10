package ollama_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/fagerbergj/document-pipeline/server/store/ollama"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newClient(t *testing.T, mux *http.ServeMux) *ollama.Client {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return ollama.New(srv.URL)
}

func writeNDJSON(w http.ResponseWriter, objects ...any) {
	for _, obj := range objects {
		b, _ := json.Marshal(obj)
		fmt.Fprintf(w, "%s\n", b)
	}
}

// ── GenerateVision ────────────────────────────────────────────────────────────

func TestGenerateVision(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/generate", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] != false {
			t.Error("vision should use stream:false")
		}
		if _, ok := req["images"]; !ok {
			t.Error("missing images field")
		}
		json.NewEncoder(w).Encode(map[string]any{"response": "hello vision"})
	})

	c := newClient(t, mux)
	var got string
	err := c.GenerateVision(context.Background(), "llava", "describe this", []byte("imgdata"), func(s string) { got = s })
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello vision" {
		t.Errorf("got %q, want %q", got, "hello vision")
	}
}

func TestGenerateVision_HTTPError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/generate", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	})
	c := newClient(t, mux)
	err := c.GenerateVision(context.Background(), "llava", "prompt", []byte("x"), nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── GenerateText ──────────────────────────────────────────────────────────────

func TestGenerateText(t *testing.T) {
	tokens := []string{"Hello", ", ", "world", "!"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/generate", func(w http.ResponseWriter, r *http.Request) {
		for i, tok := range tokens {
			done := i == len(tokens)-1
			writeNDJSON(w, map[string]any{"response": tok, "done": done})
		}
	})

	c := newClient(t, mux)
	var collected []string
	err := c.GenerateText(context.Background(), "mistral", "prompt", func(s string) {
		collected = append(collected, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(collected, ""); got != "Hello, world!" {
		t.Errorf("got %q", got)
	}
}

func TestGenerateText_ContextCancel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/generate", func(w http.ResponseWriter, r *http.Request) {
		// Stream forever until client disconnects.
		flusher := w.(http.Flusher)
		for i := 0; ; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
				fmt.Fprintf(w, `{"response":"tok","done":false}`+"\n")
				flusher.Flush()
			}
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	c := newClient(t, mux)

	count := 0
	done := make(chan error, 1)
	go func() {
		done <- c.GenerateText(ctx, "mistral", "prompt", func(s string) {
			count++
			if count == 5 {
				cancel()
			}
		})
	}()
	err := <-done
	if err == nil && count < 5 {
		t.Error("expected error or at least 5 tokens before cancel")
	}
}

// ── ChatStream ────────────────────────────────────────────────────────────────

func TestChatStream(t *testing.T) {
	tokens := []string{"Hi", " there"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["model"] != "mistral" {
			t.Errorf("unexpected model %v", req["model"])
		}
		for i, tok := range tokens {
			done := i == len(tokens)-1
			writeNDJSON(w, map[string]any{
				"message": map[string]string{"role": "assistant", "content": tok},
				"done":    done,
			})
		}
	})

	c := newClient(t, mux)
	var got []string
	err := c.ChatStream(context.Background(), "mistral", []port.LLMMessage{
		{Role: "user", Content: "hello"},
	}, func(s string) { got = append(got, s) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "") != "Hi there" {
		t.Errorf("got %q", strings.Join(got, ""))
	}
}

// ── GenerateEmbed ─────────────────────────────────────────────────────────────

func TestGenerateEmbed(t *testing.T) {
	want := []float32{0.1, 0.2, 0.3}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/embed", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float32{want},
		})
	})

	c := newClient(t, mux)
	got, err := c.GenerateEmbed(context.Background(), "nomic-embed-text", "some text")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGenerateEmbed_EmptyInput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/embed", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["input"] == "" {
			t.Error("empty input should be replaced with space")
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{0.0}}})
	})

	c := newClient(t, mux)
	c.GenerateEmbed(context.Background(), "nomic-embed-text", "")
}

// ── Unload ────────────────────────────────────────────────────────────────────

func TestUnload(t *testing.T) {
	called := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/generate", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["keep_alive"] == nil {
			t.Error("missing keep_alive")
		}
		called = true
		w.WriteHeader(http.StatusOK)
	})

	c := newClient(t, mux)
	if err := c.Unload(context.Background(), "mistral"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("unload did not call /api/generate")
	}
}

func TestUnload_ErrorIsIgnored(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/generate", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gpu gone", http.StatusInternalServerError)
	})
	c := newClient(t, mux)
	// Should not return an error even when the server fails.
	if err := c.Unload(context.Background(), "mistral"); err != nil {
		t.Errorf("Unload should swallow errors, got: %v", err)
	}
}
