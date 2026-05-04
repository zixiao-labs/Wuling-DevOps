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
	DB      DBConfig
	JWT     JWTConfig
	Storage StorageConfig
	OAuth   OAuthConfig
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

// OAuthConfig is the GitHub OAuth client configuration. Endpoint is wired
// but issuance/callbacks are stubbed in Stage 1.
type OAuthConfig struct {
	GithubClientID     string `env:"WULING_OAUTH_GITHUB_CLIENT_ID"`
	GithubClientSecret string `env:"WULING_OAUTH_GITHUB_CLIENT_SECRET"`
	GithubRedirectURL  string `env:"WULING_OAUTH_GITHUB_REDIRECT_URL"`
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
	if c.Storage.RepoRoot == "" {
		problems = append(problems, "WULING_REPO_ROOT must not be empty")
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
