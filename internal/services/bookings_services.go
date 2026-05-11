package services

import (
	"booking_api/internal/apperror"
	"booking_api/internal/dto"
	"booking_api/internal/models"
	"booking_api/internal/repository"
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

type BookingService interface {
	HoldSeats(req dto.HoldSeatsRequest) (*dto.BookingResponse, error)
	ConfirmBooking(bookingID uuid.UUID) (*dto.BookingResponse, error)
	CancelBooking(bookingID uuid.UUID) error
	GetBooking(bookingID uuid.UUID) (*dto.BookingResponse, error)
	GetBookingsByUser(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error)
	GetSeatsStatus(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error)
	ReleaseExpiredHolds() (int64, error)
	GetDashboardStats() (*dto.DashboardStatsResponse, error)
}

type SeatLocker interface {
	LockSeats(ctx context.Context, showtimeID uuid.UUID, seatIDs []uuid.UUID, owner string) (bool, error)
	UnlockSeats(ctx context.Context, showtimeID uuid.UUID, seatIDs []uuid.UUID, owner string) error
}

type bookingService struct {
	logger     *zap.Logger
	repository repository.BookingRepository
	seatLocker SeatLocker
}

func NewBookingService(
	logger *zap.Logger,
	repository repository.BookingRepository,
) BookingService {
	return NewBookingServiceWithSeatLocker(logger, repository, nil)
}

func NewBookingServiceWithSeatLocker(
	logger *zap.Logger,
	repository repository.BookingRepository,
	seatLocker SeatLocker,
) BookingService {
	return &bookingService{
		logger:     logger,
		repository: repository,
		seatLocker: seatLocker,
	}
}

const maxDeadlockRetries = 3
const seatLockTimeout = 2 * time.Second

func isDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40P01"
	}
	return false
}

// ---------- HoldSeats ----------

func (b *bookingService) HoldSeats(req dto.HoldSeatsRequest) (*dto.BookingResponse, error) {
	b.logger.Info("start hold seats", zap.Any("request", req))

	// Sort seats to prevent deadlocks from inconsistent lock ordering
	sortedSeats := append([]uuid.UUID(nil), req.SeatIDs...)
	sort.Slice(sortedSeats, func(i, j int) bool {
		return sortedSeats[i].String() < sortedSeats[j].String()
	})
	req.SeatIDs = sortedSeats

	b.logger.Debug("sorted seats", zap.Any("sorted_seats", sortedSeats))

	for i := 1; i < len(req.SeatIDs); i++ {
		if req.SeatIDs[i] == req.SeatIDs[i-1] {
			return nil, apperror.NewBadRequest("duplicate seat id in request")
		}
	}

	maxTickets, err := b.repository.GetMaxTicketsPerBooking(req.ShowtimeID)
	if err != nil {
		return nil, err
	}
	if maxTickets != nil && *maxTickets > 0 {
		if len(req.SeatIDs) > *maxTickets {
			return nil, apperror.NewBadRequest(
				fmt.Sprintf("maximum %d tickets per user for this showtime", *maxTickets),
			)
		}
	}

	var lockOwner string
	if b.seatLocker != nil {
		lockOwner = uuid.NewString()

		ctx, cancel := context.WithTimeout(context.Background(), seatLockTimeout)
		locked, lockErr := b.seatLocker.LockSeats(ctx, req.ShowtimeID, req.SeatIDs, lockOwner)
		cancel()
		if lockErr != nil {
			b.logger.Error("failed to acquire redis seat lock", zap.Error(lockErr))
			return nil, fmt.Errorf("failed to acquire seat lock: %w", lockErr)
		}
		if !locked {
			b.logger.Info("seat lock conflict", zap.String("showtime_id", req.ShowtimeID.String()), zap.Any("seat_ids", req.SeatIDs))
			return nil, apperror.NewConflict("some seats are being held by another buyer")
		}

		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), seatLockTimeout)
			defer cancel()
			if err := b.seatLocker.UnlockSeats(ctx, req.ShowtimeID, req.SeatIDs, lockOwner); err != nil {
				b.logger.Warn("failed to release redis seat lock", zap.Error(err))
			}
		}()
	}

	var booking *models.Booking

	for i := 0; i < maxDeadlockRetries; i++ {
		booking, err = b.repository.HoldSeats(req)
		if err == nil {
			break
		}

		if isDeadlock(err) {
			b.logger.Warn("deadlock detected, retrying", zap.Int("attempt", i+1))
			continue
		}

		b.logger.Error("failed to hold seats", zap.Error(err))
		return nil, fmt.Errorf("failed to hold seats: %w", err)
	}

	if err != nil {
		return nil, fmt.Errorf("failed after retries: %w", err)
	}

	b.logger.Info("hold seats successfully", zap.String("booking_id", booking.ID.String()))

	return booking.ToDTO(), nil
}

