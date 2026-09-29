-- Initial schema. Times are stored as Unix seconds (UTC).

CREATE TABLE users (
    id                   INTEGER PRIMARY KEY,
    username             TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    display_name         TEXT    NOT NULL,
    role                 TEXT    NOT NULL CHECK (role IN ('admin', 'user')),
    password_hash        TEXT    NOT NULL DEFAULT '',
    -- Set while the user holds a temporary password from the admin.
    must_change_password INTEGER NOT NULL DEFAULT 0,
    temp_password_expires_at INTEGER,
    active               INTEGER NOT NULL DEFAULT 1,
    -- 'builtin' accounts log in here; 'proxy' accounts are named by another proxy.
    source               TEXT    NOT NULL DEFAULT 'builtin',
    created_at           INTEGER NOT NULL
);

-- Sessions and tokens are stored only as SHA-256 hashes.
CREATE TABLE sessions (
    id           INTEGER PRIMARY KEY,
    token_hash   TEXT    NOT NULL UNIQUE,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
);

CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    token_hash   TEXT    NOT NULL UNIQUE,
    -- The first characters of the token, so people can tell their tokens apart.
    hint         TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER,
    last_used_at INTEGER,
    revoked_at   INTEGER
);
CREATE INDEX api_tokens_user ON api_tokens(user_id);

-- One row per request. Metadata only: never prompts or responses.
-- user_name and token_name are snapshots, so rows stay attributable after deletions.
-- Token counts are NULL when unknown, never zero.
CREATE TABLE usage_events (
    id                    INTEGER PRIMARY KEY,
    request_id            TEXT    NOT NULL UNIQUE,
    created_at            INTEGER NOT NULL,
    user_id               INTEGER,
    user_name             TEXT    NOT NULL DEFAULT '',
    token_id              INTEGER,
    token_name            TEXT    NOT NULL DEFAULT '',
    route                 TEXT    NOT NULL DEFAULT '',
    model                 TEXT    NOT NULL DEFAULT '',
    target                TEXT    NOT NULL DEFAULT '',
    tier                  TEXT    NOT NULL DEFAULT '',
    prompt_tokens         INTEGER,
    cached_tokens         INTEGER,
    cache_creation_tokens INTEGER,
    completion_tokens     INTEGER,
    reasoning_tokens      INTEGER,
    -- Tokens spent by classifier or judge calls while routing this request.
    routing_tokens        INTEGER,
    latency_ms            INTEGER,
    status                INTEGER,
    error                 TEXT    NOT NULL DEFAULT '',
    -- Sum of the priced parts of this request. cost_unknown is set when a model it used
    -- has no price, and then the cost is shown as unknown, never as a partial amount.
    cost                  REAL,
    cost_unknown          INTEGER NOT NULL DEFAULT 0,
    currency              TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX usage_events_created ON usage_events(created_at);
CREATE INDEX usage_events_user_created ON usage_events(user_name, created_at);
CREATE INDEX usage_events_token ON usage_events(token_id);

-- Monthly per-user totals, kept after per-request rows expire.
CREATE TABLE usage_monthly (
    month             TEXT    NOT NULL,
    user_name         TEXT    NOT NULL,
    requests          INTEGER NOT NULL DEFAULT 0,
    errors            INTEGER NOT NULL DEFAULT 0,
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    cached_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    cost              REAL,
    PRIMARY KEY (month, user_name)
);

-- Prices per target, per million tokens. A target without a row has no price.
CREATE TABLE prices (
    target      TEXT PRIMARY KEY,
    currency    TEXT NOT NULL,
    input       REAL NOT NULL,
    cached      REAL NOT NULL,
    cache_write REAL NOT NULL,
    output      REAL NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY,
    created_at INTEGER NOT NULL,
    actor      TEXT    NOT NULL,
    ip         TEXT    NOT NULL DEFAULT '',
    action     TEXT    NOT NULL,
    detail     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_created ON audit_log(created_at);

-- Small named values: the deployment model, the HTTPS state, log offsets.
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
