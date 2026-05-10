package services

import (
	"booking_api/internal/apperror"
	"booking_api/internal/redisqueue"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ---------- mock QueueManager ----------

type queueManagerMock struct {
	joinFn             func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error)
	heartbeatFn        func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error)
	leaveFn            func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) error
	statusFn           func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error)
	hasActiveSessionFn func(ctx context.Context, showtimeID, userID uuid.UUID) (bool, error)
}

func (m *queueManagerMock) Join(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error) {
	return m.joinFn(ctx, showtimeID, userID, maxActive)
}
func (m *queueManagerMock) Heartbeat(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error) {
	return m.heartbeatFn(ctx, showtimeID, userID, maxActive)
}
func (m *queueManagerMock) Leave(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) error {
	return m.leaveFn(ctx, showtimeID, userID, maxActive)
}
func (m *queueManagerMock) Status(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error) {
	return m.statusFn(ctx, showtimeID, userID, maxActive)
}
func (m *queueManagerMock) HasActiveSession(ctx context.Context, showtimeID, userID uuid.UUID) (bool, error) {
	return m.hasActiveSessionFn(ctx, showtimeID, userID)
}

// ---------- mock BookingRepository (queue-settings only) ----------

type queueRepoMock struct {
	bookingRepoMock
	getShowtimeQueueSettingsFnOverride func(showtimeID uuid.UUID) (bool, int, error)
}

func (m *queueRepoMock) GetShowtimeQueueSettings(showtimeID uuid.UUID) (bool, int, error) {
	if m.getShowtimeQueueSettingsFnOverride != nil {
		return m.getShowtimeQueueSettingsFnOverride(showtimeID)
	}
	return false, 50, nil
}

// ---------- NewVirtualQueueService ----------

func TestNewVirtualQueueService_NilQueue(t *testing.T) {
	svc := NewVirtualQueueService(zap.NewNop(), nil, &bookingRepoMock{}, 10)
	if svc != nil {
		t.Fatalf("expected nil for nil queue")
	}
}

func TestNewVirtualQueueServiceWithQM_NilQueue(t *testing.T) {
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), nil, &bookingRepoMock{}, 10)
	if svc != nil {
		t.Fatalf("expected nil for nil queue")
	}
}

func TestNewVirtualQueueServiceWithQM_ZeroFallback(t *testing.T) {
	qm := &queueManagerMock{}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, &bookingRepoMock{}, 0)
	if svc == nil {
		t.Fatalf("expected non-nil service")
	}
	vs := svc.(*virtualQueueService)
	if vs.fallbackActiveLn != 50 {
		t.Fatalf("expected fallback=50, got %d", vs.fallbackActiveLn)
	}
}

func TestNewVirtualQueueServiceWithQM_NegativeFallback(t *testing.T) {
	qm := &queueManagerMock{}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, &bookingRepoMock{}, -5)
	if svc == nil {
		t.Fatalf("expected non-nil service")
	}
	vs := svc.(*virtualQueueService)
	if vs.fallbackActiveLn != 50 {
		t.Fatalf("expected fallback=50, got %d", vs.fallbackActiveLn)
	}
}

// ---------- clampActive ----------

func TestClampActive(t *testing.T) {
	qm := &queueManagerMock{}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, &bookingRepoMock{}, 30).(*virtualQueueService)

	tests := []struct {
		name  string
		limit int
		want  int64
	}{
		{"normal", 100, 100},
		{"zero uses fallback", 0, 30},
		{"negative uses fallback", -1, 30},
		{"above cap", 20000, 10000},
		{"at cap", 10000, 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.clampActive(tt.limit)
			if got != tt.want {
				t.Fatalf("clampActive(%d) = %d, want %d", tt.limit, got, tt.want)
			}
		})
	}
}

func TestClampActive_ZeroFallback(t *testing.T) {
	qm := &queueManagerMock{}
	vs := &virtualQueueService{
		logger:           zap.NewNop(),
		queue:            qm,
		repo:             &bookingRepoMock{},
		fallbackActiveLn: 0,
	}
	got := vs.clampActive(0)
	if got != 50 {
		t.Fatalf("expected hardcoded fallback 50, got %d", got)
	}
}

// ---------- syntheticBypass ----------

func TestSyntheticBypass(t *testing.T) {
	sid := uuid.New()
	uid := uuid.New()
	r := syntheticBypass(sid, uid)
	if r.ShowtimeID != sid.String() || r.UserID != uid.String() {
		t.Fatalf("unexpected ids")
	}
	if r.Position != 0 || r.TotalWaiting != 0 || r.InQueue || !r.CanEnter {
		t.Fatalf("unexpected bypass fields: %+v", r)
	}
}

