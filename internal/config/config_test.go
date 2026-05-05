package config

import (
	"os"
	"testing"
)

func TestNewConfig(t *testing.T) {
	_ = os.Setenv("SERVER_HOST", "127.0.0.1")
	_ = os.Setenv("SERVER_PORT", "8080")
	_ = os.Setenv("POSTGRES_HOST", "localhost")
	_ = os.Setenv("POSTGRES_PORT", "5432")
	_ = os.Setenv("POSTGRES_USER", "postgres")
	_ = os.Setenv("POSTGRES_DB", "ticket_db")
	_ = os.Setenv("POSTGRES_PASSWORD", "postgres")
	_ = os.Setenv("POSTGRES_SSLMODE", "disable")
	_ = os.Setenv("POSTGRES_MAX_OPEN_CONNS", "10")
	_ = os.Setenv("POSTGRES_MAX_IDLE_CONNS", "5")
	_ = os.Setenv("POSTGRES_CONN_MAX_LIFETIME", "60")
	_ = os.Setenv("LOG_LEVEL", "debug")

	cfg := NewConfig()
	if cfg.Server.Port != 8080 || cfg.Postgres.Host != "localhost" || cfg.Logger.Level != "debug" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}
