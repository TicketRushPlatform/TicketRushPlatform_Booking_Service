package redislock

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const acquireSeatsScript = `
for i, key in ipairs(KEYS) do
	if redis.call("EXISTS", key) == 1 then
		return 0
	end
end

for i, key in ipairs(KEYS) do
	redis.call("SET", key, ARGV[1], "PX", ARGV[2])
end

return 1
`

const releaseSeatsScript = `
for i, key in ipairs(KEYS) do
	if redis.call("GET", key) == ARGV[1] then
		redis.call("DEL", key)
	end
end

return 1
`

type Config struct {
	Addr     string
	Password string
	DB       int
	TTL      time.Duration
}

type SeatLocker struct {
	client *redis.Client
	ttl    time.Duration
}

func NewClient(cfg Config) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
}

func NewSeatLocker(client *redis.Client, ttl time.Duration) *SeatLocker {
	if ttl <= 0 {
		ttl = 5 * time.Second
	}

	return &SeatLocker{
		client: client,
		ttl:    ttl,
	}
}

func (l *SeatLocker) LockSeats(ctx context.Context, showtimeID uuid.UUID, seatIDs []uuid.UUID, owner string) (bool, error) {
	keys := seatKeys(showtimeID, seatIDs)
	result, err := l.client.Eval(ctx, acquireSeatsScript, keys, owner, l.ttl.Milliseconds()).Int()
	if err != nil {
		return false, err
	}

	return result == 1, nil
}

func (l *SeatLocker) UnlockSeats(ctx context.Context, showtimeID uuid.UUID, seatIDs []uuid.UUID, owner string) error {
	keys := seatKeys(showtimeID, seatIDs)
	return l.client.Eval(ctx, releaseSeatsScript, keys, owner).Err()
}

func (l *SeatLocker) Close() error {
	return l.client.Close()
}

func seatKeys(showtimeID uuid.UUID, seatIDs []uuid.UUID) []string {
	keys := make([]string, 0, len(seatIDs))
	for _, seatID := range seatIDs {
		keys = append(keys, fmt.Sprintf("booking:seat-lock:%s:%s", showtimeID, seatID))
	}
	return keys
}
