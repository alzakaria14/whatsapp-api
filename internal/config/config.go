package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr               string
	MySQLDSN           string
	SessionDSN         string
	APIKey             string
	DashboardUser      string
	DashboardPassword  string
	WebhookSecret      string
	WebhookTimeout     time.Duration
	WebhookMaxAttempts int
	LogLevel           string
}

func Load() (Config, error) {
	timeout, err := time.ParseDuration(get("WEBHOOK_TIMEOUT", "10s"))
	if err != nil {
		return Config{}, errors.New("WEBHOOK_TIMEOUT tidak valid")
	}
	maxAttempts, err := strconv.Atoi(get("WEBHOOK_MAX_ATTEMPTS", "8"))
	if err != nil || maxAttempts < 1 {
		return Config{}, errors.New("WEBHOOK_MAX_ATTEMPTS harus bilangan positif")
	}
	cfg := Config{
		Addr:               get("APP_ADDR", ":8080"),
		MySQLDSN:           strings.TrimSpace(os.Getenv("MYSQL_DSN")),
		SessionDSN:         get("WHATSAPP_SESSION_DSN", "file:data/whatsapp.db?_foreign_keys=on&_busy_timeout=10000&_journal_mode=WAL"),
		APIKey:             strings.TrimSpace(os.Getenv("API_KEY")),
		DashboardUser:      get("DASHBOARD_USER", "admin"),
		DashboardPassword:  strings.TrimSpace(os.Getenv("DASHBOARD_PASSWORD")),
		WebhookSecret:      strings.TrimSpace(os.Getenv("WEBHOOK_SECRET")),
		WebhookTimeout:     timeout,
		WebhookMaxAttempts: maxAttempts,
		LogLevel:           get("LOG_LEVEL", "INFO"),
	}
	if cfg.MySQLDSN == "" {
		return Config{}, errors.New("MYSQL_DSN wajib diisi")
	}
	if len(cfg.APIKey) < 16 {
		return Config{}, errors.New("API_KEY wajib diisi minimal 16 karakter")
	}
	if cfg.DashboardPassword == "" {
		return Config{}, errors.New("DASHBOARD_PASSWORD wajib diisi")
	}
	if cfg.WebhookSecret == "" {
		cfg.WebhookSecret = cfg.APIKey
	}
	return cfg, nil
}

func get(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
