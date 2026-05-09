package realtime

import (
	"sync"

	"github.com/google/uuid"
)

type SeatHub struct {
	mu          sync.RWMutex
	subscribers map[uuid.UUID]map[chan []byte]struct{}
}

func NewSeatHub() *SeatHub {
	return &SeatHub{
		subscribers: make(map[uuid.UUID]map[chan []byte]struct{}),
	}
}

func (h *SeatHub) Subscribe(showtimeID uuid.UUID) chan []byte {
	ch := make(chan []byte, 8)

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.subscribers[showtimeID] == nil {
		h.subscribers[showtimeID] = make(map[chan []byte]struct{})
	}
	h.subscribers[showtimeID][ch] = struct{}{}

	return ch
}

func (h *SeatHub) Unsubscribe(showtimeID uuid.UUID, ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if subscribers := h.subscribers[showtimeID]; subscribers != nil {
		delete(subscribers, ch)
		if len(subscribers) == 0 {
			delete(h.subscribers, showtimeID)
		}
	}
	close(ch)
}

func (h *SeatHub) Broadcast(showtimeID uuid.UUID, payload []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for ch := range h.subscribers[showtimeID] {
		select {
		case ch <- payload:
		default:
		}
	}
}

func (h *SeatHub) ShowtimeIDs() []uuid.UUID {
	h.mu.RLock()
	defer h.mu.RUnlock()

	ids := make([]uuid.UUID, 0, len(h.subscribers))
	for id := range h.subscribers {
		ids = append(ids, id)
	}
	return ids
}
