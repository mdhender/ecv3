# `ec` — the ecv3 binary

`ec` is the single static binary for ecv3: it serves the JSON API + SSE and the
embedded Ember SPA, and carries the operational tooling (database setup, admin
bootstrap, dev seeding). It is an [`ff/v4`](https://github.com/peterbourgon/ff)
command tree.

```
ec <SUBCOMMAND> [FLAGS]
```

Run `ec` with no subcommand (or any command with `-h`/`--help`) to print usage.

## Conventions

- **Flags precede positional arguments.** Parsing is stdlib-style: the first
  non-flag argument stops flag parsing, so `ec database seed --dev <file>` works
  but `ec database seed <file> --dev` does not.
- **The database is always `<data-dir>/ecv3.db`.** Commands never create the data
  directory; it must already exist.
- **Secrets never come from flags.** Passwords are read from a TTY prompt, an
  environment variable, or generated — never a `--password` flag (shell history).

## Common flags

| Flag         | Default                   | Notes                                                                          |
|--------------|---------------------------|--------------------------------------------------------------------------------|
| `--data DIR` | `$ECV3_DATA`, else `data` | Directory holding `ecv3.db`. Used by `serve`, `admin create`, `database seed`. |
| `--port N`   | `$PORT`, else `8080`      | `serve` only.                                                                  |

`database create` is the exception: it takes the directory as a positional
`<path>` argument rather than `--data`.

## Commands

### `ec serve`

```
ec serve [--port N] [--data DIR] [--session-bind-ip BOOL] [--trusted-proxies CIDRs]
```

Run the HTTP server (API + embedded SPA). Opens `<data>/ecv3.db` and applies any
pending migrations before listening; it never creates the database (a missing one
is a fast, clear failure). Shuts down gracefully on `SIGINT`/`SIGTERM`.

| Flag                      | Default                | Notes                                                                                    |
|---------------------------|------------------------|------------------------------------------------------------------------------------------|
| `--port N`                | `$PORT`, else `8080`   | TCP port to listen on.                                                                    |
| `--data DIR`              | `$ECV3_DATA`, else `data` | Directory holding `ecv3.db`.                                                           |
| `--session-bind-ip BOOL`  | `true`                 | Bind each session to its origin IP; re-authenticate on IP change.                        |
| `--trusted-proxies CIDRs` | `127.0.0.0/8,::1/128`  | Comma-separated CIDRs of reverse proxies whose `X-Forwarded-For` is honored.             |

```sh
ec serve --port 8080 --data ./data
```

#### Sessions & security

Authentication uses opaque, server-side sessions in an `HttpOnly`, `Secure`,
`SameSite=Lax` cookie (the cookie carries a random token; only its SHA-256 is
stored, so a leaked database cannot be replayed as live cookies). CSRF is handled
at the edge by the stdlib `http.CrossOriginProtection` middleware, and any
direct-TLS path is pinned to **TLS 1.3**. We do not support older browsers.

**`--session-bind-ip`** (default `true`) ties a session to the client IP it was
created with: a request from a different IP is treated as unauthenticated and the
user must log in again. Disable it for clients whose IP rotates mid-session — a
VPN that changes exit nodes, mobile network hand-offs — so they are not nagged to
re-authenticate:

```sh
ec serve --session-bind-ip=false
```

The IP is always *recorded* (audit / "active devices"); the flag gates only
*enforcement*.

**`--trusted-proxies`** controls how the client IP is determined behind a reverse
proxy, and is what makes IP binding trustworthy:

- The `X-Forwarded-For` header is honored **only** when the request's direct peer
  is within one of these CIDRs. From any other source the header is
  attacker-controlled and ignored — the direct peer IP is used instead.
- When the peer is trusted, `X-Forwarded-For` is scanned **right-to-left** and the
  first non-trusted address is taken as the real client (correct through a chain
  of proxies, and unspoofable: an attacker can only prepend left-hand entries the
  scan never reaches).

The default trusts loopback, which is correct for the same-host Caddy setup (dev
and prod). Adjust it to match your topology:

```sh
# Caddy (or another trusted proxy) on a separate host
ec serve --trusted-proxies=10.0.0.0/8

# No proxy — the binary faces clients directly; never trust the header
ec serve --trusted-proxies=""
```

> ⚠️ **IP binding is only as trustworthy as `--trusted-proxies`.** If a real proxy
> is missing from the list, the recorded IP becomes the proxy's address (binding
> still works, just coarsely). If an *untrusted* source is wrongly added, clients
> behind it can spoof their IP via `X-Forwarded-For` and defeat binding.

