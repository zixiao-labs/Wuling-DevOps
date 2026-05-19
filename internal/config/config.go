// Package config loads runtime configuration from environment variables.
//
// Precedence (lowest -> highest): defaults in struct tags, then env vars.
// Anything sensitive (DB DSN, JWT secret, OAuth client secret) MUST be supplied
// by the environment in production.
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is the top-level application config.
type Config struct {
	Env     string `env:"WULING_ENV" envDefault:"dev"`
	HTTP    HTTPConfig
	SSH     SSHConfig
	DB      DBConfig
	JWT     JWTConfig
	Storage StorageConfig
	OAuth   OAuthConfig
	Signup  SignupConfig
	Log     LogConfig
}

// HTTPConfig configures the public HTTP listener.
type HTTPConfig struct {
	Addr            string        `env:"WULING_HTTP_ADDR" envDefault:":8080"`
	ReadTimeout     time.Duration `env:"WULING_HTTP_READ_TIMEOUT" envDefault:"30s"`
	WriteTimeout    time.Duration `env:"WULING_HTTP_WRITE_TIMEOUT" envDefault:"5m"` // long for git-upload-pack
	IdleTimeout     time.Duration `env:"WULING_HTTP_IDLE_TIMEOUT" envDefault:"120s"`
	ShutdownTimeout time.Duration `env:"WULING_HTTP_SHUTDOWN_TIMEOUT" envDefault:"15s"`
	CORSOrigins     []string      `env:"WULING_HTTP_CORS_ORIGINS" envSeparator:"," envDefault:"*"`
	BasePath        string        `env:"WULING_HTTP_BASE_PATH" envDefault:""`
}

// defaultDBDSN mirrors the envDefault on DBConfig.DSN. Kept as a constant so
// the validator can detect "operator forgot to set the DSN in production"
// without duplicating the literal.
const defaultDBDSN = "postgres://wuling:wuling@localhost:5432/wuling?sslmode=disable"

// DBConfig configures the PostgreSQL connection pool.
type DBConfig struct {
	DSN             string        `env:"WULING_DB_DSN" envDefault:"postgres://wuling:wuling@localhost:5432/wuling?sslmode=disable"`
	MaxConns        int32         `env:"WULING_DB_MAX_CONNS" envDefault:"20"`
	MinConns        int32         `env:"WULING_DB_MIN_CONNS" envDefault:"2"`
	MaxConnIdleTime time.Duration `env:"WULING_DB_MAX_CONN_IDLE" envDefault:"5m"`
	MaxConnLifetime time.Duration `env:"WULING_DB_MAX_CONN_LIFETIME" envDefault:"1h"`
}

// JWTConfig configures access-token signing.
//
// Algorithm is HS256. Rotate Secret out-of-band; the issuer changes
// invalidate all existing tokens.
type JWTConfig struct {
	Secret   string        `env:"WULING_JWT_SECRET" envDefault:"change-me-in-production-please"`
	Issuer   string        `env:"WULING_JWT_ISSUER" envDefault:"wuling-devops"`
	Audience string        `env:"WULING_JWT_AUDIENCE" envDefault:"wuling-api"`
	TTL      time.Duration `env:"WULING_JWT_TTL" envDefault:"24h"`
}

// StorageConfig controls where bare repositories live on disk.
//
// RepoRoot must be writable by the API process. Repos are stored under
// "<RepoRoot>/<orgID>/<projectID>/<repoID>.git" — ID rather than name so
// renames don't move files.
type StorageConfig struct {
	RepoRoot string `env:"WULING_REPO_ROOT" envDefault:"./var/repos"`
}

// SSHConfig controls the embedded SSH server used for Git transport.
//
// HostKeyPath: if the file exists, it's loaded; otherwise an ed25519 key is
// generated on first boot and persisted with mode 0o600. This matches how
// the RepoRoot is treated — zero-config for dev, persistent across restarts,
// overridable by operators that pre-bake a key into a secret.
type SSHConfig struct {
	Enabled     bool   `env:"WULING_SSH_ENABLED" envDefault:"true"`
	Addr        string `env:"WULING_SSH_ADDR" envDefault:":2222"`
	HostKeyPath string `env:"WULING_SSH_HOST_KEY" envDefault:"./var/ssh/host_ed25519"`
}

