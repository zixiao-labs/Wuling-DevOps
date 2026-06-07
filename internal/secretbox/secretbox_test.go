package secretbox

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := New(GenerateKey())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	plain := []byte(`{"access_key_id":"AKID","access_key_secret":"shh"}`)
	ct, nonce, err := box.Seal(plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(ct, plain) {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := box.Open(ct, nonce)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip mismatch: got %q want %q", got, plain)
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	box, _ := New(GenerateKey())
	_, n1, _ := box.Seal([]byte("x"))
	_, n2, _ := box.Seal([]byte("x"))
	if bytes.Equal(n1, n2) {
		t.Fatal("nonce reused across Seal calls")
	}
}

func TestOpenRejectsTampered(t *testing.T) {
	box, _ := New(GenerateKey())
	ct, nonce, _ := box.Seal([]byte("secret"))
	ct[0] ^= 0xff // flip a bit
	if _, err := box.Open(ct, nonce); err == nil {
		t.Fatal("Open accepted tampered ciphertext")
	}
}

func TestNewRejectsBadKeyLen(t *testing.T) {
	if _, err := New([]byte("too-short")); err == nil {
		t.Fatal("New accepted short key")
	}
}

func TestParseKey(t *testing.T) {
	raw := GenerateKey()
	for name, enc := range map[string]string{
		"hex":        hex.EncodeToString(raw),
		"base64std":  base64.StdEncoding.EncodeToString(raw),
		"base64raw":  base64.RawStdEncoding.EncodeToString(raw),
		"base64url":  base64.URLEncoding.EncodeToString(raw),
		"base64urlr": base64.RawURLEncoding.EncodeToString(raw),
	} {
		got, err := ParseKey(enc)
		if err != nil {
			t.Fatalf("%s: ParseKey: %v", name, err)
		}
		if !bytes.Equal(got, raw) {
			t.Fatalf("%s: decoded key mismatch", name)
		}
	}
	if _, err := ParseKey("not-a-key"); err == nil {
		t.Fatal("ParseKey accepted garbage")
	}
	if _, err := ParseKey(hex.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("ParseKey accepted short hex")
	}
}
