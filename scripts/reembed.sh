#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8000}"
PAGE_SIZE=200
total=0
page_token=""

echo "Re-queuing all embed jobs for re-embedding..."

while true; do
  url="$BASE_URL/api/v1/jobs?stages=embed&page_size=$PAGE_SIZE"
  [[ -n "$page_token" ]] && url="$url&page_token=$page_token"

  response=$(curl -sf "$url")
  job_ids=$(echo "$response" | jq -r '.data[].id')
  next=$(echo "$response" | jq -r '.next_page_token // empty')

  for job_id in $job_ids; do
    status=$(curl -sf "$BASE_URL/api/v1/jobs/$job_id" | jq -r '.status')
    if [[ "$status" == "running" ]]; then
      echo "  SKIP $job_id (running)"
      continue
    fi
    curl -sf -X PUT "$BASE_URL/api/v1/jobs/$job_id/status" \
      -H 'Content-Type: application/json' \
      -d '{"status":"pending"}' > /dev/null
    echo "  queued $job_id (was $status)"
    ((total++)) || true
  done

  [[ -z "$next" ]] && break
  page_token="$next"
done

echo "Done. Queued $total embed jobs."
