#!/usr/bin/env bash
#
# Re-queues every "done" embed job back to "pending" so the worker re-runs the
# embed stage for every document. Useful after switching qdrant collections,
# changing the embed model, or recreating the qdrant index.
#
# The script auto-detects its environment:
#   - host:    BASE_URL is reachable from the host (e.g. local dev with
#              document-pipeline on localhost:8000).
#   - docker:  document-pipeline runs in docker but doesn't publish a host port
#              (the production setup behind Traefik). Runs the entire HTTP loop
#              inside one sidecar container on document-pipeline's docker
#              network, talking to http://document-pipeline:8000 directly.
#   - sql:     no HTTP path is available — UPDATE jobs through
#              `docker exec shared-postgres psql`.
#
# Override mode with:  MODE=host|docker|sql ./scripts/reembed.sh
# Override the base URL: BASE_URL=http://localhost:8000
# Override the container: PIPELINE_CONTAINER=document-pipeline

set -euo pipefail

MODE="${MODE:-auto}"
BASE_URL="${BASE_URL:-http://localhost:8000}"
PIPELINE_CONTAINER="${PIPELINE_CONTAINER:-document-pipeline}"
PIPELINE_INTERNAL_URL="${PIPELINE_INTERNAL_URL:-http://${PIPELINE_CONTAINER}:8000}"
SIDECAR_IMAGE="${SIDECAR_IMAGE:-alpine:latest}"
PG_CONTAINER="${PG_CONTAINER:-shared-postgres}"
PG_USER="${PG_USER:-${SHARED_DB_USER:-shared}}"
PG_DB="${PG_DB:-shared}"
PG_SCHEMA="${PG_SCHEMA:-document_pipeline}"

host_http_works() {
  curl -sf -o /dev/null --max-time 2 "$BASE_URL/api/v1/jobs?stages=embed&page_size=1"
}

container_running() {
  command -v docker >/dev/null 2>&1 \
    && docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null | grep -q true
}

# Pick the first network the pipeline container is on so the sidecar can
# resolve its service name.
pipeline_network() {
  docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{"\n"}}{{end}}' \
    "$PIPELINE_CONTAINER" 2>/dev/null | head -1
}

if [[ "$MODE" == "auto" ]]; then
  if host_http_works; then
    MODE=host
  elif container_running "$PIPELINE_CONTAINER"; then
    MODE=docker
  elif container_running "$PG_CONTAINER"; then
    MODE=sql
  else
    echo "error: no working mode — $BASE_URL unreachable; no '$PIPELINE_CONTAINER' or '$PG_CONTAINER' container" >&2
    echo "       try MODE=host BASE_URL=... or MODE=docker or MODE=sql" >&2
    exit 1
  fi
fi

# Clear the per-series content-hash cache so the worker's "content unchanged"
# short-circuit doesn't immediately skip every replayed job. The hash is keyed
# on the corpus content, not the embed model — so when this script is run
# after an embed-model swap, the cached hashes still match and replays no-op.
# Wiping the cache forces a real rebuild on the first pass.
if container_running "$PG_CONTAINER"; then
  echo "Clearing series_corpus_hash:* from $PG_SCHEMA.key_value..."
  docker exec -i "$PG_CONTAINER" psql -U "$PG_USER" "$PG_DB" -v ON_ERROR_STOP=1 \
    -c "DELETE FROM ${PG_SCHEMA}.key_value WHERE key LIKE 'series_corpus_hash:%';"
else
  echo "warning: $PG_CONTAINER not running — skipped series_corpus_hash cache wipe." >&2
  echo "         If the worker reports 'series corpus rebuild skipped — content unchanged'," >&2
  echo "         clear it manually: DELETE FROM ${PG_SCHEMA}.key_value WHERE key LIKE 'series_corpus_hash:%';" >&2
fi

