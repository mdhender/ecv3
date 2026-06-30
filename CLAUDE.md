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
/server/            # Go module: API, SQLite, SSE, auth
  main.go
  web/dist/         # built Ember app, go:embed'd (build output; gitignore or commit per preference)
/client/            # Ember v7 app (npm/pnpm toolchain — build-time only)
/src/               # LEGACY Next.js reference (docs template) — do not extend
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

- Tailwind UI Kit HTML components are available at ~/Software/tailwind/application-ui/html.
- Refer to ELEMENTS.md for information on integrating with the HTML components

## How Claude should work in this repo

1. **For any Ember API, syntax, version, or migration question, use the `ember` MCP first** (`search_ember_docs`, API lookups, version info). Fall back to `WebFetch`/`WebSearch` of the official guides. Treat memory as suspect for Ember specifics.
2. **Pin versions.** Record exact Go, Ember, and key package versions here once chosen; prefer the latest stable confirmed via MCP/npm.
3. Keep the **API contract** between Go and Ember explicit and in one place; update both sides together.
4. Don't run a dev server or build unless asked; when building, build Ember → `dist`, embed, then `go build`.

## Versions (to confirm — fill in via ember-mcp / `go version`)

- Go: `__` (target latest stable; needs 1.22+ for pattern routing)
- Ember: `__` (v7 / latest Polaris — verify via ember-mcp)
- SQLite driver: `modernc.org/sqlite` `__`
- Node (build/CI only): `__` (ember-mcp itself needs Node 22+)

## Commands (to fill in as the project takes shape)

- Build SPA: `__`
- Build binary: `__`
- Run server: `__`
- Test (Go): `go test ./...`
- Test (Ember): `cd client && ember test`