// ---------- Join ----------

func TestVirtualQueueService_Join_Disabled(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return false, 0, nil
		},
	}
	qm := &queueManagerMock{}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	resp, err := svc.Join(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.InQueue || !resp.CanEnter {
		t.Fatalf("expected bypass: %+v", resp)
	}
}

func TestVirtualQueueService_Join_RepoError(t *testing.T) {
	repoErr := errors.New("db down")
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return false, 0, repoErr
		},
	}
	qm := &queueManagerMock{}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	_, err := svc.Join(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, repoErr) {
		t.Fatalf("expected repo error, got %v", err)
	}
}

func TestVirtualQueueService_Join_Success(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return true, 20, nil
		},
	}
	qm := &queueManagerMock{
		joinFn: func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error) {
			return &redisqueue.Status{Position: 0, TotalWaiting: 5, InQueue: false, CanEnter: true}, nil
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	resp, err := svc.Join(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalWaiting != 5 || !resp.CanEnter {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestVirtualQueueService_Join_QueueError(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return true, 10, nil
		},
	}
	qm := &queueManagerMock{
		joinFn: func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error) {
			return nil, errors.New("redis down")
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	_, err := svc.Join(context.Background(), uuid.New(), uuid.New())
	if err == nil || !strings.Contains(err.Error(), "failed to join queue") {
		t.Fatalf("expected join error, got %v", err)
	}
}

// ---------- Heartbeat ----------

func TestVirtualQueueService_Heartbeat_Disabled(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return false, 0, nil
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), &queueManagerMock{}, repo, 50)

	resp, err := svc.Heartbeat(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.InQueue || !resp.CanEnter {
		t.Fatalf("expected bypass: %+v", resp)
	}
}

func TestVirtualQueueService_Heartbeat_RepoError(t *testing.T) {
	repoErr := errors.New("db down")
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return false, 0, repoErr
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), &queueManagerMock{}, repo, 50)

	_, err := svc.Heartbeat(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, repoErr) {
		t.Fatalf("expected repo error, got %v", err)
	}
}

func TestVirtualQueueService_Heartbeat_Success(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return true, 20, nil
		},
	}
	qm := &queueManagerMock{
		heartbeatFn: func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error) {
			return &redisqueue.Status{Position: 0, TotalWaiting: 3, InQueue: false, CanEnter: true}, nil
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	resp, err := svc.Heartbeat(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalWaiting != 3 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestVirtualQueueService_Heartbeat_NotInQueue(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return true, 10, nil
		},
	}
	qm := &queueManagerMock{
		heartbeatFn: func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error) {
			return nil, redisqueue.ErrNotInQueue
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	_, err := svc.Heartbeat(context.Background(), uuid.New(), uuid.New())
	if err == nil {
		t.Fatalf("expected error")
	}
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Code != 409 {
		t.Fatalf("expected conflict app error, got %v", err)
	}
}

func TestVirtualQueueService_Heartbeat_QueueError(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return true, 10, nil
		},
	}
	qm := &queueManagerMock{
		heartbeatFn: func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error) {
			return nil, errors.New("redis down")
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	_, err := svc.Heartbeat(context.Background(), uuid.New(), uuid.New())
	if err == nil || !strings.Contains(err.Error(), "failed to heartbeat queue") {
		t.Fatalf("expected heartbeat error, got %v", err)
	}
}

// ---------- Leave ----------

func TestVirtualQueueService_Leave_Disabled(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return false, 0, nil
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), &queueManagerMock{}, repo, 50)

	err := svc.Leave(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVirtualQueueService_Leave_RepoError(t *testing.T) {
	repoErr := errors.New("db down")
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return false, 0, repoErr
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), &queueManagerMock{}, repo, 50)

	err := svc.Leave(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, repoErr) {
		t.Fatalf("expected repo error, got %v", err)
	}
}

