# ecv3 API contract

The canonical contract between the Go backend and the Ember SPA. This document is
the human-readable source of truth; keep it in step with:

- **`server/api/dto.go`** — the Go request/response structs whose `json` tags
  define the exact wire field names.
- **`server/api/*_test.go`** — `httptest` cases that assert these shapes, so code
  and doc cannot silently drift.

Update all three together in the same change.

## Conventions

- **Base path:** all endpoints are under `/api`.
- **Auth:** session is an HttpOnly, Secure, SameSite=Lax cookie
  (`__Host-ecv3_session`) set by login and cleared by logout. The SPA never sees
  the token; send requests with `credentials: 'include'`.
- **CSRF:** enforced by the stdlib `http.CrossOriginProtection` (Sec-Fetch-Site /
  Origin checks). Unsafe-method requests (POST/DELETE/…) must be same-origin; a
  cross-origin one gets **403**. Safe methods (GET/HEAD) are unaffected.
- **Format split (hybrid):**
  - Auth **actions** (login, logout) use **plain JSON**, camelCase keys.
  - **Resources** the WarpDrive cache consumes use **JSON:API**
    (`application/vnd.api+json`), dasherized attribute keys.
- **Errors:**
  - plain-JSON endpoints: `{ "error": "message" }`.
  - JSON:API endpoints: `{ "errors": [ { "status": "401", "title": "…" } ] }`.

## Endpoints

### `POST /api/login`

Plain JSON. Verifies credentials and starts a session (sets the cookie).

Request (`application/json`):

```json
{ "email": "a@example.com", "password": "secret" }
```

| Status | When | Body |
|--------|------|------|
| `200` | success (+ `Set-Cookie`) | `{ "user": { "id": 42, "email": "a@example.com", "isAdmin": false } }` |
| `400` | missing/malformed body | `{ "error": "email and password are required" }` |
| `401` | unknown email, wrong password, **or** inactive account | `{ "error": "invalid email or password" }` |

The `401` is deliberately uniform across all three failures — the response never
reveals whether an email exists (login also runs a constant-time bcrypt compare
on unknown emails to keep timing flat).

### `DELETE /api/session`

Logout. Destroys the current session and clears the cookie. Idempotent.

| Status | When |
|--------|------|
| `204` | always (whether or not a session existed) |

### `GET /api/me`

JSON:API (`application/vnd.api+json`). Returns the authenticated user, used by
the SPA on boot (ember-simple-auth `restore()`) and for current-user state.
Reports the **effective** identity — the impersonated account when an admin is
acting as someone.

| Status | When | Body |
|--------|------|------|
| `200` | authenticated | see below |
| `401` | no / invalid / expired session | `{ "errors": [ { "status": "401", "title": "not authenticated" } ] }` |

`200` body:

```json
{
  "data": {
    "type": "users",
    "id": "42",
    "attributes": { "email": "a@example.com", "is-admin": false }
  },
  "meta": { "impersonating": false }
}
```

When an admin is impersonating, `data` is the impersonated (effective) account
and `meta` carries the admin's own id so the SPA can show an "acting as" banner:

```json
{ "meta": { "impersonating": true, "real-account-id": "1" } }
```

### `GET /api/healthz`

Liveness/readiness of the data layer. Plain JSON.

| Status | Body |
|--------|------|
| `200` | `{"status":"ok"}` |
| `503` | `{"status":"unavailable"}` |

## Not yet implemented

Tracked separately: the Ember client (ember-simple-auth custom authenticator,
`session` service, login route, route guards), login rate-limiting, invalidating
live sessions when an account is deactivated mid-session, impersonation
start/stop endpoints, and password reset/change.
