# Go Rewrite

Full backend rewrite of the Python/FastAPI service in Go. The React frontend, `config/pipeline.yaml`, `prompts/`, and SQLite data are all unchanged. The REST API contract is preserved so the frontend works without modification.

**Approach:** schema-first, TDD, hexagonal architecture, `golang-migrate` for migrations, rewrite in-place on the `go-rewrite` branch.

**Deleted before merge to `main`:** this file and all Python source files.

---

## Final project structure

```
document-pipeline/
├── README.md
├── CONTRIBUTING.md
├── go.mod                             # module root — go run ./server
├── go.sum
├── Dockerfile                         # multi-stage: Node → Go binary
├── docker-compose.yml
│
├── db/
│   ├── README.md
│   └── migrations/
│       ├── 001_create_documents.{up,down}.sql
│       ├── 002_create_jobs.{up,down}.sql
│       ├── 003_create_artifacts.{up,down}.sql
│       ├── 004_create_stage_events.{up,down}.sql
│       ├── 005_create_contexts.{up,down}.sql
│       ├── 006_create_chat_sessions.{up,down}.sql
│       ├── 007_create_chat_messages.{up,down}.sql
│       └── 008_create_key_value.{up,down}.sql
│
├── frontend/                          # unchanged
│   ├── README.md
│   ├── src/
│   │   ├── __tests__/                 # Vitest component tests (Phase 10)
│   │   └── mocks/                     # MSW handlers (Phase 10)
│   └── vitest.config.ts               # (Phase 10)
│
└── server/
    ├── README.md
    ├── main.go                        # package main
    │
    ├── api/                           # OpenAPI .yaml docs (non-Go)
    │   └── rest/                      # Chi handlers — package rest
    │       ├── router.go
    │       ├── documents.go
    │       ├── jobs.go
    │       ├── pipelines.go
    │       ├── contexts.go
    │       ├── chat.go
    │       ├── webhook.go
    │       ├── sse.go
    │       └── middleware.go
    │
    ├── core/                          # package core — business logic
    │   ├── model/                     # package model — domain types
    │   │   ├── document.go
    │   │   ├── job.go
    │   │   ├── artifact.go
    │   │   ├── context.go
    │   │   ├── chat.go
    │   │   ├── pipeline.go
    │   │   ├── event.go
    │   │   └── pagination.go
    │   │
    │   ├── port/                      # package port — interfaces
    │   │   ├── repository.go          # DocumentRepo, JobRepo, ArtifactRepo, ContextRepo,
    │   │   │                          #   ChatRepo, ChatMessageRepo, StageEventRepo, KeyValueRepo
    │   │   ├── ollama.go
    │   │   ├── qdrant.go
    │   │   ├── openwebui.go
    │   │   ├── filesystem.go
    │   │   └── stream.go              # StreamManager (SSE channels)
    │   │
    │   ├── ingest.go                  # IngestService
    │   ├── worker.go                  # WorkerService
    │   ├── pipeline.go                # Config loader (pipeline.yaml + ${VAR})
    │   ├── prompts.go                 # text/template renderer
    │   ├── pagination.go              # PageToken encode/decode
    │   └── hash.go
    │
    └── store/                         # outbound adapters
        ├── sqlite/                    # package sqlite
        │   ├── db.go                  # Open, WAL, migrations
        │   ├── documents.go
        │   ├── jobs.go
        │   ├── artifacts.go
        │   ├── stage_events.go
        │   ├── contexts.go
        │   ├── chat.go
        │   ├── key_value.go
        │   └── pagination.go
        ├── ollama/                    # package ollama
        │   ├── client.go
        │   ├── generate.go            # vision + text (streaming NDJSON)
        │   ├── embed.go
        │   └── unload.go
        ├── qdrant/                    # package qdrant
        ├── openwebui/                 # package openwebui
        ├── filesystem/                # package filesystem
        └── stream/                    # package stream — SSE channel manager
```

**Removed in Phase 9:** `app.py`, `core/`, `adapters/`, `migrate.py`, `backfill_clarify_artifacts.py`, `requirements.txt`, `pyproject.toml`, `uv.lock`, `GO-REWRITE.md`.

---

## Key technical decisions

