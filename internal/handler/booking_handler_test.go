package handler

import (
	"booking_api/internal/apperror"
	"booking_api/internal/dto"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

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
	bookingRes := &dto.BookingResponse{ID: id.String(), ShowTimeID: uuid.New().String(), Status: "HOLDING", CreatedAt: time.Now()}

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
	v1 := r.Group("/api/v1")
	h.RegisterRoutes(v1)

	userID := uuid.New()
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
	svc := &bookingServiceMock{
		holdFn: func(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) {
			return nil, apperror.NewBadRequest("bad")
		},
		confirmFn: func(bookingID uuid.UUID) (*dto.BookingResponse, error) {
			return nil, apperror.NewNotFound("not found")
		},
		cancelFn: func(bookingID uuid.UUID) error { return errors.New("boom") },
		getFn:    func(bookingID uuid.UUID) (*dto.BookingResponse, error) { return nil, errors.New("boom") },
		getByUserFn: func(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
			return nil, 0, errors.New("boom")
		},
		getSeatsFn: func(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) { return nil, errors.New("boom") },
		releaseFn:  func() (int64, error) { return 0, errors.New("boom") },
	}
	h := NewBookingHandler(svc, zap.NewNop())
	r := gin.New()
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
