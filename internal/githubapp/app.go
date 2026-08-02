// Package githubapp authenticates as a GitHub App and mints installation
// access tokens. Private key material never appears in logs.
package githubapp

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const apiBase = "https://api.github.com"

// Client talks to the GitHub App API.
type Client struct {
	AppID      int64
	PrivateKey *rsa.PrivateKey
	HTTP       *http.Client

	mu     sync.Mutex
	tokens map[int64]cachedToken
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// LoadPrivateKey parses a PEM RSA private key from bytes or a file path.
func LoadPrivateKey(pemBytes, path string) (*rsa.PrivateKey, error) {
	var raw []byte
	switch {
	case strings.TrimSpace(pemBytes) != "":
		raw = []byte(pemBytes)
	case strings.TrimSpace(path) != "":
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read github app private key: %w", err)
		}
		raw = b
	default:
		return nil, fmt.Errorf("github app private key not configured")
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("github app private key: no PEM block")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github app private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("github app private key: not RSA")
	}
	return key, nil
}

// New builds a Client. httpClient may be nil.
func New(appID int64, key *rsa.PrivateKey, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		AppID:      appID,
		PrivateKey: key,
		HTTP:       httpClient,
		tokens:     map[int64]cachedToken{},
	}
}

// AppJWT mints a short-lived RS256 JWT (iss = App ID, exp ≤ 10m).
func (c *Client) AppJWT() (string, error) {
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": c.AppID,
	}
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return t.SignedString(c.PrivateKey)
}

// InstallationToken returns a cached installation access token.
func (c *Client) InstallationToken(installationID int64) (string, error) {
	c.mu.Lock()
	if tok, ok := c.tokens[installationID]; ok && time.Now().Before(tok.expiresAt.Add(-5*time.Minute)) {
		c.mu.Unlock()
		return tok.token, nil
	}
	c.mu.Unlock()

	jwtStr, err := c.AppJWT()
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", apiBase, installationID)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		return "", fmt.Errorf("installation token: HTTP %d", res.StatusCode)
	}
	var parsed struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.tokens[installationID] = cachedToken{token: parsed.Token, expiresAt: parsed.ExpiresAt}
	c.mu.Unlock()
	return parsed.Token, nil
}

// CloneURL builds an authenticated HTTPS clone URL for a repository.
func CloneURL(token, owner, name string) string {
	return fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, owner, name)
}
