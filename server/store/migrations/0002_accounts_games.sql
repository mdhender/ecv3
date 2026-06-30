-- 0002_accounts_games: identity + per-game access schema.
--
-- Adds the application-identity table (accounts), the minimal games table, and
-- the game_accounts bridge that controls per-game access, the game role (GM vs
-- player), and the per-game display handle.
--
-- Enforcement split: single-row invariants are CHECK constraints; the
-- cross-table / state-transition fairness rules and the per-game handle default
-- are triggers. Who-may-call-what is the Go service layer (later issues).

-- accounts: application identities. (No handle here — handles are per-game.)
CREATE TABLE accounts (
    id            INTEGER PRIMARY KEY,
    email         TEXT    NOT NULL UNIQUE,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    is_active     INTEGER NOT NULL DEFAULT 1,
    hashed_secret TEXT    NOT NULL,            -- bcrypt hash, produced in Go

    -- email is stored lower-cased (the Go layer lower-cases; this verifies it)
    CHECK (email = lower(email) AND email <> ''),
    CHECK (is_admin  IN (0, 1)),
    CHECK (is_active IN (0, 1))
);

-- games: minimal for now (domain schema comes later).
CREATE TABLE games (
    id        INTEGER PRIMARY KEY,
    code      TEXT    NOT NULL UNIQUE,
    is_active INTEGER NOT NULL DEFAULT 1,

    CHECK (code <> ''),
    CHECK (is_active IN (0, 1))
);

-- game_accounts: bridge controlling per-game access, game role, and handle.
-- Composite PK => one row per (game, account): an account can never be assigned
-- to the same game twice. "Dropping" toggles is_active, never DELETE.
CREATE TABLE game_accounts (
    game_id    INTEGER NOT NULL REFERENCES games(id)    ON DELETE CASCADE,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    handle     TEXT,                       -- per-game name; NULL on insert => "player_N" default (trigger below)
    is_gm      INTEGER NOT NULL DEFAULT 0,
    is_active  INTEGER NOT NULL DEFAULT 1,

    PRIMARY KEY (game_id, account_id),
    UNIQUE (game_id, handle),
    -- handle pattern [a-z][a-z0-9._-]+ : a lowercase letter, then >= 1 more chars
    -- drawn only from [a-z0-9._-]. SQLite GLOB has no class quantifier (its '*'
    -- is a match-anything wildcard), so the rule is expressed as three parts:
    --   length(handle) >= 2          -- at least the leading letter + one more
    --   handle GLOB '[a-z]*'         -- first char is a lowercase letter
    --   handle NOT GLOB '*[^a-z0-9._-]*'  -- no char anywhere outside the set
    -- NULL passes here and is filled by the default trigger immediately after insert.
    CHECK (handle IS NULL OR (
        length(handle) >= 2
        AND handle GLOB '[a-z]*'
        AND handle NOT GLOB '*[^a-z0-9._-]*'
    )),
    CHECK (is_gm     IN (0, 1)),
    CHECK (is_active IN (0, 1))
);

-- Default handle: when none is supplied (handle IS NULL), assign "player_N"
-- where N = number of accounts in the game (GMs + players, active or not)
-- INCLUDING this just-inserted row. Rows are never deleted, so N is monotonic
-- and handles stay unique within the game. A supplied handle wins.
--   empty game             -> player_1
--   2 GMs + 3 players, +1   -> player_6
-- (player_N collision is a social/GM-handled matter — no auto-retry here.)
CREATE TRIGGER game_accounts_default_handle
AFTER INSERT ON game_accounts
FOR EACH ROW
WHEN NEW.handle IS NULL
BEGIN
    UPDATE game_accounts
       SET handle = 'player_' || (SELECT count(*) FROM game_accounts WHERE game_id = NEW.game_id)
     WHERE game_id = NEW.game_id AND account_id = NEW.account_id;
END;

-- An admin may never be a member (GM or player) of any game.
CREATE TRIGGER game_accounts_no_admin_insert
BEFORE INSERT ON game_accounts
FOR EACH ROW
WHEN (SELECT is_admin FROM accounts WHERE id = NEW.account_id) = 1
BEGIN
    SELECT RAISE(ABORT, 'admin accounts cannot be game members');
END;

-- ...and you can't promote an account to admin while it belongs to a game.
CREATE TRIGGER accounts_no_admin_with_membership
BEFORE UPDATE OF is_admin ON accounts
FOR EACH ROW
WHEN NEW.is_admin = 1 AND OLD.is_admin = 0
 AND EXISTS (SELECT 1 FROM game_accounts WHERE account_id = NEW.id)
BEGIN
    SELECT RAISE(ABORT, 'cannot grant admin to an account that belongs to a game');
END;

-- Once a GM, never back to player: block is_gm 1 -> 0.
-- (The rare admin override is intentionally NOT exempted at the DB layer; it is
-- deferred to the admin-service issue.)
CREATE TRIGGER game_accounts_no_gm_downgrade
BEFORE UPDATE OF is_gm ON game_accounts
FOR EACH ROW
WHEN OLD.is_gm = 1 AND NEW.is_gm = 0
BEGIN
    SELECT RAISE(ABORT, 'a GM cannot be downgraded to player');
END;