| Concern | Choice | Reason |
|---|---|---|
| HTTP router | Chi (`github.com/go-chi/chi/v5`) | Standard `http.Handler` — no lock-in; SSE works natively |
| SQLite driver | `modernc.org/sqlite` | Pure Go, CGO-free — simpler Docker builds |
| DB layer | `database/sql` + repository interfaces | Swap to Postgres by changing one file |
| Migrations | `golang-migrate/migrate/v4` | Schema-first; embedded via `//go:embed` |
| Concurrency | `errgroup` + `semaphore` | Replaces `asyncio.gather` + `asyncio.Semaphore` |
| SSE tokens | `chan StreamEvent` per job | Replaces `asyncio.Queue` |
| Prompts | `text/template` | Prompt files updated to `{{.VariableName}}` syntax |

### Async → goroutine translation

| Python | Go |
|---|---|
| `asyncio.Task` (worker loop) | `go func()` + `context.Context` |
| `asyncio.Semaphore` | `golang.org/x/sync/semaphore` |
| `asyncio.gather` | `golang.org/x/sync/errgroup` |
| `asyncio.Queue` (SSE) | `chan StreamEvent` (buffered) |
| `await asyncio.sleep(5)` | `time.Sleep(5 * time.Second)` |

### SQLite notes

- Enable WAL at open: `PRAGMA journal_mode=WAL` (concurrent readers + one writer)
- JSON columns (`options`, `runs`, `sources`, `rag_retrieval`): use a `jsonColumn[T]` helper implementing `sql.Scanner` + `driver.Valuer`
- Keyset pagination: `(sort_col, id) > (?, ?)` — no OFFSET

### Prompt templates

Current `prompts/*.txt` use Jinja2 `{{ variable_name }}`. Go `text/template` uses `{{.VariableName}}`. Update prompt files during Phase 3 (one-time change, no user-facing impact).

---

## Phases

### Phase 1 — Go module + golang-migrate

**Goal:** Go toolchain bootstrapped; migrations define the schema.

- `go.mod` at repo root
- `server/main.go` skeleton (runs migrations, exits)
- `db/migrations/` — 8 tables matching current SQLite schema exactly
- Migration runner embeds `db/migrations` via `//go:embed`

**Packages:** `golang-migrate/migrate/v4`, `modernc.org/sqlite`

**TDD:** Verify all up/down migrations round-trip against in-memory SQLite.

**Branch:** `go/phase-1-migrate` → merge to `go-rewrite`

---

### Phase 2 — Core interfaces and models

**Goal:** All domain types and port interfaces defined. No implementations yet.

- `server/core/model/` — all domain structs
- `server/core/port/` — all repository and client interfaces

**TDD:** Compile-time interface compliance assertions:
```go
var _ port.DocumentRepo = (*sqlite.DocumentRepo)(nil)
```

**Branch:** `go/phase-2-interfaces` → merge to `go-rewrite`

---

### Phase 3 — Core implementation

**Goal:** All business logic implemented and tested. No HTTP, no real DB.

- `server/core/ingest.go` — IngestService
- `server/core/worker.go` — WorkerService (stage loop, retry, backoff)
- `server/core/pipeline.go` — YAML config loader + `${VAR}` substitution
- `server/core/prompts.go` — `text/template` renderer
- `server/core/pagination.go` — PageToken encode/decode
- Update `prompts/*.txt` to Go template syntax

**TDD:** Table-driven tests with mock port implementations:
- Hash dedup
- Retry count + exponential backoff
- `continue_if` evaluation
- Pagination token round-trip
- Config loading with env var substitution

**Branch:** `go/phase-3-core` → merge to `go-rewrite`

---

### Phase 4 — HTTP endpoint implementation

**Goal:** All 20+ endpoints running in Go, backed by mock adapters.

- `server/api/rest/` — Chi router, all handlers, SSE helpers, middleware
- Static file serving + SPA fallback from embedded `frontend/dist`

**SSE pattern:**
```go
func (h *handler) streamJob(w http.ResponseWriter, r *http.Request) {
    flusher := w.(http.Flusher)
    ch := h.streams.Subscribe(jobID)
    defer h.streams.Unsubscribe(jobID)
    for {
        select {
        case evt := <-ch:
            fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, evt.Data)
            flusher.Flush()
        case <-r.Context().Done():
            return
        }
    }
}
```

**TDD:** `net/http/httptest` handler tests — request parsing, response shapes, pagination, SSE sequence, error responses.

**Branch:** `go/phase-4-http` → merge to `go-rewrite`

---

### Phase 5 — SQLite store adapter

**Goal:** Full SQLite repository implementation.

