// Package broker is a tiny in-process pub/sub that fans JSON messages out to the
// board's live SSE clients. Publisher and subscribers share one process (the
// daemon), so this is a channel fan-out, not a cross-process message queue — a
// Redis/NATS would add a dependency and a network hop for no benefit here.
package broker

import "sync"

// Broker fans published messages out to every current subscriber.
type Broker struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func New() *Broker {
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
			// open, so it never refetches). Instead, evict it: closing the channel
			// ends its event stream, the browser's EventSource reconnects, and
			// useBoard resyncs the full snapshot on open.
			delete(b.subs, ch)
			close(ch)
		}
	}
}
