-- 0001_init: establish the migration mechanism.
--
-- This is plumbing only — not the game's domain schema. schema_meta is a
-- minimal key/value table that proves apply-on-create, apply-on-open, and
-- idempotency. Real game tables arrive in later migrations.
CREATE TABLE schema_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO schema_meta (key, value) VALUES ('app', 'ecv3');
