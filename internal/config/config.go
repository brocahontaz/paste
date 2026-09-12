// Package config loads service configuration from environment variables.
//
// Missing or invalid values fall back to safe defaults, so a malformed
// environment can never make the service unusable or insecure. No domains,
// secrets, or other deployment-specific values are hardcoded.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for the paste service.
type Config struct {
	BindAddr         string        // address to listen on
	BaseURL          string        // optional absolute base URL, e.g. https://p.example.com
	DBPath           string        // SQLite database file path
	MaxBodyBytes     int64         // maximum accepted paste (and form) size
	CreateRatePerMin int           // allowed paste creations per client IP per minute
	ReadRatePerMin   int           // allowed paste reads per client IP per minute
	CleanupInterval  time.Duration // how often the background purge loop deletes expired pastes
	TrustProxy       bool          // honor X-Forwarded-For / X-Forwarded-Proto when deriving client IP and URLs
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	return Config{
		BindAddr:         envString("PASTE_BIND_ADDR", ":8080"),
		BaseURL:          strings.TrimRight(envString("PASTE_BASE_URL", ""), "/"),
		DBPath:           envString("PASTE_DB_PATH", "data/paste.db"),
		MaxBodyBytes:     envInt64("PASTE_MAX_BODY_BYTES", 1<<20),
		CreateRatePerMin: envInt("PASTE_CREATE_RATE_PER_MIN", 5),
		ReadRatePerMin:   envInt("PASTE_READ_RATE_PER_MIN", 60),
		CleanupInterval:  envDuration("PASTE_CLEANUP_INTERVAL", time.Minute),
		TrustProxy:       envBool("PASTE_TRUST_PROXY", false),
	}
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v < 1 {
		// A non-positive rate would disable the limiter entirely; fall back
		// to the default instead.
		return def
	}
	return v
}

func envInt64(key string, def int64) int64 {
	v, err := strconv.ParseInt(os.Getenv(key), 10, 64)
	if err != nil || v < 1 {
		// A non-positive body limit would reject every request; fall back.
		return def
	}
	return v
}

func envDuration(key string, def time.Duration) time.Duration {
	v, err := time.ParseDuration(os.Getenv(key))
	if err != nil || v <= 0 {
		return def
	}
	return v
}

func envBool(key string, def bool) bool {
	v, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return def
	}
	return v
}
