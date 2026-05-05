package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

type ServerConfig struct {
	Host string
	Port int
}

type HTTPServer struct {
	server         *http.Server
	router         *gin.Engine
	isShuttingDown atomic.Bool
	baseCtx        context.Context
	cancelBaseCtx  context.CancelFunc
	closeOnce      sync.Once
}

func NewHTTPServer(cfg ServerConfig) *HTTPServer {
	r := gin.New()
	r.Use(gin.Recovery())

	s := &HTTPServer{
		router: r,
	}

	r.GET("/healthz", func(c *gin.Context) {
		if s.isShuttingDown.Load() {
			c.String(http.StatusServiceUnavailable, "shutting down")
			return
		}
		c.String(http.StatusOK, "ok")
	})

	dsn := fmt.Sprintf("%v:%d", cfg.Host, cfg.Port)
	onGoingCtx, cancel := context.WithCancel(context.Background())

	s.baseCtx = onGoingCtx
	s.cancelBaseCtx = cancel

	s.server = &http.Server{
		Addr:    dsn,
		Handler: r,
		BaseContext: func(_ net.Listener) context.Context {
			return onGoingCtx
		},
	}

	return s
}

func (s *HTTPServer) Start() error {
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start http server: %w", err)
	}
	return nil
}

func (s *HTTPServer) Shutdown(ctx context.Context) error {
	s.isShuttingDown.Store(true)
	s.cancelBaseContext()

	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown http server: %w", err)
	}

	return nil
}

func (s *HTTPServer) Close() error {
	s.isShuttingDown.Store(true)
	s.cancelBaseContext()

	var err error
	s.closeOnce.Do(func() {
		err = s.server.Close()
	})

	if err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to force close http server: %w", err)
	}

	return nil
}

func (s *HTTPServer) Router() *gin.Engine {
	return s.router
}

func (s *HTTPServer) cancelBaseContext() {
	if s.cancelBaseCtx != nil {
		s.cancelBaseCtx()
	}
}
