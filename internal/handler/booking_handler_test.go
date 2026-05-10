package handler

import (
	"booking_api/internal/apperror"
	"booking_api/internal/dto"
	"booking_api/internal/middleware"
	"booking_api/internal/services"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const testAuthUserKey = "auth_user_id"
const testAuthRoleKey = "auth_role"

type bookingServiceMock struct {
	holdFn      func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error)
	confirmFn   func(bookingID uuid.UUID) (*dto.BookingResponse, error)
	cancelFn    func(bookingID uuid.UUID) error
	getFn       func(bookingID uuid.UUID) (*dto.BookingResponse, error)
	getByUserFn func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error)
	getSeatsFn  func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error)
	getStatsFn  func() (*dto.DashboardStatsResponse, error)
	releaseFn   func() (int64, error)
}

func (m *bookingServiceMock) HoldSeats(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) {
	return m.holdFn(req)
}
func (m *bookingServiceMock) ConfirmBooking(bookingID uuid.UUID) (*dto.BookingResponse, error) {
	return m.confirmFn(bookingID)
}
func (m *bookingServiceMock) CancelBooking(bookingID uuid.UUID) error { return m.cancelFn(bookingID) }
func (m *bookingServiceMock) GetBooking(bookingID uuid.UUID) (*dto.BookingResponse, error) {
	return m.getFn(bookingID)
}
func (m *bookingServiceMock) GetBookingsByUser(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
	return m.getByUserFn(userID, page, pageSize)
}
func (m *bookingServiceMock) GetSeatsStatus(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) {
	return m.getSeatsFn(showtimeID)
}
func (m *bookingServiceMock) GetDashboardStats() (*dto.DashboardStatsResponse, error) {
	if m.getStatsFn != nil {
		return m.getStatsFn()
	}
	return &dto.DashboardStatsResponse{}, nil
}
func (m *bookingServiceMock) ReleaseExpiredHolds() (int64, error) { return m.releaseFn() }

