# ecv3 build orchestration.
#
# The production binary embeds the built Ember SPA (server/web/dist), so the
# SPA must be built and copied into the embed dir BEFORE `go build`. `make build`
# enforces that order. server/web/dist holds only a committed .gitignore until
# the SPA is built; generated assets there are ignored.

BINARY      := ec
EMBED_DIR   := server/web/dist
CLIENT_DIST := client/dist

# Files in the embed dir that must survive a clean (committed VCS placeholders).
KEEP        := ! -name .gitignore

.PHONY: all build spa embed server test test-go test-client clean dev tools

all: build

## build: build the SPA, embed it, and compile the binary (production artifact)
build: embed
	go build -o $(BINARY) ./cmd/ec

## spa: build the Ember SPA into client/dist
spa:
	cd client && pnpm install --frozen-lockfile && pnpm build

## embed: copy the built SPA into the Go embed dir
embed: spa
	find $(EMBED_DIR) -mindepth 1 $(KEEP) -delete
	cp -R $(CLIENT_DIST)/. $(EMBED_DIR)/

## server: compile the binary only (assumes the SPA is already embedded)
server:
	go build -o $(BINARY) ./cmd/ec

## test: run Go and client test suites
test: test-go test-client

test-go:
	go test ./...

test-client:
	cd client && pnpm test

## clean: remove the binary and all generated build output
clean:
	rm -f $(BINARY)
	find $(EMBED_DIR) -mindepth 1 $(KEEP) -delete
	rm -rf $(CLIENT_DIST)

## dev: print the development workflow (air + ember; global Caddy proxies)
dev:
	@echo "ecv3 dev runs behind your global Caddy at https://ecv3.localhost:8443"
	@echo "One-time: paste the dist/Caddyfile fragment into /opt/homebrew/etc/Caddyfile, then:"
	@echo "  caddy reload --config /opt/homebrew/etc/Caddyfile --adapter caddyfile"
	@echo "Then run these in separate terminals:"
	@echo "  1. air                       # rebuild/restart the Go API on :25634"
	@echo "  2. cd client && pnpm start   # Ember dev server (Vite) on :4201, hot reload"
