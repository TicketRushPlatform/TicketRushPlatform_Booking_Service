package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServerBasics(t *testing.T) {
	s := NewHTTPServer(ServerConfig{Host: "127.0.0.1", Port: 0})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d", w.Code, http.StatusOK)
	}
}

func TestServerShutdownClose(t *testing.T) {
	s := NewHTTPServer(ServerConfig{Host: "127.0.0.1", Port: 0})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown err=%v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close err=%v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want=%d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestServerStartError(t *testing.T) {
	s := NewHTTPServer(ServerConfig{Host: "bad host", Port: 8080})
	if err := s.Start(); err == nil {
		t.Fatalf("expected start error")
	}
}
