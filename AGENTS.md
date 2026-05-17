# AGENTS.md

Orientation for anyone (human or agent) working in this repo.

## Development practices

- **Schema-first for the HTTP API.** `openapi.yaml` is the source of truth.
  Edit it before writing code, then run `make generate` to refresh
  `server/api/schema/types.gen.go` and `frontend/src/generated/`. Never
  hand-edit `*.gen.*` files. `make lint-openapi` checks the spec.
- **Run `gofmt -w .` (or `make fmt`) before committing.** Unformatted Go
  fails review.
- **Run tests before pushing:** `make test` for Go, `cd frontend && npx
  vitest run` for the frontend. `npx tsc --noEmit` for type-only check.
- **Keep the implementation against generated types.** Hand-written code
  consumes the generated types/clients — not the other way around.

## Repo layout

```
openapi.yaml              source of truth for the HTTP API
docker-compose.yml        local stack (Ollama, Postgres, Qdrant, faster-whisper)
Makefile                  build / test / generate / lint entrypoints

server/
  main.go                 binary entrypoint
  api/
    rest/                 HTTP handlers (hand-written, against generated types)
    schema/               generated Go types from openapi.yaml
  core/                   domain logic
    adk/                  Google ADK agent loop wrapper (sessions, streaming)
    port/                 interfaces consumed by core (LLM, streams, repos)
    model/                domain types
    worker.go             pipeline job executor
    ingest.go             document upload + dedupe
  store/                  implementations of core/port interfaces
    ollama/  postgres/  qdrant/  filesystem/  stream/  whisper/  …
  web/                    embedded SPA bundle (//go:embed)

frontend/
  src/
    pages/                top-level routes (Chat, Document, Dashboard, …)
    components/           shared React components
    state/                app-level stores + the SSE dispatcher
    generated/            generated TypeScript client from openapi.yaml
    api.ts                thin wrapper around the generated client
```

## Key files

- `server/core/port/llm.go` — `LLMInference` interface and `LLMChatResponse`.
  Any new model capability surfaces here first.
- `server/core/port/stream.go` — SSE event-name constants. Authoritative
  list of wire events emitted by the worker and chat streams.
- `server/core/adk/runner.go` — `RunAgent` and the `StreamEvent` kinds it
  emits. Maps ADK events into the SSE vocabulary.
- `server/core/adk/model.go` — `PortLLMModel` adapts our `port.LLMInference`
  into ADK's `model.LLM`.
- `server/api/rest/chat.go` — chat send/confirm endpoints; owns the agent
  loop wiring and session-to-message reconstruction.
- `server/core/worker.go` — pipeline job loop; emits the same SSE
  vocabulary via `store/stream`.
- `frontend/src/state/agentStream.ts` — single dispatcher for the agent
  SSE vocabulary; both `chatStore.ts` (fetched ReadableStream) and
  `Document.tsx` (EventSource) consume it.
- `frontend/src/state/chatStore.ts` — app-level chat store; persists
  streams across navigation.
- `frontend/src/components/AgentParts.tsx` — the `MessagePart` union
  (`text`, `thinking`, `tool_call`, `confirmation`) and its renderers.

## Conventions

- Backend dependencies flow inward: `store/*` depends on `core/port`, never
  the reverse. New external integrations get a port + a store impl.
- Comments explain *why* (non-obvious constraints, surprising invariants),
  not *what*. Named identifiers carry the *what*.
- Errors are wrapped with `fmt.Errorf("...: %w", err)` and surfaced; we
  rarely swallow.
