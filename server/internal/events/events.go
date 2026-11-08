// Package events is a minimal in-process publish/subscribe broker used to fan
// out change notifications to WebSocket clients. It is deliberately not a
// message store: SQLite remains authoritative and clients re-fetch over REST.
package events

import (
	"sync"
)

type Event struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Broker struct {
	mu      sync.Mutex
	nextID  int
	clients map[int]chan Event
}

func NewBroker() *Broker {
	return &Broker{clients: make(map[int]chan Event)}
}

// Subscribe returns a buffered channel and an unsubscribe function. The buffer
// is small: a slow client is disconnected (via closed channel signal) rather
// than allowed to grow memory unboundedly.
func (b *Broker) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 16)
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.clients[id] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if c, ok := b.clients[id]; ok {
			delete(b.clients, id)
			close(c)
		}
		b.mu.Unlock()
	}
}

// Publish delivers an event to all subscribers, dropping it for any subscriber
// whose buffer is full (that client will resync from REST on reconnect).
func (b *Broker) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.clients {
		select {
		case ch <- ev:
		default:
		}
	}
}

// SubscriberCount reports the number of connected subscribers.
func (b *Broker) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients)
}