func TestBookingHandler_SuccessRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := uuid.New()
	authUserID := uuid.New()
	bookingRes := &dto.BookingResponse{ID: id.String(), UserID: authUserID.String(), ShowTimeID: uuid.New().String(), Status: "HOLDING", CreatedAt: time.Now()}

	svc := &bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return bookingRes, nil },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return bookingRes, nil },
		cancelFn:  func(bookingID uuid.UUID) error { return nil },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return bookingRes, nil },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return []dto.BookingResponse{*bookingRes}, 1, nil
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) {
			return &dto.SeatsStatusResponse{ShowtimeID: showtimeID.String()}, nil
		},
		releaseFn: func() (int64, error) { return 1, nil },
	}

	h := NewBookingHandler(svc, zap.NewNop())
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(testAuthUserKey, authUserID)
		if c.Request.URL.Path == "/api/v1/bookings/release-expired" {
			c.Set(testAuthRoleKey, "ADMIN")
		} else {
			c.Set(testAuthRoleKey, "BOOKING_OWNER")
		}
		c.Next()
	})
	v1 := r.Group("/api/v1")
	h.RegisterRoutes(v1)

	userID := authUserID
	showtimeID := uuid.New()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"hold", http.MethodPost, "/api/v1/bookings/hold", `{"user_id":"` + userID.String() + `","showtime_id":"` + showtimeID.String() + `","seat_ids":["` + uuid.New().String() + `"]}`, http.StatusCreated},
		{"get", http.MethodGet, "/api/v1/bookings/" + id.String(), "", http.StatusOK},
		{"confirm", http.MethodPost, "/api/v1/bookings/" + id.String() + "/confirm", "", http.StatusOK},
		{"cancel", http.MethodPost, "/api/v1/bookings/" + id.String() + "/cancel", "", http.StatusOK},
		{"get by user", http.MethodGet, "/api/v1/bookings/user/" + userID.String() + "?page=1&page_size=20", "", http.StatusOK},
		{"get seats", http.MethodGet, "/api/v1/showtimes/" + showtimeID.String() + "/seats", "", http.StatusOK},
		{"release", http.MethodPost, "/api/v1/bookings/release-expired", "", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestBookingHandler_ErrorPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authUserID := uuid.New()
	ownedBooking := &dto.BookingResponse{ID: uuid.NewString(), UserID: authUserID.String(), ShowTimeID: uuid.NewString(), Status: "HOLDING", CreatedAt: time.Now()}
	svc := &bookingServiceMock{
		holdFn: func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) {
			return nil, apperror.NewBadRequest("bad")
		},
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
			return nil, apperror.NewNotFound("not found")
		},
		getFn:    func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return ownedBooking, nil },
		cancelFn: func(bookingID uuid.UUID) error { return errors.New("boom") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("boom")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("boom") },
		releaseFn:  func() (int64, error) { return 0, errors.New("boom") },
	}
	h := NewBookingHandler(svc, zap.NewNop())
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(testAuthUserKey, authUserID)
		c.Set(testAuthRoleKey, "ADMIN")
		c.Next()
	})
	v1 := r.Group("/api/v1")
	h.RegisterRoutes(v1)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"hold invalid json", http.MethodPost, "/api/v1/bookings/hold", `{}`, http.StatusBadRequest},
		{"get invalid id", http.MethodGet, "/api/v1/bookings/bad", "", http.StatusBadRequest},
		{"confirm app error", http.MethodPost, "/api/v1/bookings/" + uuid.New().String() + "/confirm", "", http.StatusNotFound},
		{"cancel internal", http.MethodPost, "/api/v1/bookings/" + uuid.New().String() + "/cancel", "", http.StatusInternalServerError},
		{"get by user invalid user", http.MethodGet, "/api/v1/bookings/user/bad", "", http.StatusBadRequest},
		{"get seats invalid id", http.MethodGet, "/api/v1/showtimes/bad/seats", "", http.StatusBadRequest},
		{"release internal", http.MethodPost, "/api/v1/bookings/release-expired", "", http.StatusInternalServerError},
		{"get by user invalid pagination", http.MethodGet, "/api/v1/bookings/user/" + uuid.New().String() + "?page=0&page_size=20", "", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func bookingJWT(t *testing.T, secret string, sub uuid.UUID, role string) string {
	t.Helper()
	claims := middleware.AuthClaims{
		Sub:  sub.String(),
		Role: role,
		Type: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, &claims)
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return s
}

func TestBookingHandler_AnyRoleCanAccessBookingRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "handler-test-secret"
	owner := uuid.New()
	svc := &bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("mock service error") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("unreachable") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("unreachable") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("unreachable") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("unreachable")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("unreachable") },
		releaseFn:  func() (int64, error) { return 0, errors.New("unreachable") },
	}
	h := NewBookingHandler(svc, zap.NewNop())

	r := gin.New()
	r.Use(middleware.RequireAuth(middleware.AuthConfig{JWTSecret: secret, JWTAlgorithm: "HS256"}))
	v1 := r.Group("/api/v1")
	h.RegisterRoutes(v1)

	body := `{"user_id":"` + owner.String() + `","showtime_id":"` + uuid.New().String() + `","seat_ids":["` + uuid.New().String() + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bookings/hold", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Now any user (even with a normal "USER" role) can access the hold endpoint
	req.Header.Set("Authorization", "Bearer "+bookingJWT(t, secret, owner, "USER"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	
	// We expect 500 because the mock service returns an error, NOT 403 Forbidden
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", w.Code, w.Body.String())
	}
}

func TestBookingHandler_HoldSeats_UnauthorizedContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &bookingServiceMock{
		holdFn: func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) {
			return nil, errors.New("should not call service")
		},
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
		releaseFn:  func() (int64, error) { return 0, errors.New("x") },
	}
	h := NewBookingHandler(svc, zap.NewNop())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"user_id":"` + uuid.New().String() + `","showtime_id":"` + uuid.New().String() + `","seat_ids":["` + uuid.New().String() + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/hold", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	h.HoldSeats(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", w.Code)
	}
}