### `ec database create`

```
ec database create <path>
```

Create a new `ecv3.db` under the existing directory `<path>` and apply all
migrations. Fails if `<path>` is missing, is not a directory, or already contains
`ecv3.db` (it never clobbers).

```sh
ec database create ./data
```

### `ec database seed`

```
ec database seed --dev [--data DIR] <file>
```

Load accounts, games, and memberships from a JSON seed file into an existing
database. **Dev/test only:** the `--dev` flag is a required guard so the command
can never quietly run against production, and the file path is always explicit.
The whole file is applied in one transaction — a bad row rolls the batch back, so
you can fix the file and re-run.

Seed-file format (see [`testdata/dev-seed.json`](../../testdata/dev-seed.json)):

```json
{
  "accounts": [
    { "email": "admin@example.com", "password": "change-me", "is_admin": true },
    { "email": "alice@example.com", "password": "change-me" }
  ],
  "games": [
    { "code": "demo" }
  ],
  "game_accounts": [
    { "game": "demo", "email": "alice@example.com", "handle": "alice", "is_gm": false }
  ]
}
```

- `accounts[].password` is plaintext in the file and bcrypt-hashed on load.
- `game_accounts` reference accounts and games by `email` / `game` code; an
  account with an admin entry can never be a game member (the schema rejects it).
- An empty/omitted `handle` lets the schema assign the default `player_N`.

```sh
ec database seed --dev --data ./data testdata/dev-seed.json
```

### `ec admin create`

```
ec admin create --email <addr> [--generate] [--seed N] [--data DIR]
```

Create an administrator account in an existing database. This is deliberate and
separate from `database create`, so it can be re-run to recover a locked-out
admin. Re-running with an email that already exists fails clearly.

**Password source** (where the new admin's password comes from):

1. `--generate` — generate a random passphrase and print it **once** (explicit
   override; takes precedence when set).
2. otherwise, an interactive TTY prompt (no echo, entered twice to confirm).
3. otherwise, the `ECV3_ADMIN_PASSWORD` environment variable (for
   non-interactive provisioning).

| Flag             | Required | Notes                                                                      |
|------------------|----------|----------------------------------------------------------------------------|
| `--email <addr>` | yes      | Lower-cased before storage.                                                |
| `--generate`     | no       | Generate and print a 5-word, `.`-separated passphrase.                     |
| `--seed N`       | no       | Pin the passphrase generator for a reproducible result. `--generate` only. |
| `--data DIR`     | no       | Database directory (see common flags).                                     |

`--seed N` expands the single `uint64` into the generator's two PCG seeds with
SplitMix64 (reproducible, for testing). Without `--seed`, the seeds are drawn from
`crypto/rand`, so generated passwords are unpredictable.

```sh
# interactive prompt
ec admin create --email me@example.com

# non-interactive provisioning
ECV3_ADMIN_PASSWORD=… ec admin create --email me@example.com

# generate and print a password once
ec admin create --email me@example.com --generate

# reproducible password (testing)
ec admin create --email me@example.com --generate --seed 42
```

### `ec version`

```
ec version
```

Print the core version (`major.minor.patch`).

## Environment variables

| Variable              | Used by                                  | Effect                                  |
|-----------------------|------------------------------------------|-----------------------------------------|
| `ECV3_ADMIN_PASSWORD` | `admin create`                           | Non-interactive password source.        |
| `ECV3_DATA`           | `serve`, `admin create`, `database seed` | Default `--data` directory.             |
| `PORT`                | `serve`                                  | Default `--port` (set by `air` in dev). |
