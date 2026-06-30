# EMBER-MCP.md

How the **Ember MCP server** is wired into this project, how to enable Claude to use it, and how to refresh it.

## What this is

[`ember-mcp`](https://github.com/ember-tooling/ember-mcp) is an MCP server that gives Claude live access to current Ember.js documentation, API references, guides, and best practices. We use it so generated Ember code follows **modern Polaris/v7** idioms instead of Claude's classic-Ember defaults (see `CLAUDE.md`).

| Thing | Value |
|---|---|
| Local clone | `/Users/wraith/Software/ember-mcp/ember-mcp` |
| Upstream | `https://github.com/ember-tooling/ember-mcp` (branch `main`) |
| Entry point | `index.js` (stdio MCP server) |
| Package manager | **pnpm** (pinned via `packageManager`); run through **corepack** (pnpm isn't on PATH) |
| Node floor | Node 22+ (you have v22.x) |
| Registered in | `.mcp.json` at this repo's root, server name **`ember`** |

`.mcp.json` already contains:
```json
{
  "mcpServers": {
    "ember": {
      "command": "node",
      "args": ["/Users/wraith/Software/ember-mcp/ember-mcp/index.js"]
    }
  }
}
```

## Enable Claude to use the Ember MCP

Project-scoped MCP servers must be approved before they load.

1. **Approve the server.** In Claude Code, run `/mcp`. Find `ember` and approve/trust it.
   - If it isn't listed, **restart Claude Code** in this directory (`/Users/wraith/Software/mdhender/syntax`) so it re-reads `.mcp.json`, then run `/mcp` and approve.
2. **Verify it's connected.** Run `/mcp` again — `ember` should show **connected** and list tools such as `search_ember_docs`.
3. **Confirm it works.** Ask Claude something like *"use the ember MCP to look up the current Glimmer component + `<template>` tag syntax"* and check it calls an `ember`/`search_ember_docs` tool.

> Note: the MCP runs `node /Users/.../ember-mcp/index.js` directly against the local clone, so its behavior reflects whatever is currently checked out there — keep it refreshed (below).

## Refresh the MCP server

Run when you want the latest upstream docs/tooling. After refreshing, the running MCP must be restarted to pick up changes.

```bash
# 1. Update the source
cd /Users/wraith/Software/ember-mcp/ember-mcp
git checkout main
git pull --ff-only origin main

# 2. Reinstall dependencies against the (possibly updated) lockfile.
#    pnpm isn't on PATH, so go through corepack:
corepack enable pnpm        # one-time: sets up the pnpm shim
pnpm install                # honors pnpm-lock.yaml + the tar override
#    (or, without enabling globally:)
# corepack pnpm install

# 3. Sanity-check it boots (prints a startup line, then waits on stdio — Ctrl+C to exit)
node index.js
```

> Fallback only if corepack/pnpm is unavailable: `npm install` will install deps but **ignores** `pnpm-lock.yaml` and the pnpm `overrides` (e.g. the pinned `tar`), so prefer pnpm via corepack.

### Apply the refresh in Claude

The MCP process is launched by Claude Code, so after updating the files:

- **Reload MCP servers:** run `/mcp` and reconnect/restart the `ember` server, **or** restart Claude Code in this directory.
- Re-verify with `/mcp` (status `connected`).

## Troubleshooting

- **`ember` not listed in `/mcp`:** ensure `.mcp.json` exists at the repo root and you launched Claude Code from this directory; restart it.
- **Server shows error/disconnected:** run `node /Users/wraith/Software/ember-mcp/ember-mcp/index.js` manually — it should print `Ember Docs MCP Server running on stdio`. If it errors, re-run the dependency install step. (It exits immediately when run by hand because no MCP client is attached to stdin — that's normal.)
- **Wrong Node:** confirm `node --version` is 22+.
- **Want a zero-clone setup instead:** point `.mcp.json` at the published package — `"command": "npx", "args": ["-y", "ember-mcp"]` — which always fetches a release (network required, no local refresh needed).
