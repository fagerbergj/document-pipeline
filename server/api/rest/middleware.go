package rest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		// Log the UID, not the username — usernames may be PII. The UID is
		// also what scopes sessions, so logs and storage stay correlated.
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration", time.Since(start),
			"uid", r.Header.Get("X-Authentik-Uid"),
		)
	})
}

// authUserKey is the context key for storing the authenticated user ID.
type authUserKey struct{}

// WithAuthUser extracts the Authentik UID from request headers and stores it in context.
func WithAuthUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := extractAuthUser(r)
		if user != "" {
			r = r.WithContext(context.WithValue(r.Context(), authUserKey{}, user))
		}
		next.ServeHTTP(w, r)
	})
}

// extractAuthUser reads the stable caller identity from X-Authentik-Uid.
// UID is used (not username) because it survives Authentik profile renames;
// session storage is keyed by this value, so changing it would orphan a
// user's chats.
//
// Trusting this header is only safe because the service sits behind the
// api_gateway Traefik instance, whose authentik@file middleware calls the
// outpost and uses authResponseHeaders to overwrite X-Authentik-* on the
// forwarded request — so a client that sets the header itself has it
// replaced before the backend sees it. If this service is ever exposed
// outside that gateway (e.g. bound to a public port directly), the header
// becomes spoofable and this function must change.
func extractAuthUser(r *http.Request) string {
	return r.Header.Get("X-Authentik-Uid")
}

// AuthUserFromContext retrieves the authenticated user ID from context, if present.
func AuthUserFromContext(ctx context.Context) (string, bool) {
	user, ok := ctx.Value(authUserKey{}).(string)
	return user, ok
}

// requireAuth checks if the request has an authenticated user.
// Returns 401 Unauthorized if no auth user found.
func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := AuthUserFromContext(r.Context()); !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// writeJSON writes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON encode", "err", err)
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"detail": msg})
}

// decodeJSON decodes the request body into v, returning false and writing 422 on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid request body: "+err.Error())
		return false
	}
	return true
}
