-- 0007_oauth (down). Drop child tables first so FK constraints don't block.

DROP TABLE IF EXISTS oauth_audit_log;
DROP TABLE IF EXISTS oauth_device_codes;
DROP TABLE IF EXISTS oauth_access_tokens;
DROP TABLE IF EXISTS oauth_auth_codes;
DROP TABLE IF EXISTS oauth_auth_requests;
DROP TABLE IF EXISTS oauth_authorizations;
DROP TABLE IF EXISTS oauth_clients;
