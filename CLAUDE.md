# CLAUDE.md

Guidance for Claude when working in this repository.

## What this project is

A **4X strategy, turn-based web game** (orders submitted per turn, validated server-side, feedback returned in the response).
It is **not** real-time in the game-engine sense; interactivity is order entry plus light push notifications (SSE "toast" messages for the GM/admin during game setup and the order-editing cycle).

## Architecture decision (settled)

**Go backend + Ember v7 SPA frontend, shipped as one static Go binary.**

| Layer      | Choice                                                                                  | Why                                                                                                                                 |
|------------|-----------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------|
| Backend    | **Go** (JSON API + SSE), **SQLite**                                                     | Single static binary, memory-safe, tiny RAM/disk footprint, trivial to patch (recompile). Owner loves Go for backend work.          |
| Frontend   | **Ember v7** (Polaris edition) SPA                                                      | Owner is productive and happy in Ember; avoids Go-templated frontends (HTMX/templ) which the owner explicitly dislikes.             |
| Deployment | Go binary **`go:embed`s the built Ember `dist/`** and serves it at `/`, API at `/api/*` | One artifact to deploy. No Node in production. TLS via Caddy or Go `autocert` if needed.                                            |
| Auth       | **HttpOnly secure cookie sessions**                                                     | Simpler than JWT and required for SSE — `EventSource` cannot send `Authorization` headers, so it authenticates via the same cookie. |

### Why this shape (constraints that drove it)

- **No Node in production.** Prod = Go binary + SQLite file. Ember's npm toolchain runs only at build/CI, never on the server.
- **Tiny/cheap droplet** (DigitalOcean 1GB RAM / 25GB SSD). Go (~10–30MB RAM) + SQLite + static assets idles comfortably; bursty Friday-night traffic (<100 users) is trivial for goroutines.
- **Owner enjoyment / longevity.** Go for the half the owner loves, Ember for the half the owner enjoys, and zero of the Go-frontend work the owner hates.

## Proposed repo layout

```
go.mod              # module github.com/mdhender/ecv3 (rooted at repo root)
/cmd/ec/main.go     # binary entrypoint (package main); builds to `ec`
/server/            # Go backend packages: API, SQLite, SSE, auth
  web/dist/         # built Ember app, go:embed'd (build output; gitignore or commit per preference)
/client/            # Ember v7 app (npm/pnpm toolchain — build-time only)
```

## Go backend conventions

- **Routing:** stdlib `net/http` `ServeMux` with Go 1.22+ method+pattern routes (e.g. `mux.HandleFunc("POST /api/orders/{id}", ...)`). Keep the dependency surface small.
- **SQLite driver:** **`zombiezen.com/go/sqlite`** (pure Go, **no CGO**) so the binary stays fully static and cross-compiles from macOS → Linux. Use `zombiezen.com/go/sqlite/sqlitemigration` for migrations.
- **SSE:** plain `net/http` with `http.Flusher`; `Content-Type: text/event-stream`. Auth via session cookie (works with `EventSource`).
- **Embedding the SPA:** `//go:embed all:web/dist` + a handler that serves static files and falls back to `index.html` for client-side routes.
- **Commands** `github.com/peterbourgon/ff/v4` - ff.Command
- **Config:** `github.com/peterbourgon/ff/v4` - ff.FlagSet; env vars / flags; no secrets in the binary or repo.
- **Style:** `gofmt`; standard project idioms; table-driven tests.

## Ember frontend conventions — MODERN (Polaris/v7) ONLY

> ⚠️ Claude's Ember training skews toward *classic* Ember.
> The rules below are mandatory.
> When unsure about a current API, **consult the `ember` MCP server (ember-mcp) or fetch the current guides — do not answer from memory.**
> Verify version-specific details rather than asserting them.

**Use:**

