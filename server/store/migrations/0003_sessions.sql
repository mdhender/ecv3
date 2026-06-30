-- 0003_sessions: server-side login sessions.
--
-- One row per active login. The cookie carries a random opaque token; we store
-- only its SHA-256 (id_hash), so a leaked database cannot be replayed as live
-- cookies. Sessions are opaque (not JWT): revocation is a DELETE, and SSE
-- authenticates via this same cookie (EventSource cannot send headers).
--
-- There is deliberately NO csrf_token column: CSRF is handled at the edge by the
-- stdlib http.CrossOriginProtection middleware (Sec-Fetch-Site / Origin checks),
-- not by per-session tokens.
--
-- Timestamps are unix epoch SECONDS (INTEGER). The Go layer owns the clock and
-- token generation; the store never reads the clock (it is passed in), matching
-- the rest of server/store.
--
-- Enforcement split (same as 0002): structural single-row invariants are CHECK
-- constraints here; "who may do what" (e.g. only admins may impersonate, and
-- only a non-admin may be impersonated) stays in the Go service layer.
CREATE TABLE sessions (
    -- SHA-256 (hex) of the random cookie token. PK => one row per token, and
    -- the lookup index. The raw token exists only in the client's cookie.
    id_hash    TEXT    PRIMARY KEY,

    -- The authenticated account. Deleting the account drops its sessions.
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,

    -- "Admin acting as": when set, requests resolve their EFFECTIVE identity to
    -- this account while audit/ownership stays with account_id. NULL = not
    -- impersonating. If the target account is deleted we revert to acting as
    -- self (SET NULL) rather than dropping the admin's whole session.
    impersonated_account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,

    -- The game the session is currently "in", used to resolve relative links.
    -- NULL = not in a game; a deleted game clears the pointer (SET NULL).
    current_game_id INTEGER REFERENCES games(id) ON DELETE SET NULL,

    created_at   INTEGER NOT NULL,           -- unix seconds, set once at login
    expires_at   INTEGER NOT NULL,           -- unix seconds, slides forward on use
    last_seen_at INTEGER NOT NULL,           -- unix seconds, updated on use

    -- Client fingerprint at last refresh: audit, "active devices", and optional
    -- IP binding (enforced or ignored per the serve --session-bind-ip flag).
    ip         TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',

    -- You cannot impersonate yourself.
    CHECK (impersonated_account_id IS NULL OR impersonated_account_id <> account_id),
    CHECK (expires_at   >= created_at),
    CHECK (last_seen_at >= created_at)
);

-- Revoke-all / list-a-user's-sessions, and admin "active sessions" views.
CREATE INDEX sessions_account_id ON sessions (account_id);

-- Expired-session cleanup sweeps (DELETE ... WHERE expires_at < ?).
CREATE INDEX sessions_expires_at ON sessions (expires_at);
