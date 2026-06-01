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
# Config (all overridable via env):
#   LLM_URL            embeddings endpoint base, no /v1 suffix  (default http://localhost:11436)
#   LLM_API_KEY        bearer token for the embeddings endpoint (default empty)
#   EMBED_MODEL        embedding model name                     (default qwen3-embed)
#   QDRANT_URL         Qdrant base URL                          (default http://localhost:6333)
#   QDRANT_COLLECTION  collection name                          (default documents)
#   QDRANT_API_KEY     Qdrant api-key header                    (default empty)
#   TOP_K              hits to show per query                   (default 5)
#
# Requires: curl, jq.

set -euo pipefail

LLM_URL="${LLM_URL:-http://localhost:11436}"
LLM_API_KEY="${LLM_API_KEY:-}"
EMBED_MODEL="${EMBED_MODEL:-qwen3-embed}"
QDRANT_URL="${QDRANT_URL:-http://localhost:6333}"
QDRANT_COLLECTION="${QDRANT_COLLECTION:-documents}"
QDRANT_API_KEY="${QDRANT_API_KEY:-}"
TOP_K="${TOP_K:-5}"

# --- queries to benchmark -----------------------------------------------------
# Fill in a few representative queries for this dataset. Mix easy exact-ish
# lookups with harder paraphrases so the score spread is informative.
QUERIES=(
  # Campaign character: Who is Zuk Bugbag and what organization did he co-found?
  "Zuk Bugbag Ethereal Refuters"
  # Campaign: What ship is the campaign aboard and where is it going?
  "Jewel of the Seas port damali gravid archipelago"
  # World lore: What empire is ruled by King Dwindle and what is its characteristic?
  "Dwindle Empire strength intelligence resources"
  # Campaign: Who is Mara's character and why was she on the cruise?
  "Menma Everdeen parents forced vacation"
)

# ------------------------------------------------------------------------------

command -v curl >/dev/null 2>&1 || { echo "error: 'curl' is required" >&2; exit 1; }
command -v jq   >/dev/null 2>&1 || { echo "error: 'jq' is required" >&2; exit 1; }

if [[ ${#QUERIES[@]} -eq 0 ]]; then
  echo "No queries configured. Add some to the QUERIES=( ... ) array in $0." >&2
  exit 1
fi

# embed_query <text> -> JSON array of floats on stdout
embed_query() {
  local text="$1"
  local auth=()
  [[ -n "$LLM_API_KEY" ]] && auth=(-H "Authorization: Bearer $LLM_API_KEY")
  curl -sf "${auth[@]}" -H "Content-Type: application/json" \
    -X POST "$LLM_URL/v1/embeddings" \
    --data "$(jq -n --arg m "$EMBED_MODEL" --arg in "$text" '{model: $m, input: $in}')" \
    | jq -c '.data[0].embedding'
}

# qdrant_query <vector-json> -> raw query response on stdout
qdrant_query() {
  local vector="$1"
  local auth=()
  [[ -n "$QDRANT_API_KEY" ]] && auth=(-H "api-key: $QDRANT_API_KEY")
  curl -sf "${auth[@]}" -H "Content-Type: application/json" \
    -X POST "$QDRANT_URL/collections/$QDRANT_COLLECTION/points/query" \
    --data "$(jq -n --argjson q "$vector" --argjson k "$TOP_K" \
      '{query: $q, using: "text", limit: $k, with_payload: true}')"
}

echo "model=$EMBED_MODEL  collection=$QDRANT_COLLECTION  top_k=$TOP_K"
echo

for q in "${QUERIES[@]}"; do
  echo "── query: $q"
  vector=$(embed_query "$q") || { echo "  embed failed" >&2; continue; }
  resp=$(qdrant_query "$vector") || { echo "  qdrant query failed" >&2; continue; }

  # Print "score  title — snippet" per hit. Title/text come from the payload;
  # the snippet is truncated so the table stays readable.
  echo "$resp" | jq -r '
    .result.points[]?
    | [ (.score | tostring | .[0:6]),
        (.payload.title // "(untitled)"),
        ((.payload.text // "") | gsub("\\s+"; " ") | .[0:80]) ]
    | "  " + (.[0]) + "  " + (.[1]) + " — " + (.[2])
  '
  echo
done
