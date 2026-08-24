package config

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/caarlos0/env/v11"
)

func validPipelineCacheTestConfig() *Config {
	return &Config{
		Env:     "dev",
		JWT:     JWTConfig{Secret: "test-secret", TTL: time.Hour},
		DB:      DBConfig{DSN: "postgres://test:test@localhost/test"},
		Storage: StorageConfig{RepoRoot: "./repos", AvatarsDir: "./avatars"},
		Pipeline: PipelineConfig{
			LogDir: "./logs", CacheMaxUploadBytes: 512 * 1024 * 1024,
			CacheTTL: 14 * 24 * time.Hour, CacheGCInterval: 15 * time.Minute,
		},
		Artifacts: ArtifactServiceConfig{
			BaseURL: "http://localhost:8090", MaxUploadBytes: 1024,
			ConnectTimeout: time.Second, ResponseHeaderTimeout: time.Second, RequestTimeout: time.Minute,
		},
	}
}

func TestPipelineCacheConfigDefaults(t *testing.T) {
	for _, key := range []string{
		"WULING_PIPELINE_LOG_DIR",
		"WULING_PIPELINE_CACHE_MAX_UPLOAD_BYTES",
		"WULING_PIPELINE_CACHE_TTL",
		"WULING_PIPELINE_CACHE_GC_INTERVAL",
	} {
		value, exists := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}

	var cfg PipelineConfig
	if err := env.Parse(&cfg); err != nil {
		t.Fatalf("parse defaults: %v", err)
	}
	if cfg.CacheMaxUploadBytes != 512*1024*1024 {
		t.Fatalf("CacheMaxUploadBytes = %d, want 512 MiB", cfg.CacheMaxUploadBytes)
	}
	if cfg.CacheTTL != 14*24*time.Hour {
		t.Fatalf("CacheTTL = %s, want 14 days", cfg.CacheTTL)
	}
	if cfg.CacheGCInterval != 15*time.Minute {
		t.Fatalf("CacheGCInterval = %s, want 15m", cfg.CacheGCInterval)
	}
}

func TestPipelineCacheConfigValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		set  func(*Config)
		want string
	}{
		{name: "max upload", set: func(c *Config) { c.Pipeline.CacheMaxUploadBytes = 0 }, want: "WULING_PIPELINE_CACHE_MAX_UPLOAD_BYTES"},
		{name: "ttl", set: func(c *Config) { c.Pipeline.CacheTTL = 0 }, want: "WULING_PIPELINE_CACHE_TTL"},
		{name: "gc interval", set: func(c *Config) { c.Pipeline.CacheGCInterval = 0 }, want: "WULING_PIPELINE_CACHE_GC_INTERVAL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := validPipelineCacheTestConfig()
			test.set(cfg)
			err := cfg.validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validate error = %v, want %s", err, test.want)
			}
		})
	}
}
