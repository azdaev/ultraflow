package core

import "sync"

// Broker is a tiny in-process pub/sub fanning JSON messages out to SSE clients.
type Broker struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[chan []byte]struct{})}
}

func (b *Broker) Subscribe() chan []byte {
	ch := make(chan []byte, 32)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *Broker) Unsubscribe(ch chan []byte) {
	b.mu.Lock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
	b.mu.Unlock()
}

func (b *Broker) Publish(msg []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- msg:
		default:
			// Consumer is too slow and its buffer is full. Dropping the message
			// silently would leave that client permanently diverged (SSE stays
			// open, so it never refetches). Instead, evict it: closing the
			// channel ends its event stream, the browser's EventSource
			// reconnects, and useBoard resyncs the full snapshot on open.
			delete(b.subs, ch)
			close(ch)
		}
	}
}
