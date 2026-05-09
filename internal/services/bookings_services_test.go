package services

import (
	"booking_api/internal/apperror"
	"booking_api/internal/dto"
	"booking_api/internal/models"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type seatLockerMock struct {
	lockFn       func(ctx context.Context, showtimeID uuid.UUID, seatIDs []uuid.UUID, owner string) (bool, error)
	unlockFn     func(ctx context.Context, showtimeID uuid.UUID, seatIDs []uuid.UUID, owner string) error
	lockCalls    int
	unlockCalls  int
	lastSeatIDs  []uuid.UUID
	lastOwner    string
	unlockOwners []string
}

func (m *seatLockerMock) LockSeats(ctx context.Context, showtimeID uuid.UUID, seatIDs []uuid.UUID, owner string) (bool, error) {
	m.lockCalls++
	m.lastSeatIDs = append([]uuid.UUID(nil), seatIDs...)
	m.lastOwner = owner
	if m.lockFn != nil {
		return m.lockFn(ctx, showtimeID, seatIDs, owner)
	}
	return true, nil
}

func (m *seatLockerMock) UnlockSeats(ctx context.Context, showtimeID uuid.UUID, seatIDs []uuid.UUID, owner string) error {
	m.unlockCalls++
	m.unlockOwners = append(m.unlockOwners, owner)
	if m.unlockFn != nil {
		return m.unlockFn(ctx, showtimeID, seatIDs, owner)
	}
	return nil
}

type concurrentSeatLocker struct {
	mu           sync.Mutex
	owners       map[string]string
	totalCalls   int
	lockCalls    int
	unlockCalls  int
	allAttempted chan struct{}
	closeOnce    sync.Once
}

func newConcurrentSeatLocker(totalCalls int) *concurrentSeatLocker {
	return &concurrentSeatLocker{
		owners:       make(map[string]string),
		totalCalls:   totalCalls,
		allAttempted: make(chan struct{}),
	}
}

func (l *concurrentSeatLocker) LockSeats(ctx context.Context, showtimeID uuid.UUID, seatIDs []uuid.UUID, owner string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.lockCalls++
	if l.lockCalls >= l.totalCalls {
		l.closeOnce.Do(func() { close(l.allAttempted) })
	}

	for _, seatID := range seatIDs {
		if _, exists := l.owners[showtimeID.String()+":"+seatID.String()]; exists {
			return false, nil
		}
	}

	for _, seatID := range seatIDs {
		l.owners[showtimeID.String()+":"+seatID.String()] = owner
	}

	return true, nil
}

func (l *concurrentSeatLocker) UnlockSeats(ctx context.Context, showtimeID uuid.UUID, seatIDs []uuid.UUID, owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.unlockCalls++
	for _, seatID := range seatIDs {
		key := showtimeID.String() + ":" + seatID.String()
		if l.owners[key] == owner {
			delete(l.owners, key)
		}
	}

	return nil
}

type bookingRepoMock struct {
	mu                   sync.Mutex
	holdSeatsFn          func(req dto.HoldSeatsRequest) (*models.Booking, error)
	confirmBookingFn     func(bookingID uuid.UUID) (*models.Booking, error)
	cancelBookingFn      func(bookingID uuid.UUID) (*models.Booking, error)
	getBookingByIDFn     func(bookingID uuid.UUID) (*models.Booking, error)
	getBookingsByUserFn  func(userID uuid.UUID, page, pageSize int) ([]models.Booking, int64, error)
	getSeatsStatusFn     func(showtimeID uuid.UUID) ([]models.ShowTimeSeat, error)
	getDashboardStatsFn  func() (*dto.DashboardStatsResponse, error)
	releaseExpiredFn     func() (int64, error)
	holdSeatsCallCount   int
	confirmCallCount     int
	cancelCallCount      int
	getByIDCallCount     int
	getByUserCallCount   int
	getSeatsCallCount    int
	getStatsCallCount    int
	releaseCallCount     int
	lastHoldSeatsRequest dto.HoldSeatsRequest
}

func (m *bookingRepoMock) HoldSeats(req dto.HoldSeatsRequest) (*models.Booking, error) {
	m.mu.Lock()
	m.holdSeatsCallCount++
	m.lastHoldSeatsRequest = req
	m.mu.Unlock()
	return m.holdSeatsFn(req)
}

func (m *bookingRepoMock) ConfirmBooking(bookingID uuid.UUID) (*models.Booking, error) {
	m.mu.Lock()
	m.confirmCallCount++
	m.mu.Unlock()
	return m.confirmBookingFn(bookingID)
}

func (m *bookingRepoMock) CancelBooking(bookingID uuid.UUID) (*models.Booking, error) {
	m.mu.Lock()
	m.cancelCallCount++
	m.mu.Unlock()
	return m.cancelBookingFn(bookingID)
}

func (m *bookingRepoMock) GetBookingByID(bookingID uuid.UUID) (*models.Booking, error) {
	m.mu.Lock()
	m.getByIDCallCount++
	m.mu.Unlock()
	return m.getBookingByIDFn(bookingID)
}

func (m *bookingRepoMock) GetBookingsByUser(userID uuid.UUID, page, pageSize int) ([]models.Booking, int64, error) {
	m.mu.Lock()
	m.getByUserCallCount++
	m.mu.Unlock()
	return m.getBookingsByUserFn(userID, page, pageSize)
}

func (m *bookingRepoMock) GetSeatsStatus(showtimeID uuid.UUID) ([]models.ShowTimeSeat, error) {
	m.mu.Lock()
	m.getSeatsCallCount++
	m.mu.Unlock()
	return m.getSeatsStatusFn(showtimeID)
}

func (m *bookingRepoMock) GetDashboardStats() (*dto.DashboardStatsResponse, error) {
	m.mu.Lock()
	m.getStatsCallCount++
	m.mu.Unlock()
	if m.getDashboardStatsFn != nil {
		return m.getDashboardStatsFn()
	}
	return &dto.DashboardStatsResponse{}, nil
}

func (m *bookingRepoMock) ReleaseExpiredHolds() (int64, error) {
	m.mu.Lock()
	m.releaseCallCount++
	m.mu.Unlock()
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

func TestBookingService_HoldSeatsWithSeatLocker(t *testing.T) {
	seat1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	seat2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	lockErr := errors.New("redis unavailable")

	tests := []struct {
		name            string
		repo            *bookingRepoMock
		locker          *seatLockerMock
		wantErr         string
		wantAppErrCode  int
		wantRepoCalls   int
		wantLockCalls   int
		wantUnlockCalls int
	}{
		{
			name: "lock success calls repository and unlocks",
			repo: &bookingRepoMock{
				holdSeatsFn: func(req dto.HoldSeatsRequest) (*models.Booking, error) {
					return buildBooking(), nil
				},
			},
			locker:          &seatLockerMock{},
			wantRepoCalls:   1,
			wantLockCalls:   1,
			wantUnlockCalls: 1,
		},
		{
			name: "lock conflict skips repository",
			repo: &bookingRepoMock{
				holdSeatsFn: func(req dto.HoldSeatsRequest) (*models.Booking, error) {
					t.Fatalf("repository should not be called when redis lock conflicts")
					return nil, nil
				},
			},
			locker: &seatLockerMock{
				lockFn: func(ctx context.Context, showtimeID uuid.UUID, seatIDs []uuid.UUID, owner string) (bool, error) {
					return false, nil
				},
			},
			wantErr:        "some seats are being held",
			wantAppErrCode: 409,
			wantLockCalls:  1,
		},
		{
			name: "lock error skips repository",
			repo: &bookingRepoMock{
				holdSeatsFn: func(req dto.HoldSeatsRequest) (*models.Booking, error) {
					t.Fatalf("repository should not be called when redis lock errors")
					return nil, nil
				},
			},
			locker: &seatLockerMock{
				lockFn: func(ctx context.Context, showtimeID uuid.UUID, seatIDs []uuid.UUID, owner string) (bool, error) {
					return false, lockErr
				},
			},
			wantErr:       "failed to acquire seat lock",
			wantLockCalls: 1,
		},
		{
			name: "repository error still unlocks",
			repo: &bookingRepoMock{
				holdSeatsFn: func(req dto.HoldSeatsRequest) (*models.Booking, error) {
					return nil, errors.New("db unavailable")
				},
			},
			locker:          &seatLockerMock{},
			wantErr:         "failed to hold seats",
			wantRepoCalls:   1,
			wantLockCalls:   1,
			wantUnlockCalls: 1,
		},
		{
			name: "unlock failure after success still returns booking",
			repo: &bookingRepoMock{
				holdSeatsFn: func(req dto.HoldSeatsRequest) (*models.Booking, error) {
					return buildBooking(), nil
				},
			},
			locker: &seatLockerMock{
				unlockFn: func(ctx context.Context, showtimeID uuid.UUID, seatIDs []uuid.UUID, owner string) error {
					return errors.New("redis unlock failed")
				},
			},
			wantRepoCalls:   1,
			wantLockCalls:   1,
			wantUnlockCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewBookingServiceWithSeatLocker(zap.NewNop(), tt.repo, tt.locker)
			req := dto.HoldSeatsRequest{
				UserID:     uuid.New(),
				ShowtimeID: uuid.New(),
				SeatIDs:    []uuid.UUID{seat2, seat1},
			}

			_, err := svc.HoldSeats(req)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				if tt.wantAppErrCode != 0 {
					var appErr *apperror.AppError
					if !errors.As(err, &appErr) || appErr.Code != tt.wantAppErrCode {
						t.Fatalf("expected app error code %d, got %v", tt.wantAppErrCode, err)
					}
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.repo.holdSeatsCallCount != tt.wantRepoCalls {
				t.Fatalf("repo calls = %d, want %d", tt.repo.holdSeatsCallCount, tt.wantRepoCalls)
			}
			if tt.locker.lockCalls != tt.wantLockCalls {
				t.Fatalf("lock calls = %d, want %d", tt.locker.lockCalls, tt.wantLockCalls)
			}
			if tt.locker.unlockCalls != tt.wantUnlockCalls {
				t.Fatalf("unlock calls = %d, want %d", tt.locker.unlockCalls, tt.wantUnlockCalls)
			}
			if tt.wantLockCalls > 0 && len(tt.locker.lastSeatIDs) == 2 {
				if tt.locker.lastSeatIDs[0] != seat1 || tt.locker.lastSeatIDs[1] != seat2 {
					t.Fatalf("locker seat ids not sorted: got %v", tt.locker.lastSeatIDs)
				}
			}
			if tt.wantUnlockCalls > 0 && tt.locker.unlockOwners[0] != tt.locker.lastOwner {
				t.Fatalf("unlock owner = %q, want %q", tt.locker.unlockOwners[0], tt.locker.lastOwner)
			}
		})
	}
}

func TestBookingService_HoldSeatsConcurrentUsersSameSeat(t *testing.T) {
	const userCount = 20

	showtimeID := uuid.New()
	seatID := uuid.New()
	locker := newConcurrentSeatLocker(userCount)

	var repoMu sync.Mutex
	seatHeld := false
	repo := &bookingRepoMock{
		holdSeatsFn: func(req dto.HoldSeatsRequest) (*models.Booking, error) {
			repoMu.Lock()
			defer repoMu.Unlock()

			if seatHeld {
				return nil, apperror.NewConflict("some seats are not available")
			}

			seatHeld = true
			select {
			case <-locker.allAttempted:
			case <-time.After(time.Second):
				return nil, errors.New("timed out waiting for concurrent lock attempts")
			}

			booking := buildBooking()
			booking.UserID = req.UserID
			booking.ShowTimeID = req.ShowtimeID
			return booking, nil
		},
	}

	svc := NewBookingServiceWithSeatLocker(zap.NewNop(), repo, locker)
	start := make(chan struct{})
	var wg sync.WaitGroup

	var resultMu sync.Mutex
	successes := 0
	conflicts := 0
	failures := 0

	for i := 0; i < userCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			_, err := svc.HoldSeats(dto.HoldSeatsRequest{
				UserID:     uuid.New(),
				ShowtimeID: showtimeID,
				SeatIDs:    []uuid.UUID{seatID},
			})

			resultMu.Lock()
			defer resultMu.Unlock()

			if err == nil {
				successes++
				return
			}

			var appErr *apperror.AppError
			if errors.As(err, &appErr) && appErr.Code == 409 {
				conflicts++
				return
			}

			failures++
		}()
	}

	close(start)
	wg.Wait()

	if successes != 1 {
		t.Fatalf("successes = %d, want 1", successes)
	}
	if conflicts != userCount-1 {
		t.Fatalf("conflicts = %d, want %d", conflicts, userCount-1)
	}
	if failures != 0 {
		t.Fatalf("failures = %d, want 0", failures)
	}
	if repo.holdSeatsCallCount != 1 {
		t.Fatalf("repository HoldSeats calls = %d, want 1", repo.holdSeatsCallCount)
	}
	if locker.lockCalls != userCount {
		t.Fatalf("seat lock calls = %d, want %d", locker.lockCalls, userCount)
	}
	if locker.unlockCalls != 1 {
		t.Fatalf("seat unlock calls = %d, want 1", locker.unlockCalls)
	}
}