- `server/store/sqlite/` — all repos implementing `port.*Repo` interfaces
- `db.go` runs migrations via `golang-migrate` on open

**TDD:** Integration tests with `t.TempDir()` SQLite files — CRUD round-trips, pagination cursor continuity, concurrent reads, migration idempotency.

**Branch:** `go/phase-5-sqlite` → merge to `go-rewrite`

---

### Phase 6 — Ollama adapter

**Goal:** HTTP client for vision, text generation (streaming), embeddings, model unload.

- `server/store/ollama/` — NDJSON stream → `chan string`, base64 image encoding, cancellation via `context.WithCancel`

**TDD:** `httptest.Server` mock Ollama — streaming delivery, vision request, embed, cancellation, unload call.

**Branch:** `go/phase-6-ollama` → merge to `go-rewrite`

---

### Phase 7 — Qdrant, Open WebUI, filesystem adapters

**Goal:** All remaining outbound adapters implemented and tested.

- `server/store/qdrant/` — named vectors (text + optional image), search, delete
- `server/store/openwebui/` — markdown + YAML frontmatter upload, 400 treated as warning
- `server/store/filesystem/` — artifact file I/O
- `server/store/stream/` — `sync.Map` of `job_id → chan StreamEvent`

**TDD:** Mock HTTP server tests for Qdrant + Open WebUI; `t.TempDir()` for filesystem.

**Branch:** `go/phase-7-adapters` → merge to `go-rewrite`

---

### Phase 8 — E2E integration tests + documentation

**Goal:** Full pipeline runs end-to-end in tests. All docs updated.

- `server/test/integration/` — mock Ollama + real SQLite + temp vault
- Update `db/README.md`, `api/README.md`, `server/README.md`, root `README.md`, `CONTRIBUTING.md`

**Test commands:**
```bash
go test ./...                              # all tests
go test ./server/test/integration/... -v  # E2E only
go run ./server --db /tmp/test.db --vault /tmp/vault
```

**Branch:** `go/phase-8-e2e` → merge to `go-rewrite`

---

### Phase 9 — Docker full swap

**Goal:** Python code removed; Go binary runs in production.

**Dockerfile:**
```dockerfile
FROM node:22-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -o /pipeline ./server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /pipeline /pipeline
COPY config/ /config/
COPY prompts/ /prompts/
ENTRYPOINT ["/pipeline"]
```

**Pre-merge cleanup:**
```
rm -rf app.py core/ adapters/ migrate.py backfill_clarify_artifacts.py
rm -f requirements.txt pyproject.toml uv.lock GO-REWRITE.md
```

**Validation:** `docker compose up --build` — service starts, UI loads, test document processes end-to-end.

**Branch:** `go/phase-9-docker` → merge to `go-rewrite` → merge `go-rewrite` to `main`

---

### Phase 10 — React UI tests

**Goal:** Frontend component test suite.

- **Vitest** + **React Testing Library** — component unit tests
- **MSW (Mock Service Worker)** — intercept API calls without a real server

```typescript
test('approve button advances job to next stage', async () => {
  server.use(http.put('/api/v1/jobs/:id/status', () => HttpResponse.json(mockJob)))
  render(<JobDetail job={waitingJob} />)
  await userEvent.click(screen.getByRole('button', { name: /approve/i }))
  expect(screen.getByText('done')).toBeInTheDocument()
})
```

**Test command:** `cd frontend && npm run test`

**Branch:** `go/phase-10-ui-tests` → merge to `go-rewrite`

---

## Branching strategy

```
main  ←──────────────────────────────── merge after Phase 9
  │
go-rewrite  ←── go/phase-1-migrate
            ←── go/phase-2-interfaces
            ←── go/phase-3-core
            ←── go/phase-4-http
            ←── go/phase-5-sqlite
            ←── go/phase-6-ollama
            ←── go/phase-7-adapters
            ←── go/phase-8-e2e
            ←── go/phase-9-docker
            ←── go/phase-10-ui-tests
```

- Each phase branch merges to `go-rewrite` (not `main`) once tests pass
- `main` continues receiving Python bugfixes independently — merge `main → go-rewrite` periodically to pick up changes
- Do not rebase `go-rewrite` against `main` (preserves phase merge history)
- `go-rewrite` merges to `main` only after Phase 9 validation — one-way cutover
- Optional: open each `go/phase-N-*` as a PR targeting `go-rewrite` for review
