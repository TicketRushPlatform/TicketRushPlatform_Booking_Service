package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS(), RequestID(), RequestLogger(zap.NewNop()))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	tests := []struct {
		name       string
		method     string
		requestID  string
		wantStatus int
	}{
		{"get", http.MethodGet, "", http.StatusOK},
		{"options", http.MethodOptions, "", http.StatusNoContent},
		{"with request id", http.MethodGet, "abc", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/x", nil)
			if tt.requestID != "" {
				req.Header.Set("X-Request-ID", tt.requestID)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d", w.Code, tt.wantStatus)
			}
			if tt.method != http.MethodOptions && w.Header().Get("X-Request-ID") == "" {
				t.Fatalf("missing request id response header")
			}
		})
	}
}

func TestRequestLogger_ServerErrorLogBranch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestLogger(zap.NewNop()))
	r.GET("/boom", func(c *gin.Context) { c.Status(http.StatusInternalServerError) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", w.Code)
	}
}
