package config

import (
	"booking_api/internal/infrastructure/database"
	"booking_api/internal/infrastructure/logger"
	"booking_api/internal/server"

	"github.com/spf13/viper"
)

type Config struct {
	Logger   logger.Config
	Server   server.ServerConfig
	Postgres database.PostgresConfig
}

func NewConfig() Config {
	v := viper.New()

	v.AutomaticEnv()

	return Config{
		Server: server.ServerConfig{
			Host: v.GetString("SERVER_HOST"),
			Port: v.GetInt("SERVER_PORT"),
		},

		Postgres: database.PostgresConfig{
			Host:     v.GetString("POSTGRES_HOST"),
			Port:     v.GetInt("POSTGRES_PORT"),
			User:     v.GetString("POSTGRES_USER"),
			DBName:   v.GetString("POSTGRES_DB"),
			Password: v.GetString("POSTGRES_PASSWORD"),
			SSLMode:  v.GetString("POSTGRES_SSLMODE"),

			MaxOpenConns:    v.GetInt("POSTGRES_MAX_OPEN_CONNS"),
			MaxIdleConns:    v.GetInt("POSTGRES_MAX_IDLE_CONNS"),
			ConnMaxLifetime: v.GetInt("POSTGRES_CONN_MAX_LIFETIME"),
		},

		Logger: logger.Config{
			Level: v.GetString("LOG_LEVEL"),
		},
	}
}