func TestBookingHandler_ConfirmBooking_ForbiddenAndAdminBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authUser := uuid.New()
	otherUser := uuid.New()
	bid := uuid.New()
	bookingOther := &dto.BookingResponse{
		ID:         bid.String(),
		UserID:     otherUser.String(),
		ShowTimeID: uuid.New().String(),
		Status:     "HOLDING",
		CreatedAt:  time.Now(),
	}
	bookingConfirmed := *bookingOther
	bookingConfirmed.Status = "PAID"

	t.Run("forbidden same role non owner", func(t *testing.T) {
		svc := &bookingServiceMock{
			holdFn: func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
				return nil, errors.New("unreachable")
			},
			cancelFn: func(bookingID uuid.UUID) error { return errors.New("x") },
			getFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
				return bookingOther, nil
			},
			getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
				return nil, 0, errors.New("x")
			},
			getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
			releaseFn:  func() (int64, error) { return 0, errors.New("x") },
		}
		h := NewBookingHandler(svc, zap.NewNop())
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: bid.String()}}
		c.Set(testAuthUserKey, authUser)
		c.Set(testAuthRoleKey, "BOOKING_OWNER")
		c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
		h.ConfirmBooking(c)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", w.Code)
		}
	})

	t.Run("admin bypass confirms", func(t *testing.T) {
		svc := &bookingServiceMock{
			holdFn: func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
				return &bookingConfirmed, nil
			},
			cancelFn: func(bookingID uuid.UUID) error { return errors.New("x") },
			getFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
				return bookingOther, nil
			},
			getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
				return nil, 0, errors.New("x")
			},
			getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) {
				return &dto.SeatsStatusResponse{ShowtimeID: showtimeID.String()}, nil
			},
			releaseFn: func() (int64, error) { return 0, errors.New("x") },
		}
		h := NewBookingHandler(svc, zap.NewNop())
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: bid.String()}}
		c.Set(testAuthUserKey, authUser)
		c.Set(testAuthRoleKey, "ADMIN")
		c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
		h.ConfirmBooking(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d want 200", w.Code)
		}
	})
}

func TestBookingHandler_ConfirmBooking_GetBookingFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &bookingServiceMock{
		holdFn: func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
			return nil, errors.New("unreachable")
		},
		cancelFn: func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
			return nil, apperror.NewNotFound("missing")
		},
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
		releaseFn:  func() (int64, error) { return 0, errors.New("x") },
	}
	h := NewBookingHandler(svc, zap.NewNop())
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: uuid.New().String()}}
	c.Set(testAuthUserKey, uuid.New())
	c.Set(testAuthRoleKey, "BOOKING_OWNER")
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	h.ConfirmBooking(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", w.Code)
	}
}

func TestBookingHandler_HoldSeats_BroadcastPayloadErrorIgnored(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authUserID := uuid.New()
	showtimeID := uuid.New()
	svc := &bookingServiceMock{
		holdFn: func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) {
			return &dto.BookingResponse{
				ID:         uuid.New().String(),
				UserID:     req.UserID.String(),
				ShowTimeID: req.ShowtimeID.String(),
				Status:     "HOLDING",
				CreatedAt:  time.Now(),
			}, nil
		},
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) {
			return nil, errors.New("broadcast build failed")
		},
		releaseFn: func() (int64, error) { return 0, errors.New("x") },
	}
	h := NewBookingHandler(svc, zap.NewNop())
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"user_id":"` + authUserID.String() + `","showtime_id":"` + showtimeID.String() + `","seat_ids":["` + uuid.New().String() + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/hold", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Set(testAuthUserKey, authUserID)
	c.Set(testAuthRoleKey, "BOOKING_OWNER")

	h.HoldSeats(c)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d want 201", w.Code)
	}
}

func TestBookingHandler_StartStopExpiredHoldReleaser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	var releaseCalls int
	svc := &bookingServiceMock{
		holdFn: func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
			return nil, errors.New("x")
		},
		cancelFn: func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
			return nil, errors.New("x")
		},
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) {
			return &dto.SeatsStatusResponse{ShowtimeID: showtimeID.String()}, nil
		},
		releaseFn: func() (int64, error) {
			releaseCalls++
			if releaseCalls == 1 {
				return 0, errors.New("release failed")
			}
			return 2, nil
		},
	}
	h := NewBookingHandler(svc, zap.NewNop())
	_ = h.seatHub.Subscribe(showtimeID)

	h.StartExpiredHoldReleaser(20 * time.Millisecond)
	time.Sleep(90 * time.Millisecond)
	h.StopExpiredHoldReleaser()
	h.StopExpiredHoldReleaser()

	if releaseCalls < 2 {
		t.Fatalf("expected multiple release ticks, got %d", releaseCalls)
	}
}

func TestBookingHandler_StreamSeatsStatus_InvalidShowtime(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewBookingHandler(&bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
		releaseFn:  func() (int64, error) { return 0, errors.New("x") },
	}, zap.NewNop())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: "not-a-uuid"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/ws", nil)
	h.StreamSeatsStatus(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func TestBookingHandler_StreamSeatsStatus_WebSocketRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	svc := &bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) {
			return &dto.SeatsStatusResponse{ShowtimeID: showtimeID.String()}, nil
		},
		releaseFn: func() (int64, error) { return 0, errors.New("x") },
	}
	h := NewBookingHandler(svc, zap.NewNop())

	r := gin.New()
	r.GET("/showtimes/:showtime_id/seats/ws", h.StreamSeatsStatus)
	srv := httptest.NewServer(r)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/showtimes/" + showtimeID.String() + "/seats/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}

	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read initial message: %v", err)
	}

	_ = conn.WriteMessage(websocket.TextMessage, []byte("ping"))
	_ = conn.Close()

	time.Sleep(80 * time.Millisecond)
}

