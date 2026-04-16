.PHONY: build test fmt vet generate generate-client frontend-build clean

# Full local build: frontend → embed → Go binary.
build: frontend-build
	go build -o pipeline ./server

# Compile the SPA and stage it where //go:embed picks it up.
frontend-build:
	cd frontend && npm ci && npm run build
	rm -rf server/web/dist
	cp -R frontend/dist server/web/dist

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Regenerate both Go types and TypeScript client from openapi.yaml.
generate: generate-server generate-client

generate-server:
	go generate ./server/api/schema/...

generate-client:
	cd frontend && npx openapi-ts

# Lint openapi.yaml using Redocly CLI (OpenAPI 3.1 aware).
lint-openapi:
	npx --yes @redocly/cli@latest lint openapi.yaml

clean:
	rm -rf server/web/dist frontend/dist pipeline
