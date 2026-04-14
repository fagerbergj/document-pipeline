# Go Rewrite

Full backend rewrite of the Python/FastAPI service in Go. The React frontend, `config/pipeline.yaml`, `prompts/`, and SQLite data are all unchanged. The REST API contract is preserved so the frontend works without modification.

**Approach:** schema-first, TDD, hexagonal architecture, custom migration runner, rewrite in-place on the `go-rewrite` branch.

**Deleted before merge to `main`:** this file and all Python source files.

---

## Phase status

| Phase | Branch | Status |
|---|---|---|
| 1 — Go module + migrations | `go/phase-1-migrate` | ✅ merged |
| 2 — Core interfaces and models | `go/phase-2-interfaces` | ✅ merged |
| 3 — Core implementation | `go/phase-3-core` | ✅ merged |
| 4 — HTTP endpoints | `go/phase-4-http` | ✅ merged |
| 5 — SQLite store adapter | `go/phase-5-sqlite` | ✅ merged |
| 6 — Ollama adapter | `go/phase-6-ollama` | ✅ merged |
| 7 — Qdrant, Open WebUI, filesystem adapters | `go/phase-7-adapters` | ✅ merged |
| 8 — E2E integration tests + docs | `go/phase-8-e2e` | 🔄 in progress |
| 9 — Docker full swap | `go/phase-9-docker` | ⬜ not started |
| 10 — React UI tests | `go/phase-10-ui-tests` | ⬜ not started |

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
│   ├── migrations.go                  # //go:embed migrations/*.sql
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
    ├── api/
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
    │   │   ├── job.go                 # JobStatus, Confidence typed string enums
    │   │   ├── artifact.go
    │   │   ├── context.go
    │   │   ├── chat.go
    │   │   ├── pipeline.go
    │   │   ├── event.go
    │   │   └── pagination.go
    │   │
    │   ├── port/                      # package port — interfaces named after domain concepts
    │   │   ├── repository.go          # DocumentRepo, JobRepo, ArtifactRepo, ContextRepo,
    │   │   │                          #   ChatRepo, ChatMessageRepo, StageEventRepo, KeyValueRepo
    │   │   ├── llm.go                 # LLMInference (vision, text, embed, unload)
    │   │   ├── embed.go               # EmbedStore (upsert, search, delete)
    │   │   ├── artifact.go            # DocumentArtifactStore (save, read)
    │   │   └── stream.go              # StreamManager (SSE channels)
    │   │
    │   ├── ingest.go                  # IngestService — Ingest(IngestRequest)
    │   ├── worker.go                  # WorkerService — stage loop, retry, backoff
    │   ├── pipeline.go                # YAML config loader + ${VAR} substitution
    │   ├── prompts.go                 # text/template renderer + prompt data types
    │   ├── pagination.go              # PageToken encode/decode (base64 JSON)
    │   └── hash.go                    # SHA-256 content hash
    │
    └── store/                         # outbound adapters
        ├── sqlite/                    # package sqlite — all DB repositories
        │   ├── db.go                  # Open, WAL, custom migration runner
        │   ├── documents.go
        │   ├── jobs.go
        │   ├── artifacts.go
        │   ├── stage_events.go
        │   ├── contexts.go
        │   ├── chat.go
        │   ├── key_value.go
        │   └── pagination.go          # keyset pagination SQL builder
        ├── ollama/                    # package ollama — implements LLMInference
        │   ├── client.go
        │   ├── generate.go            # vision + text (streaming NDJSON)
        │   ├── embed.go
        │   └── unload.go
        ├── embed/                     # package embed — implements EmbedStore
        │   └── coordinator.go         # EmbedStoreCoordinator: wraps qdrant + openwebui
        ├── qdrant/                    # package qdrant — used by embed.EmbedStoreCoordinator
        ├── openwebui/                 # package openwebui — used by embed.EmbedStoreCoordinator
        ├── filesystem/                # package filesystem — implements DocumentArtifactStore
        └── stream/                    # package stream — implements StreamManager
```

**Removed in Phase 9:** `app.py`, `core/`, `adapters/`, `migrate.py`, `backfill_clarify_artifacts.py`, `requirements.txt`, `pyproject.toml`, `uv.lock`, `GO-REWRITE.md`.

---

## Key technical decisions

| Concern | Choice | Reason |
|---|---|---|
| HTTP router | Chi (`github.com/go-chi/chi/v5`) | Standard `http.Handler` — no lock-in; SSE works natively |
| SQLite driver | `modernc.org/sqlite` | Pure Go, CGO-free — simpler Docker builds |
| DB layer | `database/sql` + repository interfaces | Swap to Postgres by changing one file |
| Migrations | Custom runner (`db/migrations.go`) | `golang-migrate`'s sqlite3 driver pulls in CGO; custom ~80-line runner using `modernc.org/sqlite` directly |
| Concurrency | `errgroup` + `semaphore` | Replaces `asyncio.gather` + `asyncio.Semaphore` |
| SSE tokens | `chan StreamEvent` per job | Replaces `asyncio.Queue` |
| Prompts | `text/template` | Prompt files updated to `{{.VariableName}}` syntax |
| Port naming | Domain concepts, not technologies | `LLMInference` not `OllamaClient`; `EmbedStore` not `QdrantClient` |
| Embed coordination | `EmbedStoreCoordinator` in `store/embed/` | Qdrant + Open WebUI are implementation details; core only sees `EmbedStore` |
| Ingest API | `Ingest(IngestRequest)` | Single generic method — HTTP handlers do transport-specific parsing |

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
- Migration tracking: `_migrations` table, applied once, idempotent on re-open

### CI

- GitHub Actions runs `go test ./...` and `gofmt -l .` on every push and PR
- Both checks are required on `main` and `go-rewrite` via branch protection

---

## Phases

### Phase 1 — Go module + migrations ✅

**Goal:** Go toolchain bootstrapped; migrations define the schema.

- `go.mod` at repo root (module `github.com/fagerbergj/document-pipeline`, Go 1.25)
- `server/main.go` skeleton
- `db/migrations/` — 8 tables matching current SQLite schema exactly
- `db/migrations.go` — `//go:embed` package at repo root (embed can't use `../` paths)
- Custom migration runner in `server/store/sqlite/db.go` — tracks applied migrations in `_migrations` table