func TestBookingHandler_StreamSeatsStatus_InitialPayloadError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	svc := &bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("no seats") },
		releaseFn:  func() (int64, error) { return 0, errors.New("x") },
	}
	h := NewBookingHandler(svc, zap.NewNop())

	r := gin.New()
	r.GET("/showtimes/:showtime_id/seats/ws", h.StreamSeatsStatus)
	srv := httptest.NewServer(r)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/showtimes/" + showtimeID.String() + "/seats/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	_ = conn.Close()
}

func TestBookingHandler_GetBookingsByUser_ForbiddenDifferentUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authUser := uuid.New()
	other := uuid.New()
	svc := &bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("unreachable")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
		releaseFn:  func() (int64, error) { return 0, errors.New("x") },
	}
	h := NewBookingHandler(svc, zap.NewNop())
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "user_id", Value: other.String()}}
	c.Request = httptest.NewRequest(http.MethodGet, "/user/"+other.String()+"?page=1&page_size=10", nil)
	c.Set(testAuthUserKey, authUser)
	c.Set(testAuthRoleKey, "BOOKING_OWNER")

	h.GetBookingsByUser(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", w.Code)
	}
}

func TestBookingHandler_CancelBooking_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewBookingHandler(&bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
		releaseFn:  func() (int64, error) { return 0, errors.New("x") },
	}, zap.NewNop())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: uuid.New().String()}}
	c.Request = httptest.NewRequest(http.MethodPost, "/cancel", nil)
	h.CancelBooking(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", w.Code)
	}
}

func TestBookingHandler_GetBooking_UnauthorizedAndForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bid := uuid.New()
	owner := uuid.New()
	booking := &dto.BookingResponse{
		ID: bid.String(), UserID: owner.String(), ShowTimeID: uuid.New().String(),
		Status: "HOLDING", CreatedAt: time.Now(),
	}

	t.Run("unauthorized", func(t *testing.T) {
		h := NewBookingHandler(&bookingServiceMock{
			holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
			getFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
				return booking, nil
			},
			getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
				return nil, 0, errors.New("x")
			},
			getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
			releaseFn:  func() (int64, error) { return 0, errors.New("x") },
		}, zap.NewNop())
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: bid.String()}}
		c.Request = httptest.NewRequest(http.MethodGet, "/b", nil)
		h.GetBooking(c)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d want 401", w.Code)
		}
	})

	t.Run("forbidden", func(t *testing.T) {
		h := NewBookingHandler(&bookingServiceMock{
			holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
			getFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
				return booking, nil
			},
			getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
				return nil, 0, errors.New("x")
			},
			getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
			releaseFn:  func() (int64, error) { return 0, errors.New("x") },
		}, zap.NewNop())
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: bid.String()}}
		c.Set(testAuthUserKey, uuid.New())
		c.Set(testAuthRoleKey, "BOOKING_OWNER")
		c.Request = httptest.NewRequest(http.MethodGet, "/b", nil)
		h.GetBooking(c)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", w.Code)
		}
	})
}

