#!/usr/bin/env bash
#
# Embedding retrieval bench: run a handful of representative queries against the
# live embed model + Qdrant collection and print the top-K similarity scores
# with a short summary (document title + chunk snippet) for each hit.
#
# Use it to sanity-check a new embed model and to pick a RAG_MIN_SCORE cutoff:
# eyeball where real matches stop and the noise band begins, then set
# RAG_MIN_SCORE accordingly (see server/api/rest/chat.go:defaultRAGMinScore).
#
# Pipeline of one query:
#   query text --(POST /v1/embeddings)--> dense vector
#              --(POST /collections/<c>/points/query, using "text")--> scored hits
#
# The whole thing runs inside a one-shot sidecar container on the docker network
# where both services resolve: llm-swap and qdrant publish no useful host ports
# in production (Qdrant publishes none at all), so a host-side curl can't reach
# them. The sidecar joins BENCH_NETWORK and talks to the internal service names.
#
# Config (all overridable via env):
#   BENCH_NETWORK      docker network to run on                 (default llm_default)
#   LLM_URL            embeddings endpoint base, no /v1 suffix  (default http://llm-swap:11436)
#   LLM_API_KEY        bearer token for the embeddings endpoint (default empty)
#   EMBED_MODEL        embedding model name                     (default qwen3-embed)
#   QDRANT_URL         Qdrant base URL                          (default http://qdrant:6333)
#   QDRANT_COLLECTION  collection name                          (default documents)
#   QDRANT_API_KEY     Qdrant api-key header                    (default empty)
#   TOP_K              hits to show per query                   (default 5)
#   SIDECAR_IMAGE      image with a shell + apk                 (default alpine:latest)
#
# Requires: docker (curl/jq are installed inside the sidecar).

set -euo pipefail

BENCH_NETWORK="${BENCH_NETWORK:-llm_default}"
LLM_URL="${LLM_URL:-http://llm-swap:11436}"
LLM_API_KEY="${LLM_API_KEY:-}"
EMBED_MODEL="${EMBED_MODEL:-qwen3-embed}"
QDRANT_URL="${QDRANT_URL:-http://qdrant:6333}"
QDRANT_COLLECTION="${QDRANT_COLLECTION:-documents}"
QDRANT_API_KEY="${QDRANT_API_KEY:-}"
TOP_K="${TOP_K:-5}"
SIDECAR_IMAGE="${SIDECAR_IMAGE:-alpine:latest}"

# Qwen3-Embedding is trained to receive an instruction on the QUERY side only
# (documents are embedded raw). Without it, query vectors sit in the wrong
# regime, which depresses both scores and ranking. Set QUERY_INSTRUCT="" to
# disable and compare. This is applied here in the bench; the live RAG path
# would need the same prefix to match.
QUERY_INSTRUCT="${QUERY_INSTRUCT:-Given a question, retrieve passages from the tabletop campaign notes that answer it}"

# --- queries to benchmark -----------------------------------------------------
# Natural-language questions retrieve better than keyword salads with a dense
# model. Keep them specific enough that the right document should clearly win.
QUERIES=(
  "Who is Zuk Bugbag and what organization did he co-found?"
  "What ship is the campaign aboard and where is it sailing?"
  "What empire is ruled by King Dwindle and what is it known for?"
  "Who is Menma Everdeen and why was she on the cruise?"
)

# ------------------------------------------------------------------------------

command -v docker >/dev/null 2>&1 || { echo "error: 'docker' is required" >&2; exit 1; }