// ---------- ConfirmBooking ----------

func (b *bookingService) ConfirmBooking(bookingID uuid.UUID) (*dto.BookingResponse, error) {
	b.logger.Info("confirming booking", zap.String("booking_id", bookingID.String()))

	var booking *models.Booking
	var err error

	for i := 0; i < maxDeadlockRetries; i++ {
		booking, err = b.repository.ConfirmBooking(bookingID)
		if err == nil {
			break
		}

		if isDeadlock(err) {
			b.logger.Warn("deadlock on confirm, retrying", zap.Int("attempt", i+1))
			continue
		}

		b.logger.Error("failed to confirm booking", zap.Error(err))
		return nil, err
	}

	if err != nil {
		return nil, fmt.Errorf("failed to confirm after retries: %w", err)
	}

	b.logger.Info("booking confirmed", zap.String("booking_id", bookingID.String()))

	return booking.ToDTO(), nil
}

// ---------- CancelBooking ----------

func (b *bookingService) CancelBooking(bookingID uuid.UUID) error {
	b.logger.Info("canceling booking", zap.String("booking_id", bookingID.String()))

	_, err := b.repository.CancelBooking(bookingID)
	if err != nil {
		b.logger.Error("failed to cancel booking", zap.Error(err))
		return err
	}

	b.logger.Info("booking canceled", zap.String("booking_id", bookingID.String()))
	return nil
}

// ---------- GetBooking ----------

func (b *bookingService) GetBooking(bookingID uuid.UUID) (*dto.BookingResponse, error) {
	b.logger.Debug("getting booking", zap.String("booking_id", bookingID.String()))

	booking, err := b.repository.GetBookingByID(bookingID)
	if err != nil {
		return nil, err
	}

	return booking.ToDTO(), nil
}

// ---------- GetBookingsByUser ----------

func (b *bookingService) GetBookingsByUser(userID uuid.UUID, page, pageSize int) ([]dto.BookingResponse, int64, error) {
	b.logger.Debug("getting bookings by user",
		zap.String("user_id", userID.String()),
		zap.Int("page", page),
		zap.Int("page_size", pageSize),
	)

	bookings, total, err := b.repository.GetBookingsByUser(userID, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	responses := make([]dto.BookingResponse, 0, len(bookings))
	for _, booking := range bookings {
		responses = append(responses, *booking.ToDTO())
	}

	return responses, total, nil
}

// ---------- GetSeatsStatus ----------

func (b *bookingService) GetSeatsStatus(showtimeID uuid.UUID) (*dto.SeatsStatusResponse, error) {
	b.logger.Debug("getting seats status", zap.String("showtime_id", showtimeID.String()))

	seats, err := b.repository.GetSeatsStatus(showtimeID)
	if err != nil {
		return nil, err
	}

	response := &dto.SeatsStatusResponse{
		ShowtimeID: showtimeID.String(),
		Seats:      make([]dto.SeatStatusDTO, 0, len(seats)),
	}

	for _, s := range seats {
		seatDTO := dto.SeatStatusDTO{
			SeatID:    s.SeatID.String(),
			Row:       s.Seat.Row,
			Number:    s.Seat.Number,
			SeatClass: string(s.Seat.SeatClass),
			Status:    string(s.Status),
			ExpiresAt: s.ExpiresAt,
		}
		if s.Price != nil {
			seatDTO.Price = s.Price.String()
		}

		response.Seats = append(response.Seats, seatDTO)

		response.Total++
		switch s.Status {
		case models.ShowTimeSeatStatusAvailable:
			response.Available++
		case models.ShowTimeSeatStatusHolding:
			response.Holding++
		case models.ShowTimeSeatStatusSold:
			response.Sold++
		}
	}

	return response, nil
}

// ---------- ReleaseExpiredHolds ----------

func (b *bookingService) ReleaseExpiredHolds() (int64, error) {
	b.logger.Info("releasing expired holds")

	count, err := b.repository.ReleaseExpiredHolds()
	if err != nil {
		b.logger.Error("failed to release expired holds", zap.Error(err))
		return 0, err
	}

	b.logger.Info("released expired holds", zap.Int64("count", count))
	return count, nil
}

// ---------- GetDashboardStats ----------

func (b *bookingService) GetDashboardStats() (*dto.DashboardStatsResponse, error) {
	b.logger.Debug("fetching admin dashboard stats")
	return b.repository.GetDashboardStats()
}