- **First-class component templates**: `.gjs` / `.gts` with `<template>...</template>` and **strict mode** (import components/helpers/modifiers into scope explicitly).
- **Glimmer components**: `import Component from '@glimmer/component'`; access args via `this.args`.
- **Reactive state**: `@tracked` from `@glimmer/tracking` (and `tracked-built-ins` for tracked arrays/objects/maps).
- **Services**: `import { service } from '@ember/service'`.
- **Events**: `{{on "click" this.handler}}` with plain methods/functions.
- **Helpers/modifiers**: plain functions; `ember-modifier` for modifiers.
- **Build**: Vite + Embroider (`@embroider/vite`), modern `ember-cli` Polaris blueprint.
- **Types** (if `.gts`): Glint.
- **Data**: prefer lightweight native `fetch` against the Go API, or **WarpDrive/EmberData (modern)** — credentialed requests for cookie auth (`fetch(url, { credentials: 'include' })`).
- **Testing**: `qunit` + `@ember/test-helpers`, `ember test`.

**Do NOT use (Ember classic patterns to avoid):**

- `.hbs` + separate `.js` component pairs as the default (use `.gjs`/`.gts`).
- `Ember.Object`, `.set()` / `.get()`, `EmberObject.extend()`, mixins.
- `@ember/object` computed properties (`computed(...)`) or observers.
- `{{action}}` helper, `{{mut}}`, two-way `mut` bindings.
- jQuery / `Ember.$`, prototype extensions (array `.pushObject`, etc.).
- Legacy `DS.Model` / classic adapter-serializer assumptions without checking current WarpDrive guidance.

## Tailwind UI Kit HTML components

**Build the front end by composing it from the Tailwind UI Kit, not by hand-rolling markup.** When building any UI, first search the kit for a component that fits, then port it into Ember.

- **Component library:** `~/Software/tailwind/application-ui/html` — a categorized library of static HTML component markup. Browse/grep it by category before writing new UI: `application-shells`, `data-display`, `elements`, `feedback`, `forms`, `headings`, `layout`, `lists`, `navigation`, `overlays`, `page-examples`. Pick the closest snippet as the starting point.
- **Interactivity:** **Tailwind Plus Elements** (`@tailwindplus/elements`) provides the JS behavior for the interactive snippets (dialog, select, tabs, dropdown menu, popover, command palette, autocomplete, disclosure, copy button). It's framework-agnostic (custom elements), so it works inside Ember templates. See `ELEMENTS.md` for the component list and install/usage details.
- **Workflow:** find the snippet in the html library → port its markup into a `.gjs`/`.gts` component (strict mode) → wire any interactive behavior with Tailwind Plus Elements rather than reimplementing it. Default to these building blocks; only write bespoke markup when the kit has no suitable component.

## How Claude should work in this repo

1. **For any Ember API, syntax, version, or migration question, use the `ember` MCP first** (`search_ember_docs`, API lookups, version info). Fall back to `WebFetch`/`WebSearch` of the official guides. Treat memory as suspect for Ember specifics.
2. **Pin versions.** Record exact Go, Ember, and key package versions here once chosen; prefer the latest stable confirmed via MCP/npm.
3. Keep the **API contract** between Go and Ember explicit and in one place; update both sides together.
4. Don't run a dev server or build unless asked; when building, build Ember → `dist`, embed, then `go build`.

## Versions (to confirm — fill in via ember-mcp / `go version`)

