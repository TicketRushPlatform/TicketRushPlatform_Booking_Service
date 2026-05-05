package database

import "testing"

func TestConnectPostgres_MissingHost(t *testing.T) {
	_, err := ConnectPostgres(PostgresConfig{})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestConnectPostgres_PingFailure(t *testing.T) {
	_, err := ConnectPostgres(PostgresConfig{
		Host:            "127.0.0.1",
		Port:            1,
		User:            "x",
		DBName:          "x",
		Password:        "x",
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: 1,
	})
	if err == nil {
		t.Fatalf("expected ping error")
	}
}