// OAuthConfig is the GitHub OAuth client configuration plus the public URLs
// the OAuth flow uses to bounce the user between API and frontend.
//
//   - GithubClientID/Secret: from your GitHub OAuth app.
//   - GithubRedirectURL: the absolute URL of the callback handler, e.g.
//     "https://devops.example.com/api/v1/auth/oauth/github/callback".
//   - GithubScopes: comma-separated scope list requested at /authorize.
//   - FrontendBaseURL: where the API redirects the browser after a successful
//     OAuth callback (defaults to "/"). Should be an absolute URL in
//     production so we never trip same-origin assumptions in reverse-proxy
//     deployments.
//   - ProviderHMACSecret: server-held secret used to HMAC every OAuth token /
//     auth-code we issue from the provider role (the bytes themselves never
//     touch the DB; only their HMAC does). Must be set in production.
//   - PublicBaseURL: absolute origin the server advertises to OAuth clients in
//     /.well-known/wuling-clients and the token/authorize endpoints (e.g.
//     "https://wuling.zixiaolabs.com"). Defaults to empty, in which case the
//     handler falls back to deriving the origin from incoming Host headers —
//     fine for local dev, dangerous behind multi-host proxies.
//   - DesktopClientID: the public identifier used for the first-party desktop
//     app row that gets upserted on boot. Exposed via well-known so Esperanta
//     can dynamically discover it per-instance.
type OAuthConfig struct {
	GithubClientID     string `env:"WULING_OAUTH_GITHUB_CLIENT_ID"`
	GithubClientSecret string `env:"WULING_OAUTH_GITHUB_CLIENT_SECRET"`
	GithubRedirectURL  string `env:"WULING_OAUTH_GITHUB_REDIRECT_URL"`
	GithubScopes       string `env:"WULING_OAUTH_GITHUB_SCOPES" envDefault:"read:user,user:email"`
	FrontendBaseURL    string `env:"WULING_OAUTH_FRONTEND_BASE_URL" envDefault:"/"`
	ProviderHMACSecret string `env:"WULING_OAUTH_HMAC_SECRET"`
	PublicBaseURL      string `env:"WULING_OAUTH_PUBLIC_BASE_URL"`
	DesktopClientID    string `env:"WULING_OAUTH_DESKTOP_CLIENT_ID" envDefault:"wuling-desktop"`
}

// SignupConfig controls the new-account approval workflow.
//
// RequireApproval=true (the default) puts every new account into
// "pending" until an admin approves it; "approved" accounts can log in
// normally, "rejected" ones see a clear error.
//
// AutoApproveOAuth lets operators trust GitHub's identity assertion
// (i.e. anyone who can prove they own a linked GitHub account skips the
// approval queue). It's off by default to keep self-host installs closed.
type SignupConfig struct {
	RequireApproval  bool `env:"WULING_AUTH_REQUIRE_APPROVAL" envDefault:"true"`
	AutoApproveOAuth bool `env:"WULING_AUTH_OAUTH_AUTO_APPROVE" envDefault:"false"`
}

// LogConfig controls slog output.
type LogConfig struct {
	Level  string `env:"WULING_LOG_LEVEL" envDefault:"info"`  // debug|info|warn|error
	Format string `env:"WULING_LOG_FORMAT" envDefault:"text"` // text|json
}

// Load reads config from the environment.
func Load() (*Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return nil, fmt.Errorf("parse env: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	var problems []string

	if c.IsProd() && c.JWT.Secret == "change-me-in-production-please" {
		problems = append(problems, "WULING_JWT_SECRET must be set in production")
	}
	if c.JWT.Secret == "" {
		problems = append(problems, "WULING_JWT_SECRET must not be empty")
	}
	if c.JWT.TTL <= 0 {
		problems = append(problems, "WULING_JWT_TTL must be positive")
	}
	if c.DB.DSN == "" {
		problems = append(problems, "WULING_DB_DSN must not be empty")
	}
	// In production we additionally reject the built-in default DSN — pointing
	// a real deployment at the local-dev `wuling:wuling@localhost` is almost
	// certainly an operator mistake (and the credentials are public).
	if c.IsProd() && c.DB.DSN == defaultDBDSN {
		problems = append(problems, "WULING_DB_DSN must be set in production")
	}
	if c.Storage.RepoRoot == "" {
		problems = append(problems, "WULING_REPO_ROOT must not be empty")
	}
	if c.IsProd() && c.OAuth.ProviderHMACSecret == "" {
		problems = append(problems, "WULING_OAUTH_HMAC_SECRET must be set in production")
	}

	if len(problems) > 0 {
		return errors.New("invalid config: " + strings.Join(problems, "; "))
	}
	return nil
}

// IsProd reports whether the environment is "prod" or "production".
func (c *Config) IsProd() bool {
	switch strings.ToLower(c.Env) {
	case "prod", "production":
		return true
	}
	return false
}
