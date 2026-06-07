// Package secretbox seals and opens secret values with AES-256-GCM under a
// single server-held key (WULING_SECRETS_KEY). GCM gives us confidentiality
// plus integrity, so a tampered ciphertext fails to open rather than
// decrypting to garbage. The key never touches the database; only the
// ciphertext and a per-value random nonce are persisted.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// KeyLen is the required key length: AES-256.
const KeyLen = 32

// Box seals/opens with a fixed key.
type Box struct{ gcm cipher.AEAD }

// New builds a Box from a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != KeyLen {
		return nil, fmt.Errorf("secretbox: key must be %d bytes, got %d", KeyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{gcm: gcm}, nil
}

// Seal encrypts plaintext, returning (ciphertext, nonce). The nonce is freshly
// random on every call — reusing a nonce under the same key would be a
// catastrophic GCM failure, so we never derive it deterministically.
func (b *Box) Seal(plaintext []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, b.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = b.gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Open reverses Seal. A modified ciphertext or nonce yields an error.
func (b *Box) Open(ciphertext, nonce []byte) ([]byte, error) {
	if len(nonce) != b.gcm.NonceSize() {
		return nil, errors.New("secretbox: bad nonce length")
	}
	return b.gcm.Open(nil, nonce, ciphertext, nil)
}

// ParseKey decodes a 32-byte key supplied as hex (64 chars) or base64 (std or
// url, padded or raw). Anything that doesn't decode to exactly 32 bytes is
// rejected so a too-short key can't silently weaken encryption.
func ParseKey(s string) ([]byte, error) {
	if len(s) == 2*KeyLen {
		if b, err := hex.DecodeString(s); err == nil {
			return b, nil
		}
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == KeyLen {
			return b, nil
		}
	}
	return nil, fmt.Errorf("secretbox: key must be %d bytes as hex (%d chars) or base64", KeyLen, 2*KeyLen)
}

// GenerateKey returns a fresh random key. Used for the dev auto-generated
// ephemeral key (lost on restart, which is fine for local dev).
func GenerateKey() []byte {
	k := make([]byte, KeyLen)
	if _, err := rand.Read(k); err != nil {
		panic("secretbox: rand.Read: " + err.Error())
	}
	return k
}
