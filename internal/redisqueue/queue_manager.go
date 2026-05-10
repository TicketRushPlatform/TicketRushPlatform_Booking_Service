package redisqueue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

var ErrNotInQueue = errors.New("user is not in queue")

type Manager struct {
	client *redis.Client
	ttl    time.Duration
	logger *zap.Logger
}

type Status struct {
	Position     int64
	TotalWaiting int64
	InQueue      bool
	CanEnter     bool
}

// joinLua runs atomically: refresh active slot, admit if SCARD(active) < maxActive, otherwise enqueue.
var joinLua = redis.NewScript(`
local activeMembers = KEYS[1]
local activeSession = KEYS[2]
local queueKey = KEYS[3]
local queueSession = KEYS[4]

local maxActive = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])
local userId = ARGV[3]

if maxActive == nil or maxActive < 1 then
  maxActive = 50
end
if ttl == nil or ttl < 1 then
  ttl = 30
end

-- Session key alone is not enough: it must match membership in activeMembers (avoids ghost keys after leave/promotion).
if redis.call('EXISTS', activeSession) == 1 then
  if redis.call('SISMEMBER', activeMembers, userId) == 1 then
    redis.call('EXPIRE', activeSession, ttl)
    return 'active_refresh'
  end
  redis.call('DEL', activeSession)
end

local n = redis.call('SCARD', activeMembers)
if n < maxActive then
  redis.call('SET', activeSession, '1', 'EX', ttl)
  redis.call('SADD', activeMembers, userId)
  redis.call('ZREM', queueKey, userId)
  redis.call('DEL', queueSession)
  return 'active_new'
end

redis.call('SET', queueSession, '1', 'EX', ttl)
local t = redis.call('TIME')
local score = tonumber(t[1]) * 1000000 + tonumber(t[2])
redis.call('ZADD', queueKey, 'NX', score, userId)
return 'queued'
`)

var fillSlotsLua = redis.NewScript(`
local activeKey = KEYS[1]
local queueKey = KEYS[2]
local maxActive = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])
local prefix = ARGV[3]

if maxActive == nil or maxActive < 1 then
  maxActive = 50
end
if ttl == nil or ttl < 1 then
  ttl = 30
end

while true do
  local n = redis.call('SCARD', activeKey)
  if n >= maxActive then
    break
  end
  local heads = redis.call('ZRANGE', queueKey, 0, 0)
  if #heads == 0 then
    break
  end
  local m = heads[1]
  local qSess = prefix .. 'user:' .. m
  if redis.call('EXISTS', qSess) == 0 then
    redis.call('ZREM', queueKey, m)
  else
    local aSess = prefix .. 'active-user:' .. m
    redis.call('SET', aSess, '1', 'EX', ttl)
    redis.call('SADD', activeKey, m)
    redis.call('ZREM', queueKey, m)
    redis.call('DEL', qSess)
  end
end
return 'ok'
`)

func NewManager(client *redis.Client, ttl time.Duration, logger *zap.Logger) *Manager {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Manager{
		client: client,
		ttl:    ttl,
		logger: logger,
	}
}

func (m *Manager) logDebug(msg string, fields ...zap.Field) {
	if m == nil || m.logger == nil {
		return
	}
	m.logger.Debug(msg, fields...)
}

func redisKeyPrefix(showtimeID uuid.UUID) string {
	return fmt.Sprintf("booking:queue:%s:", showtimeID.String())
}

func queueOpLockKey(showtimeID uuid.UUID) string {
	return fmt.Sprintf("booking:queue:%s:op-lock", showtimeID.String())
}

