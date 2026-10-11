package config

import "testing"

func TestLoadValidConfiguration(t *testing.T) {
	t.Setenv("MYSQL_DSN", "user:pass@tcp(localhost:3306)/gateway")
	t.Setenv("API_KEY", "0123456789abcdef")
	t.Setenv("DASHBOARD_PASSWORD", "secret")
	t.Setenv("WEBHOOK_SECRET", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.WebhookSecret != cfg.APIKey {
		t.Fatalf("webhook secret should fall back to API key")
	}
	if cfg.WebhookMaxAttempts != 8 {
		t.Fatalf("WebhookMaxAttempts = %d", cfg.WebhookMaxAttempts)
	}
}

func TestLoadRejectsShortAPIKey(t *testing.T) {
	t.Setenv("MYSQL_DSN", "dsn")
	t.Setenv("API_KEY", "short")
	t.Setenv("DASHBOARD_PASSWORD", "secret")
	if _, err := Load(); err == nil {
		t.Fatal("Load() should reject a short API key")
	}
}
