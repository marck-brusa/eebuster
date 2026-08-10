package eebusgo

import (
	"sync"
	"time"
)

// Event mirrors src/facade/events/bus.py's record shape exactly (seq/ts/level/stack/event/
// ski/data) so the existing dashboard JS renders it unchanged.
type Event struct {
	Seq   int64  `json:"seq"`
	Ts    int64  `json:"ts"`
	Level string `json:"level"` // "lifecycle" | "usecase" | "spine"
	Stack string `json:"stack"`
	Event string `json:"event"`
	SKI   string `json:"ski,omitempty"`
	Data  any    `json:"data,omitempty"`
}

const defaultRingSize = 5000

// EventBus is a ring buffer plus fan-out to live SSE subscribers, matching
// src/facade/events/bus.py's EventBus. A slow subscriber drops events rather than blocking
// publishers -- same trade-off the Python version makes with asyncio.QueueFull.
type EventBus struct {
	mu          sync.Mutex
	ring        []Event
	ringSize    int
	seq         int64
	subscribers map[chan Event]struct{}
}

func NewEventBus() *EventBus {
	return &EventBus{ringSize: defaultRingSize, subscribers: make(map[chan Event]struct{})}
}

func (b *EventBus) Publish(level, stackID, event, ski string, data any) {
	b.mu.Lock()
	b.seq++
	rec := Event{Seq: b.seq, Ts: time.Now().Unix(), Level: level, Stack: stackID, Event: event, SKI: ski, Data: data}
	b.ring = append(b.ring, rec)
	if len(b.ring) > b.ringSize {
		b.ring = b.ring[len(b.ring)-b.ringSize:]
	}
	subs := make([]chan Event, 0, len(b.subscribers))
	for ch := range b.subscribers {
		subs = append(subs, ch)
	}
	b.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- rec:
		default: // slow subscriber: drop rather than block the publisher
		}
	}
}

func (b *EventBus) Recent(limit int, level, ski string) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Event, 0, len(b.ring))
	for _, e := range b.ring {
		if level != "" && e.Level != level {
			continue
		}
		if ski != "" && e.SKI != ski {
			continue
		}
		out = append(out, e)
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (b *EventBus) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ring = nil
}

// Subscribe registers a new live listener. Call the returned cancel func when done (e.g. the
// SSE HTTP handler returning) to stop leaking the channel and its buffer.
func (b *EventBus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 1000)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		delete(b.subscribers, ch)
		b.mu.Unlock()
	}
	return ch, cancel
}
