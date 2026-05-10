package services

import (
	"booking_api/internal/apperror"
	"booking_api/internal/dto"
	"booking_api/internal/redisqueue"
	"booking_api/internal/repository"
	"context"
	"errors"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const QueueKickMessage = "you were removed from queue because you left the queue page"

type VirtualQueueService interface {
	Join(ctx context.Context, showtimeID, userID uuid.UUID) (*dto.QueueStatusResponse, error)
	Heartbeat(ctx context.Context, showtimeID, userID uuid.UUID) (*dto.QueueStatusResponse, error)
	Leave(ctx context.Context, showtimeID, userID uuid.UUID) error
	Status(ctx context.Context, showtimeID, userID uuid.UUID) (*dto.QueueStatusResponse, error)
	// RequireActiveBookingRoom returns nil if the showtime has no queue, or the user holds an active seat-selection slot.
	RequireActiveBookingRoom(ctx context.Context, showtimeID, userID uuid.UUID) error
}

// QueueManager is the subset of *redisqueue.Manager used by the virtual queue service.
type QueueManager interface {
	Join(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error)
	Heartbeat(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error)
	Leave(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) error
	Status(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error)
	HasActiveSession(ctx context.Context, showtimeID, userID uuid.UUID) (bool, error)
}

type virtualQueueService struct {
	logger           *zap.Logger
	queue            QueueManager
	repo             repository.BookingRepository
	fallbackActiveLn int
}

func NewVirtualQueueService(logger *zap.Logger, queue *redisqueue.Manager, repo repository.BookingRepository, fallbackActiveLimit int) VirtualQueueService {
	if queue == nil {
		return nil
	}
	if fallbackActiveLimit <= 0 {
		fallbackActiveLimit = 50
	}
	return &virtualQueueService{
		logger:           logger,
		queue:            queue,
		repo:             repo,
		fallbackActiveLn: fallbackActiveLimit,
	}
}

// newVirtualQueueServiceWithQM is a test-friendly constructor that accepts the QueueManager interface.
func newVirtualQueueServiceWithQM(logger *zap.Logger, queue QueueManager, repo repository.BookingRepository, fallbackActiveLimit int) VirtualQueueService {
	if queue == nil {
		return nil
	}
	if fallbackActiveLimit <= 0 {
		fallbackActiveLimit = 50
	}
	return &virtualQueueService{
		logger:           logger,
		queue:            queue,
		repo:             repo,
		fallbackActiveLn: fallbackActiveLimit,
	}
}

func syntheticBypass(showtimeID, userID uuid.UUID) *dto.QueueStatusResponse {
	return &dto.QueueStatusResponse{
		ShowtimeID:   showtimeID.String(),
		UserID:       userID.String(),
		Position:     0,
		TotalWaiting: 0,
		InQueue:      false,
		CanEnter:     true,
	}
}

func (s *virtualQueueService) clampActive(limit int) int64 {
	n := limit
	if n <= 0 {
		n = s.fallbackActiveLn
	}
	if n <= 0 {
		n = 50
	}
	if n > 10000 {
		n = 10000
	}
	return int64(n)
}

func (s *virtualQueueService) Join(ctx context.Context, showtimeID, userID uuid.UUID) (*dto.QueueStatusResponse, error) {
	enabled, limit, err := s.repo.GetShowtimeQueueSettings(showtimeID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return syntheticBypass(showtimeID, userID), nil
	}
	maxActive := s.clampActive(limit)

	status, err := s.queue.Join(ctx, showtimeID, userID, maxActive)
	if err != nil {
		return nil, apperror.NewInternal("failed to join queue", err)
	}
	return &dto.QueueStatusResponse{
		ShowtimeID:   showtimeID.String(),
		UserID:       userID.String(),
		Position:     status.Position,
		TotalWaiting: status.TotalWaiting,
		InQueue:      status.InQueue,
		CanEnter:     status.CanEnter,
	}, nil
}

func (s *virtualQueueService) Heartbeat(ctx context.Context, showtimeID, userID uuid.UUID) (*dto.QueueStatusResponse, error) {
	enabled, limit, err := s.repo.GetShowtimeQueueSettings(showtimeID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return syntheticBypass(showtimeID, userID), nil
	}
	maxActive := s.clampActive(limit)

	status, err := s.queue.Heartbeat(ctx, showtimeID, userID, maxActive)
	if err != nil {
		if errors.Is(err, redisqueue.ErrNotInQueue) {
			return nil, apperror.NewConflict(QueueKickMessage)
		}
		return nil, apperror.NewInternal("failed to heartbeat queue", err)
	}
	return &dto.QueueStatusResponse{
		ShowtimeID:   showtimeID.String(),
		UserID:       userID.String(),
		Position:     status.Position,
		TotalWaiting: status.TotalWaiting,
		InQueue:      status.InQueue,
		CanEnter:     status.CanEnter,
	}, nil
}

func (s *virtualQueueService) Leave(ctx context.Context, showtimeID, userID uuid.UUID) error {
	enabled, limit, err := s.repo.GetShowtimeQueueSettings(showtimeID)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	maxActive := s.clampActive(limit)
	if err := s.queue.Leave(ctx, showtimeID, userID, maxActive); err != nil {
		return apperror.NewInternal("failed to leave queue", err)
	}
	return nil
}

func (s *virtualQueueService) RequireActiveBookingRoom(ctx context.Context, showtimeID, userID uuid.UUID) error {
	enabled, _, err := s.repo.GetShowtimeQueueSettings(showtimeID)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	active, err := s.queue.HasActiveSession(ctx, showtimeID, userID)
	if err != nil {
		return apperror.NewInternal("failed to verify booking room session", err)
	}
	if !active {
		return apperror.NewForbidden("use the waiting room and enter when your turn before holding seats for this showtime")
	}
	return nil
}

func (s *virtualQueueService) Status(ctx context.Context, showtimeID, userID uuid.UUID) (*dto.QueueStatusResponse, error) {
	enabled, limit, err := s.repo.GetShowtimeQueueSettings(showtimeID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return syntheticBypass(showtimeID, userID), nil
	}
	maxActive := s.clampActive(limit)

	status, err := s.queue.Status(ctx, showtimeID, userID, maxActive)
	if err != nil {
		if errors.Is(err, redisqueue.ErrNotInQueue) {
			return nil, apperror.NewConflict(QueueKickMessage)
		}
		return nil, apperror.NewInternal("failed to get queue status", err)
	}
	return &dto.QueueStatusResponse{
		ShowtimeID:   showtimeID.String(),
		UserID:       userID.String(),
		Position:     status.Position,
		TotalWaiting: status.TotalWaiting,
		InQueue:      status.InQueue,
		CanEnter:     status.CanEnter,
	}, nil
}
