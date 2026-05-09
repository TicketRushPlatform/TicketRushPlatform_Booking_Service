package realtime

import (
	"testing"

	"github.com/google/uuid"
)

func TestSeatHubSubscribeUnsubscribeBroadcast(t *testing.T) {
	h := NewSeatHub()
	show := uuid.New()

	ch := h.Subscribe(show)
	if len(h.ShowtimeIDs()) != 1 {
		t.Fatalf("expected one showtime, got %v", h.ShowtimeIDs())
	}

	payload := []byte(`{"type":"seat_status"}`)
	h.Broadcast(show, payload)

	select {
	case got := <-ch:
		if string(got) != string(payload) {
			t.Fatalf("payload mismatch: %s", got)
		}
	default:
		t.Fatalf("expected subscriber to receive broadcast")
	}

	h.Unsubscribe(show, ch)
	if len(h.ShowtimeIDs()) != 0 {
		t.Fatalf("expected hub empty after unsubscribe")
	}
}

func TestSeatHubBroadcastFullChannelDrops(t *testing.T) {
	h := NewSeatHub()
	show := uuid.New()
	ch := h.Subscribe(show)
	for i := 0; i < cap(ch); i++ {
		ch <- []byte("fill")
	}

	h.Broadcast(show, []byte("dropped"))

	h.Unsubscribe(show, ch)
}

func TestSeatHubUnsubscribeUnknownShowtime(t *testing.T) {
	h := NewSeatHub()
	ch := make(chan []byte)
	h.Unsubscribe(uuid.New(), ch)
}

func TestSeatHubBroadcastNoSubscribers(t *testing.T) {
	h := NewSeatHub()
	h.Broadcast(uuid.New(), []byte("x"))
}
