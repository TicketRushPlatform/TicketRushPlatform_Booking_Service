package handler

import (
	"booking_api/internal/apperror"
	"booking_api/internal/dto"
	"booking_api/internal/middleware"
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
