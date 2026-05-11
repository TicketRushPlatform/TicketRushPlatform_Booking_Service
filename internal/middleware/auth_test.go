package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func signedHS256Token(t *testing.T, secret string, claims *AuthClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

func TestRequireAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "test-secret"
	userID := uuid.New()
	cfg := AuthConfig{JWTSecret: secret, JWTAlgorithm: "HS256"}

	validClaims := &AuthClaims{
		Sub:  userID.String(),
		Role: "booking_owner",
		Type: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}

	tests := []struct {
		name       string
		header     string
		cfgAlg     string
		wantStatus int
	}{
		{
			name:       "missing bearer",
			header:     "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "malformed bearer prefix",
			header:     "Basic xxx",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid jwt",
			header:     "Bearer not-a-jwt",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong signing method vs config",
			header:     "Bearer " + signedHS256Token(t, secret, validClaims),
			cfgAlg:     "RS256",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong token type",
			header: "Bearer " + signedHS256Token(t, secret, &AuthClaims{
				Sub:  userID.String(),
				Role: "ADMIN",
				Type: "refresh",
				RegisteredClaims: jwt.RegisteredClaims{
					ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				},
			}),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "invalid subject uuid",
			header: "Bearer " + signedHS256Token(t, secret, &AuthClaims{
				Sub:  "not-a-uuid",
				Role: "ADMIN",
				Type: "access",
				RegisteredClaims: jwt.RegisteredClaims{
					ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				},
			}),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "success",
			header:     "Bearer " + signedHS256Token(t, secret, validClaims),
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testCfg := cfg
			if tt.cfgAlg != "" {
				testCfg.JWTAlgorithm = tt.cfgAlg
			}
			r := gin.New()
			r.GET("/", RequireAuth(testCfg), func(c *gin.Context) {
				id, ok := GetUserID(c)
				if !ok {
					t.Fatalf("expected user id in context")
				}
				if id != userID {
					t.Fatalf("user id mismatch")
				}
				if GetRole(c) != "BOOKING_OWNER" {
					t.Fatalf("role not normalized: %q", GetRole(c))
				}
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

func TestRequireAuth_AllowsPublicSeatStatusReads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequireAuth(AuthConfig{JWTSecret: "test-secret", JWTAlgorithm: "HS256"}))
	r.GET("/api/v1/showtimes/:showtime_id/seats", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/api/v1/showtimes/:showtime_id/seats", func(c *gin.Context) { c.Status(http.StatusOK) })

	showtimeID := uuid.NewString()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/showtimes/"+showtimeID+"/seats", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET seat status without auth status=%d want 200 body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/showtimes/"+showtimeID+"/seats", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("POST seat status without auth status=%d want 401 body=%s", w.Code, w.Body.String())
	}
}

func TestRequireAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin", func(c *gin.Context) {
		c.Set(contextRole, "VIEWER")
		c.Next()
	}, RequireAdmin(), func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want forbidden", w.Code)
	}

	r2 := gin.New()
	r2.GET("/admin", func(c *gin.Context) {
		c.Set(contextRole, "admin")
		c.Next()
	}, RequireAdmin(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w2 := httptest.NewRecorder()
	r2.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("status=%d want ok", w2.Code)
	}
}

func TestRequireAnyRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		c.Set(contextRole, "BOOKING_OWNER")
		c.Next()
	}, RequireAnyRole("EVENT_OWNER", "ADMIN"), func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want forbidden", w.Code)
	}

	r2 := gin.New()
	r2.GET("/x", func(c *gin.Context) {
		c.Set(contextRole, "event_owner")
		c.Next()
	}, RequireAnyRole("EVENT_OWNER"), func(c *gin.Context) { c.Status(http.StatusOK) })
	w2 := httptest.NewRecorder()
	r2.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("status=%d want ok", w2.Code)
	}
}

func TestGetUserIDAndRoleHelpers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	if _, ok := GetUserID(c); ok {
		t.Fatalf("expected missing user id")
	}
	if GetRole(c) != "" {
		t.Fatalf("expected empty role")
	}

	c.Set(contextUserID, "wrong-type")
	if _, ok := GetUserID(c); ok {
		t.Fatalf("expected type mismatch")
	}

	c.Set(contextRole, 123)
	if got := GetRole(c); got != "" {
		t.Fatalf("expected empty role on wrong type, got %q", got)
	}
}