func TestBookingHandler_CancelBooking_ForbiddenAndBadShowtimeBroadcastSkipped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bid := uuid.New()
	owner := uuid.New()
	authUser := uuid.New()

	t.Run("forbidden", func(t *testing.T) {
		svc := &bookingServiceMock{
			holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			cancelFn:  func(bookingID uuid.UUID) error { return errors.New("unreachable") },
			getFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
				return &dto.BookingResponse{
					ID: bid.String(), UserID: owner.String(), ShowTimeID: uuid.New().String(),
					Status: "HOLDING", CreatedAt: time.Now(),
				}, nil
			},
			getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
				return nil, 0, errors.New("x")
			},
			getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
			releaseFn:  func() (int64, error) { return 0, errors.New("x") },
		}
		h := NewBookingHandler(svc, zap.NewNop())
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: bid.String()}}
		c.Set(testAuthUserKey, authUser)
		c.Set(testAuthRoleKey, "BOOKING_OWNER")
		c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
		h.CancelBooking(c)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", w.Code)
		}
	})

	t.Run("success skips broadcast on invalid showtime id string", func(t *testing.T) {
		getCalls := 0
		svc := &bookingServiceMock{
			holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			cancelFn:  func(bookingID uuid.UUID) error { return nil },
			getFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
				return &dto.BookingResponse{
					ID: bid.String(), UserID: owner.String(), ShowTimeID: "not-a-uuid",
					Status: "HOLDING", CreatedAt: time.Now(),
				}, nil
			},
			getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
				return nil, 0, errors.New("x")
			},
			getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) {
				getCalls++
				return nil, errors.New("should not broadcast")
			},
			releaseFn: func() (int64, error) { return 0, errors.New("x") },
		}
		h := NewBookingHandler(svc, zap.NewNop())
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: bid.String()}}
		c.Set(testAuthUserKey, owner)
		c.Set(testAuthRoleKey, "BOOKING_OWNER")
		c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
		h.CancelBooking(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d want 200", w.Code)
		}
		if getCalls != 0 {
			t.Fatalf("expected broadcast skipped, getSeats calls=%d", getCalls)
		}
	})
}

func TestBookingHandler_GetAdminDashboard(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("success", func(t *testing.T) {
		svc := &bookingServiceMock{
			holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
			getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
				return nil, 0, errors.New("x")
			},
			getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
			getStatsFn: func() (*dto.DashboardStatsResponse, error) {
				return &dto.DashboardStatsResponse{TotalBookings: 5, PaidBookings: 3}, nil
			},
			releaseFn: func() (int64, error) { return 0, errors.New("x") },
		}
		h := NewBookingHandler(svc, zap.NewNop())
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)

		h.GetAdminDashboard(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d want 200 body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("service error", func(t *testing.T) {
		svc := &bookingServiceMock{
			holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
			getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
			getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
				return nil, 0, errors.New("x")
			},
			getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
			getStatsFn: func() (*dto.DashboardStatsResponse, error) {
				return nil, apperror.NewInternal("dashboard failed", errors.New("db down"))
			},
			releaseFn: func() (int64, error) { return 0, errors.New("x") },
		}
		h := NewBookingHandler(svc, zap.NewNop())
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)

		h.GetAdminDashboard(c)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d want 500 body=%s", w.Code, w.Body.String())
		}
	})
}

func TestBookingHandler_StreamSeatsStatus_UpgradeFail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	h := NewBookingHandler(&bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) {
			return &dto.SeatsStatusResponse{ShowtimeID: showtimeID.String()}, nil
		},
		releaseFn: func() (int64, error) { return 0, errors.New("x") },
	}, zap.NewNop())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: showtimeID.String()}}
	c.Request = httptest.NewRequest(http.MethodGet, "/ws", nil)

	h.StreamSeatsStatus(c)
}

func TestBookingHandler_StreamSeatsStatus_ReceivesBroadcastUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	h := NewBookingHandler(&bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) {
			return &dto.SeatsStatusResponse{ShowtimeID: showtimeID.String()}, nil
		},
		releaseFn: func() (int64, error) { return 0, errors.New("x") },
	}, zap.NewNop())

	r := gin.New()
	r.GET("/showtimes/:showtime_id/seats/ws", h.StreamSeatsStatus)
	srv := httptest.NewServer(r)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/showtimes/" + showtimeID.String() + "/seats/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()

	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read initial message: %v", err)
	}

	updatePayload := []byte(`{"type":"seat_status","data":{"showtime_id":"` + showtimeID.String() + `"}}`)
	h.seatHub.Broadcast(showtimeID, updatePayload)
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read update message: %v", err)
	}
}

func TestBookingHandler_GetBookingsByUser_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	svc := &bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("unreachable")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
		releaseFn:  func() (int64, error) { return 0, errors.New("x") },
	}
	h := NewBookingHandler(svc, zap.NewNop())
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "user_id", Value: userID.String()}}
	c.Request = httptest.NewRequest(http.MethodGet, "/bookings/user/"+userID.String()+"?page=1&page_size=10", nil)

	h.GetBookingsByUser(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", w.Code)
	}
}

func TestBookingHandler_GetSeatsStatus_ServiceAppError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	svc := &bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) {
			return nil, apperror.NewNotFound("showtime not found")
		},
		releaseFn: func() (int64, error) { return 0, errors.New("x") },
	}
	h := NewBookingHandler(svc, zap.NewNop())
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: showtimeID.String()}}
	c.Request = httptest.NewRequest(http.MethodGet, "/showtimes/"+showtimeID.String()+"/seats", nil)

	h.GetSeatsStatus(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", w.Code, w.Body.String())
	}
}

