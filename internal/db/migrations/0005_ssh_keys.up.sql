-- 0005_ssh_keys: registered SSH public keys for the embedded sshd.
--
-- One row per (user, key). Fingerprints are stored as the canonical SHA256
-- form ssh-keygen prints (e.g. "SHA256:abc..."), so the sshd's
-- PublicKeyHandler can match an incoming key in one indexed lookup. We make
-- fingerprint globally unique because a key reused across accounts is
-- ambiguous on auth — if Alice and Bob both register the same key, neither
-- should be allowed to sneak in as the other.

CREATE TABLE user_ssh_keys (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title        TEXT NOT NULL CHECK (LENGTH(title) >= 1),
    fingerprint  TEXT NOT NULL UNIQUE,
    public_key   TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);
CREATE INDEX user_ssh_keys_user_idx ON user_ssh_keys (user_id);
