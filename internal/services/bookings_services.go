package services

import (
	"booking_api/internal/dto"
	"booking_api/internal/models"
	"booking_api/internal/repository"
	"errors"
	"fmt"
	"sort"

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
}

type bookingService struct {
	logger     *zap.Logger
	repository repository.BookingRepository
}

func NewBookingService(
	logger *zap.Logger,
	repository repository.BookingRepository,
) BookingService {
	return &bookingService{
		logger:     logger,
		repository: repository,
	}
}

const maxDeadlockRetries = 3

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

	var booking *models.Booking
	var err error

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
