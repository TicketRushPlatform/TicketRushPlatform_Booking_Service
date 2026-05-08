package redislock

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
)

func TestSeatLocker_LockSeatsAtomicConflictAndRelease(t *testing.T) {
	server := miniredis.RunT(t)
	client := NewClient(Config{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	locker := NewSeatLocker(client, 30*time.Second)
	ctx := context.Background()
	showtimeID := uuid.New()
	seat1 := uuid.New()
	seat2 := uuid.New()

	locked, err := locker.LockSeats(ctx, showtimeID, []uuid.UUID{seat1, seat2}, "owner-1")
	if err != nil {
		t.Fatalf("LockSeats first err=%v", err)
	}
	if !locked {
		t.Fatalf("expected first lock to succeed")
	}

	locked, err = locker.LockSeats(ctx, showtimeID, []uuid.UUID{seat2}, "owner-2")
	if err != nil {
		t.Fatalf("LockSeats second err=%v", err)
	}
	if locked {
		t.Fatalf("expected overlapping lock to fail")
	}

	seat3 := uuid.New()
	locked, err = locker.LockSeats(ctx, showtimeID, []uuid.UUID{seat2, seat3}, "owner-2")
	if err != nil {
		t.Fatalf("LockSeats partial overlap err=%v", err)
	}
	if locked {
		t.Fatalf("expected partial overlapping lock to fail atomically")
	}
	if server.Exists(seatKey(showtimeID, seat3)) {
		t.Fatalf("partial overlap should not create a lock for the free seat")
	}

	if err := locker.UnlockSeats(ctx, showtimeID, []uuid.UUID{seat1, seat2}, "wrong-owner"); err != nil {
		t.Fatalf("UnlockSeats wrong owner err=%v", err)
	}
	if !server.Exists(seatKey(showtimeID, seat1)) || !server.Exists(seatKey(showtimeID, seat2)) {
		t.Fatalf("wrong owner must not delete locks")
	}

	if err := locker.UnlockSeats(ctx, showtimeID, []uuid.UUID{seat1, seat2}, "owner-1"); err != nil {
		t.Fatalf("UnlockSeats owner err=%v", err)
	}
	if server.Exists(seatKey(showtimeID, seat1)) || server.Exists(seatKey(showtimeID, seat2)) {
		t.Fatalf("owner unlock should delete locks")
	}

	locked, err = locker.LockSeats(ctx, showtimeID, []uuid.UUID{seat2}, "owner-2")
	if err != nil {
		t.Fatalf("LockSeats after release err=%v", err)
	}
	if !locked {
		t.Fatalf("expected lock after release to succeed")
	}
}

func TestSeatLocker_LockSeatsTTL(t *testing.T) {
	server := miniredis.RunT(t)
	client := NewClient(Config{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	locker := NewSeatLocker(client, time.Second)
	ctx := context.Background()
	showtimeID := uuid.New()
	seatID := uuid.New()

	locked, err := locker.LockSeats(ctx, showtimeID, []uuid.UUID{seatID}, "owner")
	if err != nil {
		t.Fatalf("LockSeats err=%v", err)
	}
	if !locked {
		t.Fatalf("expected lock to succeed")
	}

	server.FastForward(1100 * time.Millisecond)

	locked, err = locker.LockSeats(ctx, showtimeID, []uuid.UUID{seatID}, "new-owner")
	if err != nil {
		t.Fatalf("LockSeats after TTL err=%v", err)
	}
	if !locked {
		t.Fatalf("expected lock after TTL to succeed")
	}
}

func seatKey(showtimeID uuid.UUID, seatID uuid.UUID) string {
	return fmt.Sprintf("booking:seat-lock:%s:%s", showtimeID, seatID)
}