**Tests:** Up/down round-trip; idempotent re-open.

---

### Phase 2 — Core interfaces and models ✅

**Goal:** All domain types and port interfaces defined. No implementations.

- `server/core/model/` — all domain structs; `JobStatus` and `Confidence` are typed string enums
- `server/core/port/` — all interfaces named after domain concepts, not technologies

**Port design:**
- `EmbedStore` is the core-facing interface; `EmbedStoreCoordinator` implements it by coordinating `store/qdrant` and `store/openwebui` — those are not ports
- Port files named after domain (`llm.go`, `embed.go`, `artifact.go`) not implementation

---

### Phase 3 — Core implementation ✅

**Goal:** All business logic implemented and tested. No HTTP, no real DB.

- `ingest.go` — `IngestService.Ingest(IngestRequest)`: hash, dedup, save artifact, create doc + first job
- `worker.go` — `WorkerService.Run(ctx)`: stage loop, OCR/LLM/embed handlers, retry with 2^n backoff (max 3 → error)
- `pipeline.go` — YAML loader with `os.Expand` for `${VAR}` substitution
- `prompts.go` — `text/template` renderer; typed data structs per stage (`OCRPromptData`, `ClarifyPromptData`, `ClassifyPromptData`)
- `pagination.go` — base64 JSON encode/decode
- `prompts/*.txt` — converted from Jinja2 to Go `text/template` syntax

**Tests:** 11 unit tests — hash, pagination round-trip, pipeline loading, `continue_if`, `start_if`, `skip_if`, LLM response parsing (XML and JSON formats).

---

### Phase 4 — HTTP endpoints

**Goal:** All endpoints running in Go, backed by real adapters once available.

- `server/api/rest/` — Chi router, all handlers, SSE helpers, CORS middleware
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

**Tests:** `net/http/httptest` handler tests — request parsing, response shapes, pagination, SSE sequence, error responses.

---

### Phase 5 — SQLite store adapter

**Goal:** Full SQLite repository implementation.

- `server/store/sqlite/` — all repos implementing `port.*Repo` interfaces
- Interface compliance assertions: `var _ port.DocumentRepo = (*DocumentRepo)(nil)`

**Tests:** Integration tests with `t.TempDir()` SQLite files — CRUD round-trips, pagination cursor continuity, concurrent reads, migration idempotency.

---

### Phase 6 — Ollama adapter

**Goal:** HTTP client implementing `port.LLMInference`.

- `server/store/ollama/` — NDJSON streaming, base64 image encoding, cancellation via context

**Tests:** `httptest.Server` mock — streaming delivery, vision request, embed, cancellation, unload.

---

### Phase 7 — Qdrant, Open WebUI, filesystem, stream adapters

**Goal:** All remaining outbound adapters.

- `server/store/embed/coordinator.go` — `EmbedStoreCoordinator` implements `port.EmbedStore`
- `server/store/qdrant/` — named vectors (text + optional image), search, delete
- `server/store/openwebui/` — markdown + YAML frontmatter upload; 400 treated as warning
- `server/store/filesystem/` — implements `port.DocumentArtifactStore`
- `server/store/stream/` — `sync.Map` of `job_id → chan StreamEvent`, implements `port.StreamManager`

**Tests:** Mock HTTP servers for Qdrant + Open WebUI; `t.TempDir()` for filesystem.

---

### Phase 8 — E2E integration tests + documentation

**Goal:** Full pipeline runs end-to-end in tests. All docs updated.

- `server/test/integration/` — mock Ollama + real SQLite + temp vault
- Update `db/README.md`, `api/README.md`, `server/README.md`, root `README.md`, `CONTRIBUTING.md`

```bash
go test ./...                              # all tests
go test ./server/test/integration/... -v  # E2E only
go run ./server --db /tmp/test.db --vault /tmp/vault
```

---

### Phase 9 — Docker full swap

**Goal:** Python code removed; Go binary runs in production.

```dockerfile
FROM node:22-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.25-alpine AS builder
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

---

## Branching strategy

```
main  ←──────────────────────────────── merge after Phase 9
  │
go-rewrite  ←── go/phase-1-migrate      ✅
            ←── go/phase-2-interfaces   ✅
            ←── go/phase-3-core         ✅
            ←── go/phase-4-http         ✅
            ←── go/phase-5-sqlite       ✅
            ←── go/phase-6-ollama       ✅
            ←── go/phase-7-adapters       ✅
            ←── go/phase-8-e2e
            ←── go/phase-9-docker
            ←── go/phase-10-ui-tests
```

- Each phase branch merges to `go-rewrite` (not `main`) once CI passes
- `main` continues receiving Python bugfixes independently — merge `main → go-rewrite` periodically
- Do not rebase `go-rewrite` against `main`
- `go-rewrite` merges to `main` only after Phase 9 validation — one-way cutover
