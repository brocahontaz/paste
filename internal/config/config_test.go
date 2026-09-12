package config

import (
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	// Ensure the env is clean for this test.
	for _, k := range []string{
		"PASTE_BIND_ADDR", "PASTE_BASE_URL", "PASTE_DB_PATH", "PASTE_MAX_BODY_BYTES",
		"PASTE_CREATE_RATE_PER_MIN", "PASTE_READ_RATE_PER_MIN", "PASTE_CLEANUP_INTERVAL",
		"PASTE_TRUST_PROXY",
	} {
		t.Setenv(k, "")
	}
	c := Load()
	if c.BindAddr != ":8080" {
		t.Errorf("BindAddr = %q", c.BindAddr)
	}
	if c.BaseURL != "" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	if c.DBPath != "data/paste.db" {
		t.Errorf("DBPath = %q", c.DBPath)
	}
	if c.MaxBodyBytes != 1<<20 {
		t.Errorf("MaxBodyBytes = %d", c.MaxBodyBytes)
	}
	if c.CreateRatePerMin != 5 || c.ReadRatePerMin != 60 {
		t.Errorf("rates = %d/%d", c.CreateRatePerMin, c.ReadRatePerMin)
	}
	if c.CleanupInterval != time.Minute {
		t.Errorf("CleanupInterval = %v", c.CleanupInterval)
	}
	if c.TrustProxy {
		t.Error("TrustProxy = true, want false")
	}
}

func TestFallbacksOnInvalidValues(t *testing.T) {
	t.Setenv("PASTE_CLEANUP_INTERVAL", "not-a-duration")
	t.Setenv("PASTE_CREATE_RATE_PER_MIN", "0") // would disable the limiter
	t.Setenv("PASTE_MAX_BODY_BYTES", "bogus")
	t.Setenv("PASTE_BIND_ADDR", "127.0.0.1:9999")
	t.Setenv("PASTE_TRUST_PROXY", "true")
	t.Setenv("PASTE_BASE_URL", "https://p.example.com/")

	c := Load()
	if c.CleanupInterval != time.Minute {
		t.Errorf("invalid duration did not fall back: %v", c.CleanupInterval)
	}
	if c.CreateRatePerMin != 5 {
		t.Errorf("non-positive rate did not fall back: %d", c.CreateRatePerMin)
	}
	if c.MaxBodyBytes != 1<<20 {
		t.Errorf("invalid body limit did not fall back: %d", c.MaxBodyBytes)
	}
	if c.BindAddr != "127.0.0.1:9999" {
		t.Errorf("BindAddr = %q", c.BindAddr)
	}
	if !c.TrustProxy {
		t.Error("TrustProxy = false, want true")
	}
	if c.BaseURL != "https://p.example.com" {
		t.Errorf("BaseURL trailing slash not trimmed: %q", c.BaseURL)
	}
}