func TestVirtualQueueService_Leave_Success(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return true, 10, nil
		},
	}
	qm := &queueManagerMock{
		leaveFn: func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) error {
			return nil
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	err := svc.Leave(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVirtualQueueService_Leave_QueueError(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return true, 10, nil
		},
	}
	qm := &queueManagerMock{
		leaveFn: func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) error {
			return errors.New("redis down")
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	err := svc.Leave(context.Background(), uuid.New(), uuid.New())
	if err == nil || !strings.Contains(err.Error(), "failed to leave queue") {
		t.Fatalf("expected leave error, got %v", err)
	}
}

// ---------- RequireActiveBookingRoom ----------

func TestVirtualQueueService_RequireActiveBookingRoom_Disabled(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return false, 0, nil
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), &queueManagerMock{}, repo, 50)

	err := svc.RequireActiveBookingRoom(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVirtualQueueService_RequireActiveBookingRoom_RepoError(t *testing.T) {
	repoErr := errors.New("db down")
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return false, 0, repoErr
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), &queueManagerMock{}, repo, 50)

	err := svc.RequireActiveBookingRoom(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, repoErr) {
		t.Fatalf("expected repo error, got %v", err)
	}
}

func TestVirtualQueueService_RequireActiveBookingRoom_Active(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return true, 10, nil
		},
	}
	qm := &queueManagerMock{
		hasActiveSessionFn: func(ctx context.Context, showtimeID, userID uuid.UUID) (bool, error) {
			return true, nil
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	err := svc.RequireActiveBookingRoom(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVirtualQueueService_RequireActiveBookingRoom_NotActive(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return true, 10, nil
		},
	}
	qm := &queueManagerMock{
		hasActiveSessionFn: func(ctx context.Context, showtimeID, userID uuid.UUID) (bool, error) {
			return false, nil
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	err := svc.RequireActiveBookingRoom(context.Background(), uuid.New(), uuid.New())
	if err == nil {
		t.Fatalf("expected forbidden error")
	}
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Code != 403 {
		t.Fatalf("expected 403 forbidden, got %v", err)
	}
}

func TestVirtualQueueService_RequireActiveBookingRoom_QueueError(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return true, 10, nil
		},
	}
	qm := &queueManagerMock{
		hasActiveSessionFn: func(ctx context.Context, showtimeID, userID uuid.UUID) (bool, error) {
			return false, errors.New("redis down")
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	err := svc.RequireActiveBookingRoom(context.Background(), uuid.New(), uuid.New())
	if err == nil || !strings.Contains(err.Error(), "failed to verify booking room session") {
		t.Fatalf("expected internal error, got %v", err)
	}
}

// ---------- Status ----------

func TestVirtualQueueService_Status_Disabled(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return false, 0, nil
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), &queueManagerMock{}, repo, 50)

	resp, err := svc.Status(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.InQueue || !resp.CanEnter {
		t.Fatalf("expected bypass: %+v", resp)
	}
}

func TestVirtualQueueService_Status_RepoError(t *testing.T) {
	repoErr := errors.New("db down")
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return false, 0, repoErr
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), &queueManagerMock{}, repo, 50)

	_, err := svc.Status(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, repoErr) {
		t.Fatalf("expected repo error, got %v", err)
	}
}

func TestVirtualQueueService_Status_Success(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return true, 20, nil
		},
	}
	qm := &queueManagerMock{
		statusFn: func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error) {
			return &redisqueue.Status{Position: 3, TotalWaiting: 10, InQueue: true, CanEnter: false}, nil
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	resp, err := svc.Status(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Position != 3 || resp.TotalWaiting != 10 || !resp.InQueue || resp.CanEnter {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestVirtualQueueService_Status_NotInQueue(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return true, 10, nil
		},
	}
	qm := &queueManagerMock{
		statusFn: func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error) {
			return nil, redisqueue.ErrNotInQueue
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	_, err := svc.Status(context.Background(), uuid.New(), uuid.New())
	if err == nil {
		t.Fatalf("expected error")
	}
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Code != 409 {
		t.Fatalf("expected conflict app error, got %v", err)
	}
}

func TestVirtualQueueService_Status_QueueError(t *testing.T) {
	repo := &queueRepoMock{
		getShowtimeQueueSettingsFnOverride: func(showtimeID uuid.UUID) (bool, int, error) {
			return true, 10, nil
		},
	}
	qm := &queueManagerMock{
		statusFn: func(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*redisqueue.Status, error) {
			return nil, errors.New("redis down")
		},
	}
	svc := newVirtualQueueServiceWithQM(zap.NewNop(), qm, repo, 50)

	_, err := svc.Status(context.Background(), uuid.New(), uuid.New())
	if err == nil || !strings.Contains(err.Error(), "failed to get queue status") {
		t.Fatalf("expected status error, got %v", err)
	}
}

// ---------- QueueKickMessage ----------

func TestQueueKickMessage(t *testing.T) {
	if QueueKickMessage == "" {
		t.Fatalf("QueueKickMessage should not be empty")
	}
}
