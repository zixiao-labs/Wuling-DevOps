-- 0007_oauth: Wuling-DevOps becomes its own OAuth 2.0 Authorization Server.
--
-- Where 0001's `access_tokens` (PAT, `wlpat_` prefix) is a long-lived,
-- user-owned bearer with the full user scope, the tables here are for the
-- OAuth provider role: clients register, users grant per-app scoped consent,
-- and short-lived `wloat_`-prefixed access tokens are issued (with refresh
-- rotation + reuse detection). Authorization Code + PKCE and Device
-- Authorization Grant (RFC 8628) are the two supported flows.
--
-- Token hashing differs from PATs deliberately. PATs hash with argon2id
-- because they are dumped to disk in CI runners and the slow KDF blunts an
-- offline dump. OAT and refresh tokens are HMAC-SHA256'd with a server-held
-- secret instead: each request rehashes the bearer to look up the row, and
-- argon2 at 50ms/call would dominate latency. The server secret never leaves
-- the host, so an attacker who pops the DB still can't forge tokens.

-- ----------------------------------------------------------------------------
-- oauth_clients: a third-party app registration.
--
-- `client_secret_hash` is HMAC-SHA256 of the raw secret; NULL for public
-- clients (Esperanta and other desktop / SPA clients that can't keep a secret).
-- `redirect_uris` are matched by exact string equality at runtime, with the
-- single RFC 8252 §7.3 exception for `http://127.0.0.1` (any port permitted).
-- `is_first_party` is the gate the seed row toggles; user UIs use it to badge
-- "official" apps but it does NOT skip PKCE or first-time consent.
-- ----------------------------------------------------------------------------
CREATE TABLE oauth_clients (
    id                 UUID PRIMARY KEY,
    client_id          TEXT NOT NULL,                 -- public identifier (e.g. "wuling-desktop")
    client_secret_hash TEXT,                          -- HMAC-SHA256(secret); NULL for public clients
    name               TEXT NOT NULL,
    homepage_url       TEXT NOT NULL DEFAULT '',
    description        TEXT NOT NULL DEFAULT '',
    logo_url           TEXT NOT NULL DEFAULT '',
    owner_user_id      UUID REFERENCES users(id) ON DELETE CASCADE, -- NULL for first-party
    is_first_party     BOOLEAN NOT NULL DEFAULT FALSE,
    is_confidential    BOOLEAN NOT NULL DEFAULT TRUE, -- false => public client; client_secret_hash must be NULL
    redirect_uris      TEXT[] NOT NULL,
    default_scopes     TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX oauth_clients_client_id_uk ON oauth_clients (client_id);
CREATE INDEX oauth_clients_owner_idx ON oauth_clients (owner_user_id);

-- ----------------------------------------------------------------------------
-- oauth_authorizations: per-(user, client) durable consent.
--
-- A row here means "user has, at some point, granted client a set of scopes".
-- On a subsequent /authorize where requested ⊆ existing.scopes, consent is
-- silently reused. On requested ⊋ existing.scopes, the user re-consents and
-- this row is overwritten atomically (UPSERT) — never merged via append.
-- ----------------------------------------------------------------------------
CREATE TABLE oauth_authorizations (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_id   UUID NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    scopes      TEXT[] NOT NULL,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, client_id)
);
CREATE INDEX oauth_authorizations_user_idx ON oauth_authorizations (user_id);

-- ----------------------------------------------------------------------------
-- oauth_auth_requests: server-side hold area for an /authorize call.
--
-- The browser only ever sees a one-shot `req` id; client_id, scopes,
-- redirect_uri, state, and code_challenge live here so query-string tampering
-- cannot reshape the request. The session_cookie_hash binds the request to
-- the browser that initiated it (CSRF on the decision POST).
-- ----------------------------------------------------------------------------
CREATE TABLE oauth_auth_requests (
    id                    UUID PRIMARY KEY,
    client_id             UUID NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    redirect_uri          TEXT NOT NULL,
    scopes                TEXT[] NOT NULL,
    state                 TEXT NOT NULL,
    code_challenge        TEXT NOT NULL,
    code_challenge_method TEXT NOT NULL CHECK (code_challenge_method = 'S256'),
    session_cookie_hash   TEXT NOT NULL,        -- HMAC of the consent CSRF cookie
    user_id               UUID REFERENCES users(id) ON DELETE CASCADE,
    decision              TEXT,                 -- NULL while pending; 'allow' | 'deny' once decided
    expires_at            TIMESTAMPTZ NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX oauth_auth_requests_expires_idx ON oauth_auth_requests (expires_at);

-- ----------------------------------------------------------------------------
-- oauth_auth_codes: the short-lived authorization code itself.
--
-- The raw code is shown once in the redirect URL fragment, then HMAC-hashed
-- to row-lookup form. `used_at` flips it from valid to spent on a successful
-- /token exchange; subsequent attempts return invalid_grant. PKCE verifier
-- check happens against `code_challenge`.
-- ----------------------------------------------------------------------------
CREATE TABLE oauth_auth_codes (
    code_hash      TEXT PRIMARY KEY,             -- HMAC-SHA256(code)
    client_id      UUID NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_uri   TEXT NOT NULL,
    scopes         TEXT[] NOT NULL,
    code_challenge TEXT NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL,         -- ~10 min from issue
    used_at        TIMESTAMPTZ,                  -- single-use; non-NULL once exchanged
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX oauth_auth_codes_expires_idx ON oauth_auth_codes (expires_at);

-- ----------------------------------------------------------------------------
-- oauth_access_tokens: live access + refresh token pairs.
--
-- `refresh_chain_id` groups every refresh derived from the same root grant;
-- if a refresh hash is re-presented after rotation (parent_refresh_hash
-- collision), the auth server revokes the whole chain and writes an audit
-- event — RFC 6819 §5.2.2.3 reuse-detection.
-- ----------------------------------------------------------------------------
CREATE TABLE oauth_access_tokens (
    id                  UUID PRIMARY KEY,
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_id           UUID NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    token_hash          TEXT NOT NULL,            -- HMAC-SHA256(wloat_…)
    scopes              TEXT[] NOT NULL,
    expires_at          TIMESTAMPTZ NOT NULL,
    refresh_token_hash  TEXT,                     -- HMAC-SHA256(refresh_token); NULL = no refresh
    refresh_expires_at  TIMESTAMPTZ,
    refresh_chain_id    UUID,                     -- shared by every rotation of one root grant
    parent_refresh_hash TEXT,                     -- HMAC of the refresh that minted this row; NULL on root
    revoked_at          TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at        TIMESTAMPTZ
);
CREATE UNIQUE INDEX oauth_access_tokens_token_hash_uk
    ON oauth_access_tokens (token_hash);
CREATE UNIQUE INDEX oauth_access_tokens_refresh_hash_uk
    ON oauth_access_tokens (refresh_token_hash)
    WHERE refresh_token_hash IS NOT NULL;
CREATE INDEX oauth_access_tokens_user_live_idx
    ON oauth_access_tokens (user_id)
    WHERE revoked_at IS NULL;
CREATE INDEX oauth_access_tokens_chain_idx
    ON oauth_access_tokens (refresh_chain_id);

-- ----------------------------------------------------------------------------
-- oauth_device_codes: RFC 8628 device authorization state.
--
-- The device gets a `device_code` (long random secret) and shows the user a
-- `user_code` (short, easy to type). The auth server stores HMACs of both:
-- device_code_hash is the primary key for /token polling, while user_code is
-- looked up by the browser-side /oauth/device entry page (and compared
-- constant-time to defeat enumeration). status walks
-- pending -> approved | denied | expired; on approved we attach user_id.
-- ----------------------------------------------------------------------------
CREATE TABLE oauth_device_codes (
    device_code_hash TEXT PRIMARY KEY,            -- HMAC-SHA256(device_code)
    user_code        TEXT NOT NULL,               -- 8 chars base32, displayed to user
    client_id        UUID NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    scopes           TEXT[] NOT NULL,
    user_id          UUID REFERENCES users(id) ON DELETE CASCADE, -- filled on approve
    status           TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'approved', 'denied', 'expired')),
    interval_sec     INTEGER NOT NULL DEFAULT 5,
    last_polled_at   TIMESTAMPTZ,
    expires_at       TIMESTAMPTZ NOT NULL,        -- ~15 min from issue
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX oauth_device_codes_user_code_uk ON oauth_device_codes (user_code);
CREATE INDEX oauth_device_codes_expires_idx ON oauth_device_codes (expires_at);

-- ----------------------------------------------------------------------------
-- oauth_audit_log: append-only trail for token lifecycle events.
--
-- Token issue/refresh/revoke/reuse-detect events land here so operators can
-- forensics after a suspected compromise. Schema-light by design: `meta` is
-- a JSONB blob describing the event-specific fields.
-- ----------------------------------------------------------------------------
CREATE TABLE oauth_audit_log (
    id        BIGSERIAL PRIMARY KEY,
    ts        TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id   UUID,
    client_id UUID,
    event     TEXT NOT NULL,
    meta      JSONB NOT NULL DEFAULT '{}'::JSONB
);
CREATE INDEX oauth_audit_log_ts_idx ON oauth_audit_log (ts DESC);
CREATE INDEX oauth_audit_log_user_idx ON oauth_audit_log (user_id, ts DESC);
CREATE INDEX oauth_audit_log_client_idx ON oauth_audit_log (client_id, ts DESC);
