package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/config"
)

func testJWTConfig(ttl time.Duration) config.JWTConfig {
	return config.JWTConfig{
		Secret:   "test-secret-do-not-use-in-prod",
		Issuer:   "wuling-test",
		Audience: "wuling-test-aud",
		TTL:      ttl,
	}
}

// ---------- argon2id password hashing ----------

func TestHashPassword_RoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$v=19$"), "hash should start with argon2id PHC marker, got %q", hash)
	ok, err := VerifyPassword("correct-horse-battery-staple", hash)
	require.NoError(t, err)
	assert.True(t, ok, "round-trip should match")
}

func TestVerifyPassword_WrongPassword(t *testing.T) {
	hash, err := HashPassword("hunter2")
	require.NoError(t, err)
	ok, err := VerifyPassword("hunter3", hash)
	require.NoError(t, err, "mismatch returns (false, nil), not error")
	assert.False(t, ok)
}

func TestVerifyPassword_MalformedHash(t *testing.T) {
	cases := []struct {
		name   string
		hash   string
		errMsg string
	}{
		{"empty", "", "invalid argon2id hash"},
		{"wrong segment count", "$argon2id$v=19$", "invalid argon2id hash"},
		{"wrong algo", "$argon2i$v=19$m=65536,t=3,p=2$YWFh$YmJi", "invalid argon2id hash"},
		{"bad version field", "$argon2id$nope$m=65536,t=3,p=2$YWFh$YmJi", "parse version"},
		{"unsupported version", "$argon2id$v=42$m=65536,t=3,p=2$YWFh$YmJi", "unsupported argon2 version"},
		{"bad params", "$argon2id$v=19$nope$YWFh$YmJi", "parse params"},
		{"bad salt b64", "$argon2id$v=19$m=65536,t=3,p=2$@@@@@@@@$YmJi", "decode salt"},
		{"bad hash b64", "$argon2id$v=19$m=65536,t=3,p=2$YWFh$@@@@@@@@", "decode hash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := VerifyPassword("any", tc.hash)
			assert.False(t, ok)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errMsg)
		})
	}
}

// ---------- access tokens (PATs) ----------

func TestNewAccessToken_FormatAndPrefix(t *testing.T) {
	raw, hashed, err := NewAccessToken()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(raw, AccessTokenPrefix), "raw must carry the wlpat_ prefix")
	// 32 random bytes -> 64 hex chars after the prefix.
	assert.Len(t, raw, len(AccessTokenPrefix)+64, "raw token must be prefix + 64 hex chars")
	assert.NotEqual(t, raw, hashed, "stored hash must not equal the raw token")
	assert.True(t, strings.HasPrefix(hashed, "$argon2id$"), "hashed token must be a PHC string")
}

func TestNewAccessToken_Unique(t *testing.T) {
	a, _, err := NewAccessToken()
	require.NoError(t, err)
	b, _, err := NewAccessToken()
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "successive tokens must differ")
}

func TestVerifyAccessToken_OK_Mismatch(t *testing.T) {
	raw, hashed, err := NewAccessToken()
	require.NoError(t, err)

	ok, err := VerifyAccessToken(raw, hashed)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = VerifyAccessToken(raw+"tamper", hashed)
	require.NoError(t, err)
	assert.False(t, ok)
}

// ---------- JWT issue / verify ----------

func TestIssuer_VerifierRoundTrip(t *testing.T) {
	cfg := testJWTConfig(time.Minute)
	issuer := NewIssuer(cfg)
	verifier := NewVerifier(cfg)

	uid := uuid.New()
	tok, exp, err := issuer.Issue(uid, "alice")
	require.NoError(t, err)
	assert.True(t, exp.After(time.Now()), "expiry should be in the future")

	claims, err := verifier.Verify(tok)
	require.NoError(t, err)
	assert.Equal(t, uid, claims.UserID)
	assert.Equal(t, "alice", claims.Username)
	assert.Equal(t, cfg.Issuer, claims.Issuer)
	require.Len(t, claims.Audience, 1)
	assert.Equal(t, cfg.Audience, claims.Audience[0])
}

func TestVerifier_Expired(t *testing.T) {
	cfg := testJWTConfig(-time.Second) // already expired at issue time
	tok, _, err := NewIssuer(cfg).Issue(uuid.New(), "u")
	require.NoError(t, err)
	_, err = NewVerifier(cfg).Verify(tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, jwt.ErrTokenExpired)
}

func TestVerifier_BadSignature(t *testing.T) {
	signCfg := testJWTConfig(time.Minute)
	tok, _, err := NewIssuer(signCfg).Issue(uuid.New(), "u")
	require.NoError(t, err)

	verifyCfg := signCfg
	verifyCfg.Secret = "different-secret"
	_, err = NewVerifier(verifyCfg).Verify(tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, jwt.ErrSignatureInvalid)
}

func TestVerifier_WrongIssuer(t *testing.T) {
	cfg := testJWTConfig(time.Minute)
	tok, _, err := NewIssuer(cfg).Issue(uuid.New(), "u")
	require.NoError(t, err)

	bad := cfg
	bad.Issuer = "someone-else"
	_, err = NewVerifier(bad).Verify(tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, jwt.ErrTokenInvalidIssuer)
}

func TestVerifier_WrongAudience(t *testing.T) {
	cfg := testJWTConfig(time.Minute)
	tok, _, err := NewIssuer(cfg).Issue(uuid.New(), "u")
	require.NoError(t, err)

	bad := cfg
	bad.Audience = "someone-else"
	_, err = NewVerifier(bad).Verify(tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, jwt.ErrTokenInvalidAudience)
}

func TestVerifier_AlgNone(t *testing.T) {
	// Hand-craft an alg=none token. WithValidMethods should reject it.
	claims := Claims{
		UserID:   uuid.New(),
		Username: "u",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "wuling-test",
			Audience:  jwt.ClaimStrings{"wuling-test-aud"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	unsafe := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := unsafe.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = NewVerifier(testJWTConfig(time.Minute)).Verify(signed)
	require.Error(t, err, "alg=none must be rejected")
	assert.ErrorIs(t, err, jwt.ErrTokenSignatureInvalid)
}

func TestVerifier_ZeroSubject(t *testing.T) {
	cfg := testJWTConfig(time.Minute)
	tok, _, err := NewIssuer(cfg).Issue(uuid.Nil, "u")
	require.NoError(t, err)
	_, err = NewVerifier(cfg).Verify(tok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no subject")
}