// withQueueOpLock serializes Join / Leave / Status / Heartbeat for one showtime so no request
// can observe SCARD=0 between Leave's member removal and fillSlots promoting the next waiter.
func (m *Manager) withQueueOpLock(ctx context.Context, showtimeID uuid.UUID, fn func() error) error {
	lockKey := queueOpLockKey(showtimeID)
	const lockTTL = 15 * time.Second
	const spin = 5 * time.Millisecond
	deadline := time.Now().Add(3 * time.Second)
	for {
		ok, err := m.client.SetNX(ctx, lockKey, "1", lockTTL).Result()
		if err != nil {
			return err
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("queue operation lock timeout for showtime %s", showtimeID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(spin):
		}
	}
	defer func() { _ = m.client.Del(ctx, lockKey).Err() }()
	return fn()
}

func (m *Manager) drainPromotionLoop(ctx context.Context, showtimeID uuid.UUID, maxActive int64) error {
	qk := queueMembersKey(showtimeID)
	activeKey := activeMembersKey(showtimeID)
	if maxActive < 1 {
		maxActive = 1
	}
	ttlArg := int64(m.ttl / time.Second)
	if ttlArg < 1 {
		ttlArg = 30
	}
	return fillSlotsLua.Run(ctx, m.client, []string{activeKey, qk}, maxActive, ttlArg, redisKeyPrefix(showtimeID)).Err()
}

func (m *Manager) Join(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*Status, error) {
	m.logDebug("redisqueue join begin",
		zap.String("showtime_id", showtimeID.String()),
		zap.String("user_id", userID.String()),
		zap.Int64("max_active", maxActive),
		zap.Duration("session_ttl", m.ttl),
	)

	var out *Status
	lockErr := m.withQueueOpLock(ctx, showtimeID, func() error {
		if err := m.pruneExpired(ctx, showtimeID, maxActive); err != nil {
			m.logDebug("redisqueue join prune_expired failed", zap.Error(err),
				zap.String("showtime_id", showtimeID.String()),
				zap.String("user_id", userID.String()))
			return err
		}

		qKey := queueMembersKey(showtimeID)
		aMembers := activeMembersKey(showtimeID)
		aSession := activeSessionKey(showtimeID, userID)
		qSession := queueSessionKey(showtimeID, userID)

		ma := maxActive
		if ma < 1 {
			ma = 1
		}
		ttlArg := int64(m.ttl / time.Second)
		if ttlArg < 1 {
			ttlArg = 30
		}

		tag, err := joinLua.Run(ctx, m.client, []string{aMembers, aSession, qKey, qSession}, ma, ttlArg, userID.String()).Text()
		if err != nil {
			m.logDebug("redisqueue join lua failed", zap.Error(err),
				zap.String("showtime_id", showtimeID.String()),
				zap.String("user_id", userID.String()))
			return err
		}

		m.logDebug("redisqueue join lua result",
			zap.String("showtime_id", showtimeID.String()),
			zap.String("user_id", userID.String()),
			zap.String("lua_tag", tag),
		)

		totalWaiting, err := m.client.ZCard(ctx, qKey).Result()
		if err != nil {
			m.logDebug("redisqueue join zcard failed", zap.Error(err),
				zap.String("showtime_id", showtimeID.String()))
			return err
		}

		switch tag {
		case "active_refresh", "active_new":
			out = &Status{Position: 0, TotalWaiting: totalWaiting, InQueue: false, CanEnter: true}
		case "queued":
			st, err := m.positionAndSize(ctx, showtimeID, userID)
			if err != nil {
				m.logDebug("redisqueue join position failed", zap.Error(err),
					zap.String("showtime_id", showtimeID.String()),
					zap.String("user_id", userID.String()))
				return err
			}
			out = st
		default:
			m.logDebug("redisqueue join unexpected lua tag",
				zap.String("showtime_id", showtimeID.String()),
				zap.String("user_id", userID.String()),
				zap.String("lua_tag", tag))
			return fmt.Errorf("unexpected join script result %q", tag)
		}

		m.logDebug("redisqueue join done",
			zap.String("showtime_id", showtimeID.String()),
			zap.String("user_id", userID.String()),
			zap.String("lua_tag", tag),
			zap.Bool("in_queue", out.InQueue),
			zap.Bool("can_enter", out.CanEnter),
			zap.Int64("position", out.Position),
			zap.Int64("total_waiting", out.TotalWaiting),
		)
		return nil
	})
	if lockErr != nil {
		return nil, lockErr
	}
	return out, nil
}

func (m *Manager) Heartbeat(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*Status, error) {
	var out *Status
	err := m.withQueueOpLock(ctx, showtimeID, func() error {
		activeSess := activeSessionKey(showtimeID, userID)
		inRoom, err := m.userHasLegitimateActiveSlot(ctx, showtimeID, userID)
		if err != nil {
			return err
		}
		if inRoom {
			if err := m.client.Expire(ctx, activeSess, m.ttl).Err(); err != nil {
				return err
			}
			totalWaiting, err := m.client.ZCard(ctx, queueMembersKey(showtimeID)).Result()
			if err != nil {
				return err
			}
			out = &Status{Position: 0, TotalWaiting: totalWaiting, InQueue: false, CanEnter: true}
			return nil
		}

		sessionKey := queueSessionKey(showtimeID, userID)
		exists, err := m.client.Exists(ctx, sessionKey).Result()
		if err != nil {
			return err
		}
		if exists == 0 {
			_ = m.client.ZRem(ctx, queueMembersKey(showtimeID), userID.String()).Err()
			return ErrNotInQueue
		}
		if err := m.client.Expire(ctx, sessionKey, m.ttl).Err(); err != nil {
			return err
		}

		if err := m.drainPromotionLoop(ctx, showtimeID, maxActive); err != nil {
			return err
		}

		st, err := m.positionAndSize(ctx, showtimeID, userID)
		if err != nil {
			return err
		}
		out = st
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotInQueue) {
			return nil, ErrNotInQueue
		}
		return nil, err
	}
	return out, nil
}

func (m *Manager) Leave(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) error {
	m.logDebug("redisqueue leave begin",
		zap.String("showtime_id", showtimeID.String()),
		zap.String("user_id", userID.String()),
		zap.Int64("max_active", maxActive),
	)

	return m.withQueueOpLock(ctx, showtimeID, func() error {
		queueKey := queueMembersKey(showtimeID)
		sessionKey := queueSessionKey(showtimeID, userID)
		activeKey := activeMembersKey(showtimeID)
		activeSession := activeSessionKey(showtimeID, userID)

		pipe := m.client.TxPipeline()
		pipe.Del(ctx, sessionKey)
		pipe.ZRem(ctx, queueKey, userID.String())
		pipe.Del(ctx, activeSession)
		pipe.SRem(ctx, activeKey, userID.String())
		_, err := pipe.Exec(ctx)
		if err != nil {
			m.logDebug("redisqueue leave pipeline failed", zap.Error(err),
				zap.String("showtime_id", showtimeID.String()),
				zap.String("user_id", userID.String()))
			return err
		}

		m.logDebug("redisqueue leave removed user from queue and active set",
			zap.String("showtime_id", showtimeID.String()),
			zap.String("user_id", userID.String()),
		)

		if err := m.drainPromotionLoop(ctx, showtimeID, maxActive); err != nil {
			m.logDebug("redisqueue leave fill_slots failed", zap.Error(err),
				zap.String("showtime_id", showtimeID.String()))
			return err
		}

		waiting, wErr := m.client.ZCard(ctx, queueKey).Result()
		activeN, aErr := m.client.SCard(ctx, activeKey).Result()
		fields := []zap.Field{
			zap.String("showtime_id", showtimeID.String()),
			zap.String("user_id", userID.String()),
		}
		if wErr == nil {
			fields = append(fields, zap.Int64("queue_size_after", waiting))
		} else {
			fields = append(fields, zap.NamedError("zcard_err", wErr))
		}
		if aErr == nil {
			fields = append(fields, zap.Int64("active_count_after", activeN))
		} else {
			fields = append(fields, zap.NamedError("scard_err", aErr))
		}
		m.logDebug("redisqueue leave done", fields...)

		return nil
	})
}

func (m *Manager) Status(ctx context.Context, showtimeID, userID uuid.UUID, maxActive int64) (*Status, error) {
	var out *Status
	err := m.withQueueOpLock(ctx, showtimeID, func() error {
		if err := m.pruneExpired(ctx, showtimeID, maxActive); err != nil {
			return err
		}

		inRoom, err := m.userHasLegitimateActiveSlot(ctx, showtimeID, userID)
		if err != nil {
			return err
		}
		if inRoom {
			totalWaiting, err := m.client.ZCard(ctx, queueMembersKey(showtimeID)).Result()
			if err != nil {
				return err
			}
			out = &Status{Position: 0, TotalWaiting: totalWaiting, InQueue: false, CanEnter: true}
			return nil
		}

		if err := m.drainPromotionLoop(ctx, showtimeID, maxActive); err != nil {
			return err
		}
		st, err := m.positionAndSize(ctx, showtimeID, userID)
		if err != nil {
			return err
		}
		out = st
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func activeMembersKey(showtimeID uuid.UUID) string {
	return fmt.Sprintf("booking:queue:%s:active-members", showtimeID.String())
}

func activeSessionKey(showtimeID, userID uuid.UUID) string {
	return fmt.Sprintf("booking:queue:%s:active-user:%s", showtimeID.String(), userID.String())
}

// HasActiveSession reports whether the user currently holds an active "booking room" slot (seat selection).
func (m *Manager) HasActiveSession(ctx context.Context, showtimeID, userID uuid.UUID) (bool, error) {
	var inRoom bool
	err := m.withQueueOpLock(ctx, showtimeID, func() error {
		var errInner error
		inRoom, errInner = m.userHasLegitimateActiveSlot(ctx, showtimeID, userID)
		return errInner
	})
	return inRoom, err
}

// userHasLegitimateActiveSlot is true only when both the per-user session key and active-members set agree.
// If the session key exists but the user is not in the set (stale ghost), the key is deleted and false is returned.
func (m *Manager) userHasLegitimateActiveSlot(ctx context.Context, showtimeID, userID uuid.UUID) (bool, error) {
	sess := activeSessionKey(showtimeID, userID)
	n, err := m.client.Exists(ctx, sess).Result()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	inSet, err := m.client.SIsMember(ctx, activeMembersKey(showtimeID), userID.String()).Result()
	if err != nil {
		return false, err
	}
	if !inSet {
		_ = m.client.Del(ctx, sess).Err()
		return false, nil
	}
	return true, nil
}

func (m *Manager) positionAndSize(ctx context.Context, showtimeID, userID uuid.UUID) (*Status, error) {
	queueKey := queueMembersKey(showtimeID)
	sessionKey := queueSessionKey(showtimeID, userID)

	inRoom, err := m.userHasLegitimateActiveSlot(ctx, showtimeID, userID)
	if err != nil {
		return nil, err
	}
	if inRoom {
		totalWaiting, err := m.client.ZCard(ctx, queueKey).Result()
		if err != nil {
			return nil, err
		}
		return &Status{Position: 0, TotalWaiting: totalWaiting, InQueue: false, CanEnter: true}, nil
	}

	existsQueue, err := m.client.Exists(ctx, sessionKey).Result()
	if err != nil {
		return nil, err
	}
	if existsQueue == 0 {
		_ = m.client.ZRem(ctx, queueKey, userID.String()).Err()
		return nil, ErrNotInQueue
	}

	rank, err := m.client.ZRank(ctx, queueKey, userID.String()).Result()
	if err == redis.Nil {
		return nil, ErrNotInQueue
	}
	if err != nil {
		return nil, err
	}

	total, err := m.client.ZCard(ctx, queueKey).Result()
	if err != nil {
		return nil, err
	}

	return &Status{
		Position:     rank + 1,
		TotalWaiting: total,
		InQueue:      true,
		CanEnter:     false,
	}, nil
}

func (m *Manager) pruneExpired(ctx context.Context, showtimeID uuid.UUID, maxActive int64) error {
	queueKey := queueMembersKey(showtimeID)
	activeKey := activeMembersKey(showtimeID)

	members, err := m.client.ZRange(ctx, queueKey, 0, -1).Result()
	if err != nil {
		return err
	}
	staleQueue := make([]interface{}, 0)
	for _, member := range members {
		uid, err := uuid.Parse(member)
		if err != nil {
			staleQueue = append(staleQueue, member)
			continue
		}
		exists, err := m.client.Exists(ctx, queueSessionKey(showtimeID, uid)).Result()
		if err != nil {
			return err
		}
		if exists == 0 {
			staleQueue = append(staleQueue, member)
		}
	}
	if len(staleQueue) > 0 {
		if err := m.client.ZRem(ctx, queueKey, staleQueue...).Err(); err != nil {
			return err
		}
	}

	activeMembers, err := m.client.SMembers(ctx, activeKey).Result()
	if err != nil {
		return err
	}
	staleActive := make([]interface{}, 0)
	for _, member := range activeMembers {
		uid, err := uuid.Parse(member)
		if err != nil {
			staleActive = append(staleActive, member)
			continue
		}
		exists, err := m.client.Exists(ctx, activeSessionKey(showtimeID, uid)).Result()
		if err != nil {
			return err
		}
		if exists == 0 {
			staleActive = append(staleActive, member)
		}
	}
	if len(staleActive) > 0 {
		if err := m.client.SRem(ctx, activeKey, staleActive...).Err(); err != nil {
			return err
		}
	}

	return m.drainPromotionLoop(ctx, showtimeID, maxActive)
}

func queueMembersKey(showtimeID uuid.UUID) string {
	return fmt.Sprintf("booking:queue:%s:members", showtimeID.String())
}

func queueSessionKey(showtimeID, userID uuid.UUID) string {
	return fmt.Sprintf("booking:queue:%s:user:%s", showtimeID.String(), userID.String())
}
