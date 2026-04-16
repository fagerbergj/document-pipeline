# Stage 1 — build React frontend
FROM node:22-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# Stage 2 — build Go binary (with frontend/dist embedded via //go:embed)
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/frontend/dist ./server/web/dist
RUN CGO_ENABLED=0 go build -o /pipeline ./server

# Stage 3 — minimal runtime image
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /pipeline /pipeline
COPY config/ /config/
COPY prompts/ /prompts/
COPY db/migrations/ /db/migrations/
EXPOSE 8000
ENTRYPOINT ["/pipeline"]
CMD ["--migrations", "/db/migrations", "--pipeline", "/config/pipeline.yaml"]
