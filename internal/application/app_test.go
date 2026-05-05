package application

import (
	"booking_api/internal/config"
	"booking_api/internal/infrastructure/database"
	"booking_api/internal/infrastructure/logger"
	"booking_api/internal/server"
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNewAppErrorPaths(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
	}{
		{
			name: "invalid logger level",
			cfg: config.Config{
				Logger: logger.Config{Level: "invalid"},
			},
		},
		{
			name: "missing db host",
			cfg: config.Config{
				Logger:   logger.Config{Level: "info"},
				Server:   server.ServerConfig{Host: "127.0.0.1", Port: 0},
				Postgres: database.PostgresConfig{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewApp(tt.cfg)
			if err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestNewAppSuccessAndLifecycle(t *testing.T) {
	origLogger := newLoggerFn
	origHTTP := newHTTPServerFn
	origConnect := connectPostgresFn
	t.Cleanup(func() {
		newLoggerFn = origLogger
		newHTTPServerFn = origHTTP
		connectPostgresFn = origConnect
	})

	newLoggerFn = func(cfg logger.Config) (*zap.Logger, error) {
		return zap.NewNop(), nil
	}
	newHTTPServerFn = func(cfg server.ServerConfig) *server.HTTPServer {
		return server.NewHTTPServer(server.ServerConfig{Host: "bad host", Port: 8080})
	}
	connectPostgresFn = func(cfg database.PostgresConfig) (*gorm.DB, error) {
		return gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	}

	app, err := NewApp(config.Config{
		Logger: logger.Config{Level: "info"},
		Server: server.ServerConfig{Host: "127.0.0.1", Port: 0},
		Postgres: database.PostgresConfig{
			Host: "dummy",
		},
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	if err := app.Start(); err == nil {
		t.Fatalf("expected start error from invalid address")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	if err := app.ForceShutdown(); err != nil {
		t.Fatalf("ForceShutdown() error = %v", err)
	}
}

func TestNewAppConnectError(t *testing.T) {
	origLogger := newLoggerFn
	origConnect := connectPostgresFn
	t.Cleanup(func() {
		newLoggerFn = origLogger
		connectPostgresFn = origConnect
	})

	newLoggerFn = func(cfg logger.Config) (*zap.Logger, error) { return zap.NewNop(), nil }
	connectPostgresFn = func(cfg database.PostgresConfig) (*gorm.DB, error) {
		return nil, errors.New("db init failed")
	}

	_, err := NewApp(config.Config{
		Logger: logger.Config{Level: "info"},
		Server: server.ServerConfig{Host: "127.0.0.1", Port: 0},
		Postgres: database.PostgresConfig{
			Host: "dummy",
		},
	})
	if err == nil {
		t.Fatalf("expected db init error")
	}
}
