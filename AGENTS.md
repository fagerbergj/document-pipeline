# AGENTS.md

- Treat `openapi.yaml` as the HTTP contract. Edit it first, run `make generate`, and commit the regenerated `server/api/schema/types.gen.go` and `frontend/src/generated/`; never hand-edit generated output.
- Keep backend dependencies inward: `server/core` must not import `server/store`. Add external integrations as a `core/port` plus a `store` implementation.
- For Go changes, run `make fmt`, `make vet`, and `make test`. The integration suite starts PostgreSQL through testcontainers and requires Docker.
- For frontend changes, run `cd frontend && npm test && npm run build`. Browser tests require Chromium; use `.github/workflows/test.yml` for its installation step.