- Go: `1.26.4` (per `go.mod`; needs 1.22+ for pattern routing)
- Ember: `ember-source ~7.0.0`, `ember-cli ~7.0.0` (scaffolded with CLI 7.0.1). App is **TypeScript (.gts + Glint)**, strict mode.
- Build system: **Vite 8 + Embroider** (`@embroider/vite ^1.7.2`); type-check via `ember-tsc`. TypeScript `^5.9.3`.
- Data layer: **WarpDrive** (`@warp-drive/core` + `@warp-drive/ember` `~5.8.2`); store at `client/app/services/store.ts`.
- CLI/config: `github.com/peterbourgon/ff/v4` `v4.0.0-beta.1` (ff.Command tree in `cmd/ec`).
- Versioning: `github.com/maloquacious/semver` `v0.4.0`; version lives in root `version.go` (`ecv3.Version()`).
- SQLite driver: `zombiezen.com/go/sqlite` (+ `zombiezen.com/go/sqlite/sqlitemigration`) `__` (pin once added to `go.mod`)
- Node (build/CI only): `22.x` (currently `22.22.2`). Minimum `>= 20.19.0` for ember-cli 7.0.1; ember-mcp needs Node 22+.
- Package manager (client): **pnpm** `11.9.0` (via corepack).

## Commands

Production build is orchestrated by the **Makefile** (SPA must be built and
embedded before `go build`):

- `make build` — build the Ember SPA, copy it into `server/web/dist`, then compile `ec` (the production binary)
- `make spa` / `make embed` — build the SPA / copy it into the embed dir
- `make server` — compile the binary only (assumes the SPA is already embedded)
- `make test` — `go test ./...` + `cd client && pnpm test`
- `make clean` — remove the binary and generated build output (preserves the embed dir's committed `.gitignore`)

The binary (`ec`) is an `ff.Command` tree:

- `ec serve [--port N]` — run the HTTP server (API + embedded SPA). Port defaults to `$PORT` then `8080`. (`go run ./cmd/ec serve`)
- `ec version` — print the core version (`major.minor.patch`) from `ecv3.Version().Core()`
- `ec` (no subcommand) — print help

Client-only commands:

- Install deps: `cd client && pnpm install`
- Build SPA: `cd client && pnpm build` → `client/dist/`
- Lint / type-check: `cd client && pnpm lint` / `pnpm lint:types`

How the embed works: `server/web/web.go` does `//go:embed all:dist` and serves
the SPA with an index.html fallback for client-side routes. Until the SPA is
built, `server/web/dist` holds only its committed `.gitignore`, so `go build`
still compiles and the server returns a "run make build" notice at `/`.

## Development workflow

Dev is fronted by the developer's **global, brew-managed Caddy**
(`/opt/homebrew/etc/Caddyfile`), which multiplexes all local projects on one
instance via `*.localhost` subdomains (global `http_port 8080` / `https_port
8443`). ecv3 is the `ecv3.localhost` site; a single TLS origin makes dev mirror
prod (HttpOnly+Secure cookies and SSE both require same-origin). **In dev the
SPA is NOT embedded** — air rebuilds only the Go API; Ember serves `/` itself.

```
Browser ─> https://ecv3.localhost:8443 (global Caddy, tls internal)
             ├── /api/*  ─> Go API   (air, :25634)  — SSE-friendly (flush_interval -1)
             └── /*      ─> Ember/Vite (:4201)      — HMR over wss
```

One-time setup: paste the two blocks from `./Caddyfile` (a fragment, not a
standalone config) into `/opt/homebrew/etc/Caddyfile`, then reload:
`caddy reload --config /opt/homebrew/etc/Caddyfile --adapter caddyfile`.

Then run in separate terminals (`make dev` prints this):

1. `air` — rebuilds/restarts the Go API on `:25634` (config: `.air.toml`; watches `*.go`, ignores `client/` and `server/web/dist/`).
2. `cd client && pnpm start` — Ember dev server (Vite) on `:4201` with hot reload (port + HMR set for `ecv3.localhost:8443` in `client/vite.config.mjs`).

Backend ports (`:25634` Go, `:4201` Ember) are chosen to not collide with the
other projects in the global Caddyfile. The production binary still defaults to
`:8080` (overridden in dev by `PORT` in `.air.toml`).

Tools needed for dev (not required to build): `air` (`go install github.com/air-verse/air@latest`); Caddy (already running as a brew service).
