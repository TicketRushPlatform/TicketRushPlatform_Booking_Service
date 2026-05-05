package services

import (
	"booking_api/internal/dto"
	"booking_api/internal/models"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type bookingRepoMock struct {
	holdSeatsFn          func(req dto.HoldSeatsRequest) (*models.Booking, error)
	confirmBookingFn     func(bookingID uuid.UUID) (*models.Booking, error)
	cancelBookingFn      func(bookingID uuid.UUID) (*models.Booking, error)
	getBookingByIDFn     func(bookingID uuid.UUID) (*models.Booking, error)
	getBookingsByUserFn  func(userID uuid.UUID, page, pageSize int) ([]models.Booking, int64, error)
	getSeatsStatusFn     func(showtimeID uuid.UUID) ([]models.ShowTimeSeat, error)
	releaseExpiredFn     func() (int64, error)
	holdSeatsCallCount   int
	confirmCallCount     int
	cancelCallCount      int
	getByIDCallCount     int
	getByUserCallCount   int
	getSeatsCallCount    int
	releaseCallCount     int
	lastHoldSeatsRequest dto.HoldSeatsRequest
}

func (m *bookingRepoMock) HoldSeats(req dto.HoldSeatsRequest) (*models.Booking, error) {
	m.holdSeatsCallCount++
	m.lastHoldSeatsRequest = req
	return m.holdSeatsFn(req)
}

func (m *bookingRepoMock) ConfirmBooking(bookingID uuid.UUID) (*models.Booking, error) {
	m.confirmCallCount++
	return m.confirmBookingFn(bookingID)
}

func (m *bookingRepoMock) CancelBooking(bookingID uuid.UUID) (*models.Booking, error) {
	m.cancelCallCount++
	return m.cancelBookingFn(bookingID)
}

func (m *bookingRepoMock) GetBookingByID(bookingID uuid.UUID) (*models.Booking, error) {
	m.getByIDCallCount++
	return m.getBookingByIDFn(bookingID)
}

func (m *bookingRepoMock) GetBookingsByUser(userID uuid.UUID, page, pageSize int) ([]models.Booking, int64, error) {
	m.getByUserCallCount++
	return m.getBookingsByUserFn(userID, page, pageSize)
}

func (m *bookingRepoMock) GetSeatsStatus(showtimeID uuid.UUID) ([]models.ShowTimeSeat, error) {
	m.getSeatsCallCount++
	return m.getSeatsStatusFn(showtimeID)
}

func (m *bookingRepoMock) ReleaseExpiredHolds() (int64, error) {
	m.releaseCallCount++
	return m.releaseExpiredFn()
}

func buildBooking() *models.Booking {
	now := time.Now().UTC()
	seatID := uuid.New()
	showTimeSeatID := uuid.New()

	return &models.Booking{
		Base: models.Base{
			ID:        uuid.New(),
			CreatedAt: now,
			UpdatedAt: now,
		},
		ShowTimeID: uuid.New(),
		Status:     models.BookingStatusHolding,
		Items: []models.BookingItem{
			{
				ShowTimeSeatID: showTimeSeatID,
				Price:          decimal.NewFromInt(120),
				ShowTimeSeat: models.ShowTimeSeat{
					SeatID: seatID,
					Seat: models.Seat{
						Row:    "A",
						Number: 1,
					},
				},
			},
		},
	}
}

func TestIsDeadlock(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "deadlock error",
			err:  &pgconn.PgError{Code: "40P01"},
			want: true,
		},
		{
			name: "non deadlock pg error",
			err:  &pgconn.PgError{Code: "23505"},
			want: false,
		},
		{
			name: "normal error",
			err:  errors.New("boom"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDeadlock(tt.err)
			if got != tt.want {
				t.Fatalf("isDeadlock() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBookingService_HoldSeats(t *testing.T) {
	deadlockErr := &pgconn.PgError{Code: "40P01"}
	normalErr := errors.New("db unavailable")
	seat1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	seat2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	tests := []struct {
		name          string
		mock          *bookingRepoMock
		wantErr       string
		wantCalls     int
		wantSortedIDs []uuid.UUID
	}{
		{
			name: "success and seat ids sorted",
			mock: &bookingRepoMock{
				holdSeatsFn: func(req dto.HoldSeatsRequest) (*models.Booking, error) {
					return buildBooking(), nil
				},
			},
			wantCalls:     1,
			wantSortedIDs: []uuid.UUID{seat1, seat2},
		},
		{
			name: "non deadlock error returns wrapped message",
			mock: &bookingRepoMock{
				holdSeatsFn: func(req dto.HoldSeatsRequest) (*models.Booking, error) {
					return nil, normalErr
				},
			},
			wantErr:       "failed to hold seats",
			wantCalls:     1,
			wantSortedIDs: []uuid.UUID{seat1, seat2},
		},
		{
			name: "deadlock then success retries",
			mock: func() *bookingRepoMock {
				call := 0
				return &bookingRepoMock{
					holdSeatsFn: func(req dto.HoldSeatsRequest) (*models.Booking, error) {
						call++
						if call == 1 {
							return nil, deadlockErr
						}
						return buildBooking(), nil
					},
				}
			}(),
			wantCalls:     2,
			wantSortedIDs: []uuid.UUID{seat1, seat2},
		},
		{
			name: "deadlock exhausted retries",
			mock: &bookingRepoMock{
				holdSeatsFn: func(req dto.HoldSeatsRequest) (*models.Booking, error) {
					return nil, deadlockErr
				},
			},
			wantErr:       "failed after retries",
			wantCalls:     maxDeadlockRetries,
			wantSortedIDs: []uuid.UUID{seat1, seat2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewBookingService(zap.NewNop(), tt.mock)
			req := dto.HoldSeatsRequest{
				UserID:     uuid.New(),
				ShowtimeID: uuid.New(),
				SeatIDs:    []uuid.UUID{seat2, seat1},
			}

			got, err := svc.HoldSeats(req)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				if got != nil {
					t.Fatalf("expected nil response on error")
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.mock.holdSeatsCallCount != tt.wantCalls {
				t.Fatalf("HoldSeats call count = %d, want %d", tt.mock.holdSeatsCallCount, tt.wantCalls)
			}

			if len(tt.mock.lastHoldSeatsRequest.SeatIDs) == 2 {
				if tt.mock.lastHoldSeatsRequest.SeatIDs[0] != tt.wantSortedIDs[0] ||
					tt.mock.lastHoldSeatsRequest.SeatIDs[1] != tt.wantSortedIDs[1] {
					t.Fatalf("seat IDs not sorted: got %v want %v", tt.mock.lastHoldSeatsRequest.SeatIDs, tt.wantSortedIDs)
				}
			}
		})
	}
}

func TestBookingService_ConfirmBooking(t *testing.T) {
	deadlockErr := &pgconn.PgError{Code: "40P01"}
	normalErr := errors.New("failed confirm")

	tests := []struct {
		name      string
		mock      *bookingRepoMock
		wantErr   string
		wantCalls int
	}{
		{
			name: "success",
			mock: &bookingRepoMock{
				confirmBookingFn: func(bookingID uuid.UUID) (*models.Booking, error) {
					return buildBooking(), nil
				},
			},
			wantCalls: 1,
		},
		{
			name: "non deadlock error no wrap",
			mock: &bookingRepoMock{
				confirmBookingFn: func(bookingID uuid.UUID) (*models.Booking, error) {
					return nil, normalErr
				},
			},
			wantErr:   "failed confirm",
			wantCalls: 1,
		},
		{
			name: "deadlock then success",
			mock: func() *bookingRepoMock {
				call := 0
				return &bookingRepoMock{
					confirmBookingFn: func(bookingID uuid.UUID) (*models.Booking, error) {
						call++
						if call == 1 {
							return nil, deadlockErr
						}
						return buildBooking(), nil
					},
				}
			}(),
			wantCalls: 2,
		},
		{
			name: "deadlock exhausted",
			mock: &bookingRepoMock{
				confirmBookingFn: func(bookingID uuid.UUID) (*models.Booking, error) {
					return nil, deadlockErr
				},
			},
			wantErr:   "failed to confirm after retries",
			wantCalls: maxDeadlockRetries,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewBookingService(zap.NewNop(), tt.mock)
			_, err := svc.ConfirmBooking(uuid.New())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.mock.confirmCallCount != tt.wantCalls {
				t.Fatalf("ConfirmBooking call count = %d, want %d", tt.mock.confirmCallCount, tt.wantCalls)
			}
		})
	}
}

func TestBookingService_OtherMethods(t *testing.T) {
	booking := buildBooking()
	userID := uuid.New()
	showtimeID := uuid.New()
	getErr := errors.New("not found")

	tests := []struct {
		name string
		run  func(t *testing.T, svc BookingService, mock *bookingRepoMock)
	}{
		{
			name: "cancel booking success and error",
			run: func(t *testing.T, svc BookingService, mock *bookingRepoMock) {
				mock.cancelBookingFn = func(bookingID uuid.UUID) (*models.Booking, error) {
					return booking, nil
				}
				if err := svc.CancelBooking(uuid.New()); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				mock.cancelBookingFn = func(bookingID uuid.UUID) (*models.Booking, error) {
					return nil, getErr
				}
				if err := svc.CancelBooking(uuid.New()); !errors.Is(err, getErr) {
					t.Fatalf("expected %v, got %v", getErr, err)
				}
			},
		},
		{
			name: "get booking success and error",
			run: func(t *testing.T, svc BookingService, mock *bookingRepoMock) {
				mock.getBookingByIDFn = func(bookingID uuid.UUID) (*models.Booking, error) {
					return booking, nil
				}
				got, err := svc.GetBooking(uuid.New())
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.ID == "" {
					t.Fatalf("expected mapped booking response")
				}

				mock.getBookingByIDFn = func(bookingID uuid.UUID) (*models.Booking, error) {
					return nil, getErr
				}
				_, err = svc.GetBooking(uuid.New())
				if !errors.Is(err, getErr) {
					t.Fatalf("expected %v, got %v", getErr, err)
				}
			},
		},
		{
			name: "get bookings by user success and error",
			run: func(t *testing.T, svc BookingService, mock *bookingRepoMock) {
				mock.getBookingsByUserFn = func(u uuid.UUID, page, pageSize int) ([]models.Booking, int64, error) {
					return []models.Booking{*booking}, 1, nil
				}
				got, total, err := svc.GetBookingsByUser(userID, 1, 10)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(got) != 1 || total != 1 {
					t.Fatalf("unexpected list result len=%d total=%d", len(got), total)
				}

				mock.getBookingsByUserFn = func(u uuid.UUID, page, pageSize int) ([]models.Booking, int64, error) {
					return nil, 0, getErr
				}
				_, _, err = svc.GetBookingsByUser(userID, 1, 10)
				if !errors.Is(err, getErr) {
					t.Fatalf("expected %v, got %v", getErr, err)
				}
			},
		},
		{
			name: "get seats status success and error",
			run: func(t *testing.T, svc BookingService, mock *bookingRepoMock) {
				mock.getSeatsStatusFn = func(showtimeID uuid.UUID) ([]models.ShowTimeSeat, error) {
					return []models.ShowTimeSeat{
						{
							SeatID: uuid.New(),
							Status: models.ShowTimeSeatStatusAvailable,
							Seat: models.Seat{
								Row:       "A",
								Number:    1,
								SeatClass: models.SeatClassStandard,
							},
						},
						{
							SeatID: uuid.New(),
							Status: models.ShowTimeSeatStatusHolding,
							Seat: models.Seat{
								Row:       "B",
								Number:    2,
								SeatClass: models.SeatClassVIP,
							},
						},
						{
							SeatID: uuid.New(),
							Status: models.ShowTimeSeatStatusSold,
							Seat: models.Seat{
								Row:       "C",
								Number:    3,
								SeatClass: models.SeatClassPremium,
							},
						},
					}, nil
				}
				got, err := svc.GetSeatsStatus(showtimeID)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.Total != 3 || got.Available != 1 || got.Holding != 1 || got.Sold != 1 {
					t.Fatalf("unexpected counters: %+v", got)
				}

				mock.getSeatsStatusFn = func(showtimeID uuid.UUID) ([]models.ShowTimeSeat, error) {
					return nil, getErr
				}
				_, err = svc.GetSeatsStatus(showtimeID)
				if !errors.Is(err, getErr) {
					t.Fatalf("expected %v, got %v", getErr, err)
				}
			},
		},
		{
			name: "release expired holds success and error",
			run: func(t *testing.T, svc BookingService, mock *bookingRepoMock) {
				mock.releaseExpiredFn = func() (int64, error) {
					return 7, nil
				}
				got, err := svc.ReleaseExpiredHolds()
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != 7 {
					t.Fatalf("expected 7, got %d", got)
				}

				mock.releaseExpiredFn = func() (int64, error) {
					return 0, getErr
				}
				_, err = svc.ReleaseExpiredHolds()
				if !errors.Is(err, getErr) {
					t.Fatalf("expected %v, got %v", getErr, err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &bookingRepoMock{
				holdSeatsFn: func(req dto.HoldSeatsRequest) (*models.Booking, error) {
					return buildBooking(), nil
				},
				confirmBookingFn: func(bookingID uuid.UUID) (*models.Booking, error) {
					return buildBooking(), nil
				},
				cancelBookingFn: func(bookingID uuid.UUID) (*models.Booking, error) {
					return buildBooking(), nil
				},
				getBookingByIDFn: func(bookingID uuid.UUID) (*models.Booking, error) {
					return buildBooking(), nil
				},
				getBookingsByUserFn: func(userID uuid.UUID, page, pageSize int) ([]models.Booking, int64, error) {
					return []models.Booking{*buildBooking()}, 1, nil
				},
				getSeatsStatusFn: func(showtimeID uuid.UUID) ([]models.ShowTimeSeat, error) {
					return []models.ShowTimeSeat{}, nil
				},
				releaseExpiredFn: func() (int64, error) {
					return 0, nil
				},
			}
			svc := NewBookingService(zap.NewNop(), mock)
			tt.run(t, svc, mock)
		})
	}
}