if [[ ${#QUERIES[@]} -eq 0 ]]; then
  echo "No queries configured. Add some to the QUERIES=( ... ) array in $0." >&2
  exit 1
fi

# Export the secrets so they pass through to the sidecar via `-e NAME` (value
# stays out of the docker run argv, unlike `-e NAME=value`).
export LLM_API_KEY QDRANT_API_KEY

echo "network=$BENCH_NETWORK  model=$EMBED_MODEL  collection=$QDRANT_COLLECTION  top_k=$TOP_K"

# Queries are passed as positional args; config via env. The inner script avoids
# `set -e` so one failing query reports its HTTP status and the run continues.
docker run --rm -i --network "$BENCH_NETWORK" \
  -e LLM_URL="$LLM_URL" -e LLM_API_KEY \
  -e EMBED_MODEL="$EMBED_MODEL" -e QUERY_INSTRUCT="$QUERY_INSTRUCT" \
  -e QDRANT_URL="$QDRANT_URL" -e QDRANT_COLLECTION="$QDRANT_COLLECTION" -e QDRANT_API_KEY \
  -e TOP_K="$TOP_K" \
  "$SIDECAR_IMAGE" sh -s -- "${QUERIES[@]}" <<'EOF'
set -u
apk add --no-cache curl jq >/dev/null 2>&1 || { echo "sidecar: failed to install curl/jq" >&2; exit 1; }

# POST with optional auth header; echoes "<body>\n<http_code>".
emb_post() {
  if [ -n "${LLM_API_KEY:-}" ]; then
    curl -s -w '\n%{http_code}' -H "Content-Type: application/json" \
      -H "Authorization: Bearer $LLM_API_KEY" -X POST "$1" --data "$2"
  else
    curl -s -w '\n%{http_code}' -H "Content-Type: application/json" -X POST "$1" --data "$2"
  fi
}
qd_post() {
  if [ -n "${QDRANT_API_KEY:-}" ]; then
    curl -s -w '\n%{http_code}' -H "Content-Type: application/json" \
      -H "api-key: $QDRANT_API_KEY" -X POST "$1" --data "$2"
  else
    curl -s -w '\n%{http_code}' -H "Content-Type: application/json" -X POST "$1" --data "$2"
  fi
}

echo "llm=$LLM_URL  qdrant=$QDRANT_URL"
echo

for q in "$@"; do
  echo "── query: $q"

  # Qwen3 query format: "Instruct: <task>\nQuery: <text>" (query side only).
  if [ -n "${QUERY_INSTRUCT:-}" ]; then
    qin=$(printf 'Instruct: %s\nQuery: %s' "$QUERY_INSTRUCT" "$q")
  else
    qin="$q"
  fi
  body=$(jq -n --arg m "$EMBED_MODEL" --arg in "$qin" '{model:$m, input:$in}')
  resp=$(emb_post "$LLM_URL/v1/embeddings" "$body")
  code=$(printf '%s' "$resp" | tail -n1)
  data=$(printf '%s' "$resp" | sed '$d')
  if [ "$code" != "200" ]; then
    echo "  embed failed [HTTP $code]: $(printf '%s' "$data" | head -c 200)" >&2
    echo; continue
  fi
  vector=$(printf '%s' "$data" | jq -c '.data[0].embedding // empty')
  if [ -z "$vector" ]; then
    echo "  embed failed: no embedding in response: $(printf '%s' "$data" | head -c 200)" >&2
    echo; continue
  fi

  qbody=$(jq -n --argjson q "$vector" --argjson k "$TOP_K" \
    '{query:$q, using:"text", limit:$k, with_payload:true}')
  resp=$(qd_post "$QDRANT_URL/collections/$QDRANT_COLLECTION/points/query" "$qbody")
  code=$(printf '%s' "$resp" | tail -n1)
  data=$(printf '%s' "$resp" | sed '$d')
  if [ "$code" != "200" ]; then
    echo "  qdrant query failed [HTTP $code]: $(printf '%s' "$data" | head -c 200)" >&2
    echo; continue
  fi

  # "score  title — snippet" per hit; snippet truncated for readability.
  printf '%s' "$data" | jq -r '
    .result.points[]?
    | [ (.score | tostring | .[0:6]),
        (.payload.title // "(untitled)"),
        ((.payload.text // "") | gsub("\\s+"; " ") | .[0:80]) ]
    | "  " + (.[0]) + "  " + (.[1]) + " — " + (.[2])
  ' || echo "  (no parseable results)"
  echo
done
EOF