// ---------- virtualQueueServiceMock ----------

type virtualQueueServiceMock struct {
	joinFn                    func(ctx context.Context, showtimeID, userID uuid.UUID) (*dto.QueueStatusResponse, error)
	heartbeatFn               func(ctx context.Context, showtimeID, userID uuid.UUID) (*dto.QueueStatusResponse, error)
	leaveFn                   func(ctx context.Context, showtimeID, userID uuid.UUID) error
	statusFn                  func(ctx context.Context, showtimeID, userID uuid.UUID) (*dto.QueueStatusResponse, error)
	requireActiveBookingRoomFn func(ctx context.Context, showtimeID, userID uuid.UUID) error
}

func (m *virtualQueueServiceMock) Join(ctx context.Context, showtimeID, userID uuid.UUID) (*dto.QueueStatusResponse, error) {
	return m.joinFn(ctx, showtimeID, userID)
}
func (m *virtualQueueServiceMock) Heartbeat(ctx context.Context, showtimeID, userID uuid.UUID) (*dto.QueueStatusResponse, error) {
	return m.heartbeatFn(ctx, showtimeID, userID)
}
func (m *virtualQueueServiceMock) Leave(ctx context.Context, showtimeID, userID uuid.UUID) error {
	return m.leaveFn(ctx, showtimeID, userID)
}
func (m *virtualQueueServiceMock) Status(ctx context.Context, showtimeID, userID uuid.UUID) (*dto.QueueStatusResponse, error) {
	return m.statusFn(ctx, showtimeID, userID)
}
func (m *virtualQueueServiceMock) RequireActiveBookingRoom(ctx context.Context, showtimeID, userID uuid.UUID) error {
	return m.requireActiveBookingRoomFn(ctx, showtimeID, userID)
}

// ---------- SetQueueService ----------

func TestBookingHandler_SetQueueService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewBookingHandler(&bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
		releaseFn:  func() (int64, error) { return 0, errors.New("x") },
	}, zap.NewNop())

	if h.queueService != nil {
		t.Fatalf("expected nil queueService initially")
	}

	qsMock := &virtualQueueServiceMock{}
	h.SetQueueService(qsMock)
	if h.queueService == nil {
		t.Fatalf("expected non-nil queueService after set")
	}
}

// ---------- parseQueueAuth ----------

func TestBookingHandler_ParseQueueAuth_InvalidShowtimeID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewBookingHandler(&bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
		releaseFn:  func() (int64, error) { return 0, errors.New("x") },
	}, zap.NewNop())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: "bad"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)

	_, _, ok := h.parseQueueAuth(c)
	if ok {
		t.Fatalf("expected false for invalid showtime")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func TestBookingHandler_ParseQueueAuth_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewBookingHandler(&bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
		releaseFn:  func() (int64, error) { return 0, errors.New("x") },
	}, zap.NewNop())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: uuid.New().String()}}
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	// No auth user set

	_, _, ok := h.parseQueueAuth(c)
	if ok {
		t.Fatalf("expected false for missing auth")
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", w.Code)
	}
}

func TestBookingHandler_ParseQueueAuth_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewBookingHandler(&bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
		releaseFn:  func() (int64, error) { return 0, errors.New("x") },
	}, zap.NewNop())

	expectedShowtime := uuid.New()
	expectedUser := uuid.New()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: expectedShowtime.String()}}
	c.Set(testAuthUserKey, expectedUser)
	c.Set(testAuthRoleKey, "USER")
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)

	sid, uid, ok := h.parseQueueAuth(c)
	if !ok {
		t.Fatalf("expected success")
	}
	if sid != expectedShowtime || uid != expectedUser {
		t.Fatalf("unexpected ids: showtime=%s user=%s", sid, uid)
	}
}

// ---------- Queue handlers: nil queueService → 503 ----------

func newMinimalSvc() *bookingServiceMock {
	return &bookingServiceMock{
		holdFn:    func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
		releaseFn:  func() (int64, error) { return 0, errors.New("x") },
	}
}

