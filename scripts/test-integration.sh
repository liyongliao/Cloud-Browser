#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
name="cb-test-postgres-$$"
cleanup(){ docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT
docker run -d --name "$name" -e POSTGRES_PASSWORD=test -e POSTGRES_DB=cloudbrowser -p 127.0.0.1::5432 postgres:17-bookworm >/dev/null
for i in {1..60}; do
 if docker exec "$name" pg_isready -U postgres >/dev/null 2>&1; then break; fi
 sleep 1
done
port=$(docker port "$name" 5432/tcp | sed 's/.*://')
TEST_DATABASE_URL="postgres://postgres:test@127.0.0.1:$port/cloudbrowser?sslmode=disable" go test -race ./internal/api -count=1 -v
