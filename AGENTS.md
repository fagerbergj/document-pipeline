# AGENTS.md

Guidance for AI coding agents (and humans) working on this repo.

## Schema-first

This codebase is **schema-first**. `openapi.yaml` is the source of truth for
the HTTP API contract; both server and client code is generated from it.

When adding or modifying an endpoint, request body, response shape, SSE event
type, or any other public contract:

1. **Edit `openapi.yaml` first.** Define the new schema, parameters, response,
   or event description there. Document SSE event names in the relevant
   endpoint's `description` field.
2. **Regenerate the bindings:** `make generate`
   - `make generate-server` — runs `go generate ./server/api/schema/...` to
     emit Go types into `server/api/schema/types.gen.go`.
   - `make generate-client` — runs `npx openapi-ts` to emit the TypeScript
     client into `frontend/src/generated/`.
3. **Write the implementation** against the generated types. Never hand-edit
   `*.gen.*` files — they will be overwritten on the next `make generate`.
4. **Lint the spec:** `make lint-openapi` (Redocly CLI).

If the change is purely internal (e.g. a Go-only refactor, a frontend-only
state shape, or a non-public-API constant), schema-first does not apply —
just write the code.

## Before committing

- Run `gofmt -w .` (or `make fmt`) on any Go files you touched. CI relies on
  `gofmt` formatting being clean; unformatted code will fail review.
- Run `go test ./...` and the frontend test suite (`cd frontend && npx vitest run`).
- If you touched `openapi.yaml`, confirm `make generate` produced no
  unexpected diffs and that both `go build ./...` and `npx tsc --noEmit`
  still pass.

## Layout

- `server/api/rest/` — HTTP handlers (hand-written, against generated types).
- `server/api/schema/types.gen.go` — generated Go types from `openapi.yaml`.
- `server/core/` — domain logic and ports (interfaces).
- `server/store/` — port implementations (ollama, postgres, qdrant, etc.).
- `frontend/src/generated/` — generated TypeScript client.
- `frontend/src/` — hand-written React app, consuming the generated client.

## SSE event types

The chat and worker streams emit named SSE events. Current set:
`token`, `thinking`, `tool_call`, `tool_result`, `confirmation_request`,
`status`, `done`, `error`. Event names are not codegen'd (the OpenAPI 3.1
SSE story is prose-only), so adding a new one is a multi-step manual
change:

1. `server/core/port/stream.go` — add the `Event*` constant.
2. `server/core/adk/runner.go` — add the `StreamEvent*` kind, wire it
   through `JSONPayload()` and `SSEEventType()`, and emit it where
   appropriate in `RunAgent`.
3. `frontend/src/state/agentStream.ts` — add the event to
   `AGENT_EVENT_NAMES` and to the `dispatchAgentEvent` switch; add an
   `onX` field to `AgentStreamHandlers`. Both consumers
   (`chatStore.ts` and `Document.tsx`) pick it up via this shared
   dispatcher.
4. `openapi.yaml` — mention the new event name in the relevant endpoint
   description.
