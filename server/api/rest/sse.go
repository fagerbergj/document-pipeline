package rest

import (
	"bytes"
	"fmt"
	"net/http"
)

// writeSSEEvent writes a single SSE event to the response writer.
func writeSSEEvent(w http.ResponseWriter, eventType, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
}

// writeSSEComment writes an SSE keepalive comment.
func writeSSEComment(w http.ResponseWriter, comment string) {
	fmt.Fprintf(w, ": %s\n\n", comment)
}

// sseHeaders sets the required headers for an SSE response.
func sseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

// sseUnbuffer writes a padding comment and flushes. Some reverse proxies
// (and the Go http2 stack with small writes) hold the response open until
// they've seen enough bytes; the padding forces an immediate handoff so the
// client begins receiving events from the first real write. X-Accel-Buffering
// covers nginx; this covers everything else.
func sseUnbuffer(w http.ResponseWriter, flusher http.Flusher) {
	// 2 KiB of pad — enough to defeat default buffers in caddy/traefik/cdn.
	pad := bytes.Repeat([]byte{' '}, 2048)
	fmt.Fprintf(w, ": %s\n\n", pad)
	flusher.Flush()
}
