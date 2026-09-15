FROM golang:1.25-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api && CGO_ENABLED=0 go build -trimpath -o /out/runner ./cmd/runner && CGO_ENABLED=0 go build -trimpath -o /out/admin ./cmd/admin && CGO_ENABLED=0 go build -trimpath -o /out/setup ./cmd/setup
FROM node:22-bookworm-slim AS web-build
WORKDIR /src
COPY package.json package-lock.json ./
COPY web/package.json web/package.json
COPY extension/package.json extension/package.json
RUN npm ci
COPY web web
COPY api api
RUN npm run types && npm run build -w web
FROM caddy:2.10-alpine AS gateway
COPY --from=web-build /src/web/dist /srv/web
COPY deploy/Caddyfile /etc/caddy/Caddyfile
FROM debian:bookworm-slim AS api
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* && groupadd -g 10001 cloud && useradd -u 10001 -g 10001 cloud
COPY --from=go-build /out/api /out/admin /usr/local/bin/
USER 10001:10001
ENTRYPOINT ["api"]
FROM docker:28-cli AS runner
COPY --from=go-build /out/runner /usr/local/bin/runner
ENTRYPOINT ["runner"]
FROM docker:28-cli AS setup
RUN apk add --no-cache ca-certificates docker-cli-compose
COPY --from=go-build /out/setup /usr/local/bin/cloud-browser-setup
ENTRYPOINT ["cloud-browser-setup"]