# Delete the Qdrant collection to recreate with correct vector dimensions
# Use the same default collection name as docker-compose.yml
QDRANT_COLLECTION="${QDRANT_COLLECTION:-documents}"
QDRANT_URL="${QDRANT_URL:-http://localhost:6333}"

echo "Deleting Qdrant collection '$QDRANT_COLLECTION' at $QDRANT_URL..."
if [[ "$MODE" == "host" ]] || [[ "$MODE" == "docker" ]]; then
  # Use HTTP DELETE to remove the collection
  if curl -sf -X DELETE "$QDRANT_URL/collections/$QDRANT_COLLECTION"; then
    echo "  Collection deleted successfully."
  else
    echo "  Collection may not exist (this is OK if creating fresh)." >&2
  fi
else
  echo "  Skipping collection deletion in sql mode (no HTTP access to Qdrant)." >&2
fi

echo "Re-queuing all done embed jobs (mode=$MODE)..."

# Inner loop runs verbatim in either host shell or sidecar shell — `$1` is the
# base URL (localhost:8000 or document-pipeline:8000). Reads `done` jobs, PUTs
# them back to pending. Skips anything not in `done` so we don't disturb
# in-flight or errored jobs.
INNER_LOOP='
set -eu
BASE="$1"
PAGE_SIZE=200
total=0
page_token=""
while true; do
  url="$BASE/api/v1/jobs?stages=embed&page_size=$PAGE_SIZE"
  [ -n "$page_token" ] && url="$url&page_token=$page_token"
  resp=$(curl -sf "$url")
  ids=$(echo "$resp" | jq -r ".data[].id")
  next=$(echo "$resp" | jq -r ".next_page_token // empty")
  for id in $ids; do
    status=$(curl -sf "$BASE/api/v1/jobs/$id" | jq -r ".status")
    if [ "$status" != "done" ]; then
      echo "  SKIP $id ($status)"
      continue
    fi
    curl -sf -X PUT "$BASE/api/v1/jobs/$id/status" \
      -H "Content-Type: application/json" \
      -d "{\"status\":\"pending\"}" > /dev/null
    echo "  queued $id"
    total=$((total + 1))
  done
  [ -z "$next" ] && break
  page_token="$next"
done
echo "Done. Queued $total embed jobs."
'

case "$MODE" in
  host)
    command -v jq >/dev/null 2>&1 || { echo "error: host mode requires 'jq'" >&2; exit 1; }
    sh -c "$INNER_LOOP" _ "$BASE_URL"
    ;;

  docker)
    container_running "$PIPELINE_CONTAINER" \
      || { echo "error: container '$PIPELINE_CONTAINER' is not running" >&2; exit 1; }
    NET=$(pipeline_network)
    [[ -n "$NET" ]] \
      || { echo "error: could not determine network for '$PIPELINE_CONTAINER'" >&2; exit 1; }
    echo "  sidecar=$SIDECAR_IMAGE network=$NET target=$PIPELINE_INTERNAL_URL"
    # One sidecar, one apk install, the whole loop runs inside.
    docker run --rm -i --network "$NET" "$SIDECAR_IMAGE" sh -s -- "$PIPELINE_INTERNAL_URL" <<EOF
apk add --no-cache curl jq >/dev/null
$INNER_LOOP
EOF
    ;;

  sql)
    container_running "$PG_CONTAINER" \
      || { echo "error: container '$PG_CONTAINER' is not running" >&2; exit 1; }
    docker exec -i "$PG_CONTAINER" psql -U "$PG_USER" "$PG_DB" -v ON_ERROR_STOP=1 <<SQL
SET search_path = $PG_SCHEMA, public;
UPDATE jobs
   SET status = 'pending', updated_at = now()
 WHERE stage = 'embed' AND status = 'done'
RETURNING id;
SQL
    ;;

  *)
    echo "error: unknown MODE='$MODE' (expected: auto, host, docker, sql)" >&2
    exit 1
    ;;
esac