func TestBookingHandler_QueueHandlers_NilService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	userID := uuid.New()

	type handlerFunc func(*BookingHandler) func(*gin.Context)

	handlers := []struct {
		name string
		fn   handlerFunc
	}{
		{"JoinQueue", func(h *BookingHandler) func(*gin.Context) { return h.JoinQueue }},
		{"HeartbeatQueue", func(h *BookingHandler) func(*gin.Context) { return h.HeartbeatQueue }},
		{"LeaveQueue", func(h *BookingHandler) func(*gin.Context) { return h.LeaveQueue }},
		{"GetQueueStatus", func(h *BookingHandler) func(*gin.Context) { return h.GetQueueStatus }},
	}

	for _, tt := range handlers {
		t.Run(tt.name, func(t *testing.T) {
			h := NewBookingHandler(newMinimalSvc(), zap.NewNop())
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Params = gin.Params{{Key: "showtime_id", Value: showtimeID.String()}}
			c.Set(testAuthUserKey, userID)
			c.Set(testAuthRoleKey, "USER")
			c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)

			tt.fn(h)(c)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d want 503", w.Code)
			}
		})
	}
}

// ---------- JoinQueue ----------

func TestBookingHandler_JoinQueue_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	userID := uuid.New()

	qsMock := &virtualQueueServiceMock{
		joinFn: func(ctx context.Context, sid, uid uuid.UUID) (*dto.QueueStatusResponse, error) {
			return &dto.QueueStatusResponse{ShowtimeID: sid.String(), UserID: uid.String(), CanEnter: true}, nil
		},
	}
	h := NewBookingHandler(newMinimalSvc(), zap.NewNop())
	h.SetQueueService(qsMock)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: showtimeID.String()}}
	c.Set(testAuthUserKey, userID)
	c.Set(testAuthRoleKey, "USER")
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)

	h.JoinQueue(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", w.Code, w.Body.String())
	}
}

func TestBookingHandler_JoinQueue_Error(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	userID := uuid.New()

	qsMock := &virtualQueueServiceMock{
		joinFn: func(ctx context.Context, sid, uid uuid.UUID) (*dto.QueueStatusResponse, error) {
			return nil, apperror.NewConflict("already in queue")
		},
	}
	h := NewBookingHandler(newMinimalSvc(), zap.NewNop())
	h.SetQueueService(qsMock)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: showtimeID.String()}}
	c.Set(testAuthUserKey, userID)
	c.Set(testAuthRoleKey, "USER")
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)

	h.JoinQueue(c)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409", w.Code)
	}
}

// ---------- HeartbeatQueue ----------

func TestBookingHandler_HeartbeatQueue_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	userID := uuid.New()

	qsMock := &virtualQueueServiceMock{
		heartbeatFn: func(ctx context.Context, sid, uid uuid.UUID) (*dto.QueueStatusResponse, error) {
			return &dto.QueueStatusResponse{ShowtimeID: sid.String(), CanEnter: true}, nil
		},
	}
	h := NewBookingHandler(newMinimalSvc(), zap.NewNop())
	h.SetQueueService(qsMock)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: showtimeID.String()}}
	c.Set(testAuthUserKey, userID)
	c.Set(testAuthRoleKey, "USER")
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)

	h.HeartbeatQueue(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
}

func TestBookingHandler_HeartbeatQueue_Error(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	userID := uuid.New()

	qsMock := &virtualQueueServiceMock{
		heartbeatFn: func(ctx context.Context, sid, uid uuid.UUID) (*dto.QueueStatusResponse, error) {
			return nil, errors.New("redis down")
		},
	}
	h := NewBookingHandler(newMinimalSvc(), zap.NewNop())
	h.SetQueueService(qsMock)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: showtimeID.String()}}
	c.Set(testAuthUserKey, userID)
	c.Set(testAuthRoleKey, "USER")
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)

	h.HeartbeatQueue(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", w.Code)
	}
}

// ---------- LeaveQueue ----------

func TestBookingHandler_LeaveQueue_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	userID := uuid.New()

	qsMock := &virtualQueueServiceMock{
		leaveFn: func(ctx context.Context, sid, uid uuid.UUID) error {
			return nil
		},
	}
	h := NewBookingHandler(newMinimalSvc(), zap.NewNop())
	h.SetQueueService(qsMock)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: showtimeID.String()}}
	c.Set(testAuthUserKey, userID)
	c.Set(testAuthRoleKey, "USER")
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)

	h.LeaveQueue(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
}