func TestBookingService_HoldSeatsConcurrentUsersOverlappingSeats(t *testing.T) {
	showtimeID := uuid.New()
	seatA := uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	seatB := uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	seatC := uuid.MustParse("00000000-0000-0000-0000-00000000000c")
	locker := newConcurrentSeatLocker(2)

	var repoMu sync.Mutex
	heldSeats := make(map[uuid.UUID]struct{})
	successfulSeatIDs := make([]uuid.UUID, 0, 2)
	repo := &bookingRepoMock{
		holdSeatsFn: func(req dto.HoldSeatsRequest) (*models.Booking, error) {
			repoMu.Lock()
			defer repoMu.Unlock()

			for _, seatID := range req.SeatIDs {
				if _, exists := heldSeats[seatID]; exists {
					return nil, apperror.NewConflict("some seats are not available")
				}
			}

			for _, seatID := range req.SeatIDs {
				heldSeats[seatID] = struct{}{}
			}
			successfulSeatIDs = append(successfulSeatIDs, req.SeatIDs...)

			select {
			case <-locker.allAttempted:
			case <-time.After(time.Second):
				return nil, errors.New("timed out waiting for overlapping lock attempts")
			}

			booking := buildBooking()
			booking.UserID = req.UserID
			booking.ShowTimeID = req.ShowtimeID
			return booking, nil
		},
	}

	svc := NewBookingServiceWithSeatLocker(zap.NewNop(), repo, locker)
	start := make(chan struct{})
	var wg sync.WaitGroup

	type holdResult struct {
		name string
		err  error
	}
	results := make(chan holdResult, 2)

	requests := []struct {
		name    string
		seatIDs []uuid.UUID
	}{
		{name: "first user A,B", seatIDs: []uuid.UUID{seatA, seatB}},
		{name: "second user B,C", seatIDs: []uuid.UUID{seatB, seatC}},
	}

	for _, request := range requests {
		request := request
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			_, err := svc.HoldSeats(dto.HoldSeatsRequest{
				UserID:     uuid.New(),
				ShowtimeID: showtimeID,
				SeatIDs:    request.seatIDs,
			})
			results <- holdResult{name: request.name, err: err}
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	successes := 0
	conflicts := 0
	failures := 0
	for result := range results {
		if result.err == nil {
			successes++
			continue
		}

		var appErr *apperror.AppError
		if errors.As(result.err, &appErr) && appErr.Code == 409 {
			conflicts++
			continue
		}

		t.Logf("%s failed with unexpected error: %v", result.name, result.err)
		failures++
	}

	if successes != 1 {
		t.Fatalf("successes = %d, want 1", successes)
	}
	if conflicts != 1 {
		t.Fatalf("conflicts = %d, want 1", conflicts)
	}
	if failures != 0 {
		t.Fatalf("failures = %d, want 0", failures)
	}
	if repo.holdSeatsCallCount != 1 {
		t.Fatalf("repository HoldSeats calls = %d, want 1", repo.holdSeatsCallCount)
	}
	if locker.lockCalls != 2 {
		t.Fatalf("seat lock calls = %d, want 2", locker.lockCalls)
	}
	if locker.unlockCalls != 1 {
		t.Fatalf("seat unlock calls = %d, want 1", locker.unlockCalls)
	}
	if len(successfulSeatIDs) != 2 {
		t.Fatalf("successful seat count = %d, want 2", len(successfulSeatIDs))
	}

	successSet := map[uuid.UUID]struct{}{}
	for _, seatID := range successfulSeatIDs {
		successSet[seatID] = struct{}{}
	}
	_, hasA := successSet[seatA]
	_, hasB := successSet[seatB]
	_, hasC := successSet[seatC]
	if !(hasB && (hasA != hasC)) {
		t.Fatalf("successful hold must be exactly A,B or B,C, got %v", successfulSeatIDs)
	}

	locker.mu.Lock()
	defer locker.mu.Unlock()
	if len(locker.owners) != 0 {
		t.Fatalf("expected no leftover or partial redis locks, got %v", locker.owners)
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

func TestBookingService_GetDashboardStats(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		expected := &dto.DashboardStatsResponse{
			TotalBookings: 12,
			PaidBookings:  10,
			TotalRevenue:  999.5,
		}
		mock := &bookingRepoMock{
			getDashboardStatsFn: func() (*dto.DashboardStatsResponse, error) {
				return expected, nil
			},
		}

		svc := NewBookingService(zap.NewNop(), mock)
		got, err := svc.GetDashboardStats()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.TotalBookings != expected.TotalBookings || got.PaidBookings != expected.PaidBookings {
			t.Fatalf("unexpected stats response: %+v", got)
		}
		if mock.getStatsCallCount != 1 {
			t.Fatalf("GetDashboardStats calls = %d, want 1", mock.getStatsCallCount)
		}
	})

	t.Run("repository error", func(t *testing.T) {
		repoErr := errors.New("db down")
		mock := &bookingRepoMock{
			getDashboardStatsFn: func() (*dto.DashboardStatsResponse, error) {
				return nil, repoErr
			},
		}

		svc := NewBookingService(zap.NewNop(), mock)
		got, err := svc.GetDashboardStats()
		if !errors.Is(err, repoErr) {
			t.Fatalf("expected %v, got %v", repoErr, err)
		}
		if got != nil {
			t.Fatalf("expected nil stats on error")
		}
		if mock.getStatsCallCount != 1 {
			t.Fatalf("GetDashboardStats calls = %d, want 1", mock.getStatsCallCount)
		}
	})
}
