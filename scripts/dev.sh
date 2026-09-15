#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
name="cb-dev-postgres-$$"
api_pid=''; web_pid=''
cleanup(){ [[ -z $api_pid ]] || kill "$api_pid" 2>/dev/null || true; [[ -z $web_pid ]] || kill "$web_pid" 2>/dev/null || true; docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM
docker run -d --name "$name" -e POSTGRES_PASSWORD=local-dev-only -e POSTGRES_DB=cloudbrowser -p 127.0.0.1::5432 postgres:17-bookworm >/dev/null
for i in {1..60}; do docker exec "$name" pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done
port=$(docker port "$name" 5432/tcp | sed 's/.*://')
export DATABASE_URL="postgres://postgres:local-dev-only@127.0.0.1:$port/cloudbrowser?sslmode=disable"
export PUBLIC_ORIGIN=http://127.0.0.1:5188 LISTEN=127.0.0.1:8188
# Disposable database only; never used by production bootstrap.
ADMIN_PASSWORD=local-preview-password go run ./cmd/admin create preview@example.test
mkdir -p bin
go build -o bin/api ./cmd/api
bin/api & api_pid=$!
./node_modules/.bin/vite --config web/vite.config.ts web --host 127.0.0.1 & web_pid=$!
echo 'Local control-plane preview: http://127.0.0.1:5188 · preview@example.test / local-preview-password'
echo 'Browser start requires the Linux Runner. This preview has no simulated browser session.'
wait