func TestBookingHandler_LeaveQueue_Error(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	userID := uuid.New()

	qsMock := &virtualQueueServiceMock{
		leaveFn: func(ctx context.Context, sid, uid uuid.UUID) error {
			return apperror.NewInternal("leave failed", errors.New("redis"))
		},
	}
	h := NewBookingHandler(newMinimalSvc(), zap.NewNop())
	h.SetQueueService(qsMock)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: showtimeID.String()}}
	c.Set(testAuthUserKey, userID)
	c.Set(testAuthRoleKey, "USER")
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)

	h.LeaveQueue(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", w.Code)
	}
}

// ---------- GetQueueStatus ----------

func TestBookingHandler_GetQueueStatus_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	userID := uuid.New()

	qsMock := &virtualQueueServiceMock{
		statusFn: func(ctx context.Context, sid, uid uuid.UUID) (*dto.QueueStatusResponse, error) {
			return &dto.QueueStatusResponse{ShowtimeID: sid.String(), Position: 3}, nil
		},
	}
	h := NewBookingHandler(newMinimalSvc(), zap.NewNop())
	h.SetQueueService(qsMock)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: showtimeID.String()}}
	c.Set(testAuthUserKey, userID)
	c.Set(testAuthRoleKey, "USER")
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)

	h.GetQueueStatus(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
}

func TestBookingHandler_GetQueueStatus_Error(t *testing.T) {
	gin.SetMode(gin.TestMode)
	showtimeID := uuid.New()
	userID := uuid.New()

	qsMock := &virtualQueueServiceMock{
		statusFn: func(ctx context.Context, sid, uid uuid.UUID) (*dto.QueueStatusResponse, error) {
			return nil, apperror.NewConflict(services.QueueKickMessage)
		},
	}
	h := NewBookingHandler(newMinimalSvc(), zap.NewNop())
	h.SetQueueService(qsMock)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "showtime_id", Value: showtimeID.String()}}
	c.Set(testAuthUserKey, userID)
	c.Set(testAuthRoleKey, "USER")
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)

	h.GetQueueStatus(c)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409", w.Code)
	}
}

// ---------- HoldSeats with queueService ----------

func TestBookingHandler_HoldSeats_QueueServiceForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authUserID := uuid.New()
	showtimeID := uuid.New()

	svc := &bookingServiceMock{
		holdFn: func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) {
			t.Fatalf("service should not be called when queue check fails")
			return nil, nil
		},
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("x") },
		releaseFn:  func() (int64, error) { return 0, errors.New("x") },
	}

	qsMock := &virtualQueueServiceMock{
		requireActiveBookingRoomFn: func(ctx context.Context, sid, uid uuid.UUID) error {
			return apperror.NewForbidden("waiting room required")
		},
	}

	h := NewBookingHandler(svc, zap.NewNop())
	h.SetQueueService(qsMock)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"user_id":"` + authUserID.String() + `","showtime_id":"` + showtimeID.String() + `","seat_ids":["` + uuid.New().String() + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/hold", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Set(testAuthUserKey, authUserID)
	c.Set(testAuthRoleKey, "USER")

	h.HoldSeats(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 body=%s", w.Code, w.Body.String())
	}
}

func TestBookingHandler_HoldSeats_QueueServiceAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authUserID := uuid.New()
	showtimeID := uuid.New()

	svc := &bookingServiceMock{
		holdFn: func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) {
			return &dto.BookingResponse{
				ID:         uuid.New().String(),
				UserID:     req.UserID.String(),
				ShowTimeID: req.ShowtimeID.String(),
				Status:     "HOLDING",
				CreatedAt:  time.Now(),
			}, nil
		},
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		cancelFn:  func(bookingID uuid.UUID) error { return errors.New("x") },
		getFn:     func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("x") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("x")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) {
			return &dto.SeatsStatusResponse{ShowtimeID: showtimeID.String()}, nil
		},
		releaseFn: func() (int64, error) { return 0, errors.New("x") },
	}

	qsMock := &virtualQueueServiceMock{
		requireActiveBookingRoomFn: func(ctx context.Context, sid, uid uuid.UUID) error {
			return nil // allowed
		},
	}

	h := NewBookingHandler(svc, zap.NewNop())
	h.SetQueueService(qsMock)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"user_id":"` + authUserID.String() + `","showtime_id":"` + showtimeID.String() + `","seat_ids":["` + uuid.New().String() + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/hold", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Set(testAuthUserKey, authUserID)
	c.Set(testAuthRoleKey, "USER")

	h.HoldSeats(c)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d want 201 body=%s", w.Code, w.Body.String())
	}
}
