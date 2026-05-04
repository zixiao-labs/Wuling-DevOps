// Package auth handles password hashing, JWT issue/verify, and HTTP middleware.
//
// Two credential types live here:
//
//   - Password: hashed with argon2id (PHC string). Used at login.
//   - Access token (PAT): random 32-byte secret, hashed with argon2id, used as
//     HTTP Basic auth password by Git CLI and other long-lived clients.
//
// Once login or PAT exchange succeeds, callers receive a JWT (HS256) that
// embeds the user id and expires per JWTConfig.TTL. Internal services should
// always verify via the Verifier; never decode a JWT manually.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"

	"github.com/zixiao-labs/wuling-devops/internal/config"
)

// ----------------------------------------------------------------------------
// argon2id password hashing
// ----------------------------------------------------------------------------

// argon2idParams defaults tuned for ~50ms on a modern CPU. Increase Memory
// when hardware allows; never decrease without a migration plan.
type argon2idParams struct {
	Memory     uint32
	Iterations uint32
	Threads    uint8
	SaltLen    uint32
	KeyLen     uint32
}

var defaultArgonParams = argon2idParams{
	Memory:     64 * 1024, // 64 MiB
	Iterations: 3,
	Threads:    2,
	SaltLen:    16,
	KeyLen:     32,
}

// HashPassword returns a PHC-formatted argon2id hash.
//
//	$argon2id$v=19$m=65536,t=3,p=2$<saltb64>$<hashb64>
func HashPassword(plain string) (string, error) {
	return hashWithParams(plain, defaultArgonParams)
}

func hashWithParams(plain string, p argon2idParams) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(plain), salt, p.Iterations, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword compares plain text against a PHC-formatted hash. It returns
// (true, nil) on match, (false, nil) on mismatch, or (false, err) if the hash
// is malformed.
func VerifyPassword(plain, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// $ argon2id $ v=19 $ m=...,t=...,p=... $ salt $ hash -> 6 parts after leading empty
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("invalid argon2id hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("parse version: %w", err)
	}
	if version != argon2.Version {
		return false, fmt.Errorf("unsupported argon2 version %d", version)
	}
	var p argon2idParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Threads); err != nil {
		return false, fmt.Errorf("parse params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode salt: %w", err)
	}
	wantHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode hash: %w", err)
	}
	gotHash := argon2.IDKey([]byte(plain), salt, p.Iterations, p.Memory, p.Threads, uint32(len(wantHash)))
	if subtle.ConstantTimeCompare(gotHash, wantHash) == 1 {
		return true, nil
	}
	return false, nil
}

// ----------------------------------------------------------------------------
// access tokens (PATs)
// ----------------------------------------------------------------------------

// AccessTokenPrefix prefixes raw PATs so leaked secrets can be scanned for.
const AccessTokenPrefix = "wlpat_"

// NewAccessToken returns (raw, hashed). Store hashed; show raw to the user once.
func NewAccessToken() (raw, hashed string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = AccessTokenPrefix + hex.EncodeToString(buf)
	hashed, err = HashPassword(raw)
	if err != nil {
		return "", "", err
	}
	return raw, hashed, nil
}

// VerifyAccessToken constant-time compares raw vs an argon2id hash.
func VerifyAccessToken(raw, hashed string) (bool, error) {
	return VerifyPassword(raw, hashed)
}

// ----------------------------------------------------------------------------
// JWT issue / verify
// ----------------------------------------------------------------------------

// Claims is the strongly-typed claim set we embed in every access token.
type Claims struct {
	UserID   uuid.UUID `json:"sub"`
	Username string    `json:"username"`
	jwt.RegisteredClaims
}

// GetSubject overrides RegisteredClaims so we can stuff a uuid in `sub`.
func (c Claims) GetSubject() (string, error) { return c.UserID.String(), nil }

// Issuer issues access tokens.
type Issuer struct {
	cfg config.JWTConfig
}

// NewIssuer returns an Issuer bound to cfg.
func NewIssuer(cfg config.JWTConfig) *Issuer { return &Issuer{cfg: cfg} }

// Issue mints a JWT for the given user.
func (i *Issuer) Issue(userID uuid.UUID, username string) (string, time.Time, error) {
	now := time.Now().UTC()
	exp := now.Add(i.cfg.TTL)
	claims := Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    i.cfg.Issuer,
			Audience:  jwt.ClaimStrings{i.cfg.Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.NewString(),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(i.cfg.Secret))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign jwt: %w", err)
	}
	return signed, exp, nil
}

// Verifier verifies access tokens.
type Verifier struct {
	cfg config.JWTConfig
}

// NewVerifier returns a Verifier bound to cfg.
func NewVerifier(cfg config.JWTConfig) *Verifier { return &Verifier{cfg: cfg} }

// Verify parses and validates a token string.
func (v *Verifier) Verify(token string) (*Claims, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(v.cfg.Issuer),
		jwt.WithAudience(v.cfg.Audience),
	)
	claims := &Claims{}
	_, err := parser.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		return []byte(v.cfg.Secret), nil
	})
	if err != nil {
		return nil, err
	}
	if claims.UserID == uuid.Nil {
		return nil, errors.New("token has no subject")
	}
	return claims, nil
}
