// Package ws is VAC's real-time transport: an in-memory pub/sub Hub and a thin
// WebSocket connection wrapper. Producers (the deploy pipeline, log followers,
// stats collectors) publish opaque, pre-marshalled frames to string topics;
// WebSocket handlers subscribe and pump frames to the client. The hub never
// touches the producers' Go types — it moves []byte.
package ws

import "sync"

// defaultBuffer is the per-subscriber queue depth. A subscriber that falls this
// far behind is dropped (see Publish) rather than allowed to stall a producer.
const defaultBuffer = 256

// subscriber is one attached client's queue. closed guards against a
// double-close race between Publish (drop-slow) and the cancel func.
type subscriber struct {
	ch     chan []byte
	closed bool
}

// Hub fans out published frames to every subscriber of a topic. Safe for
// concurrent use. Topics are reference-counted: a topic with no subscribers is
// removed, and the optional first-subscribe / last-unsubscribe callbacks fire
// so on-demand producers (the stats collector) can start and stop work.
type Hub struct {
	mu      sync.Mutex
	topics  map[string]map[*subscriber]struct{}
	onSub   func(topic string)
	onUnsub func(topic string)
	shut    bool
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{topics: make(map[string]map[*subscriber]struct{})}
}

// SetCallbacks registers hooks fired when a topic gains its first subscriber
// and loses its last. Used by the stats collector to gate work on demand. Pass
// nil to clear. Callbacks run outside the hub lock, so they may call back into
// the hub.
func (h *Hub) SetCallbacks(onSub, onUnsub func(topic string)) {
	h.mu.Lock()
	h.onSub, h.onUnsub = onSub, onUnsub
	h.mu.Unlock()
}

// Subscribe registers a new subscriber for the topic and returns its receive
// channel plus a cancel func. The channel is closed when the subscriber is
// cancelled or dropped for being too slow; readers must treat a closed channel
// as "connection should end". cancel is idempotent.
func (h *Hub) Subscribe(topic string) (<-chan []byte, func()) {
	sub := &subscriber{ch: make(chan []byte, defaultBuffer)}

	h.mu.Lock()
	if h.shut {
		// Shutting down — hand back an already-closed channel so a connection
		// racing the shutdown ends immediately.
		h.mu.Unlock()
		close(sub.ch)
		return sub.ch, func() {}
	}
	subs := h.topics[topic]
	if subs == nil {
		subs = make(map[*subscriber]struct{})
		h.topics[topic] = subs
	}
	first := len(subs) == 0
	subs[sub] = struct{}{}
	onSub := h.onSub
	h.mu.Unlock()

	if first && onSub != nil {
		onSub(topic)
	}

	var once sync.Once
	return sub.ch, func() { once.Do(func() { h.remove(topic, sub) }) }
}

// Publish sends msg to every current subscriber of the topic. The send is
// non-blocking: a subscriber whose buffer is full is dropped (its channel
// closed), so a slow client can never stall the publisher.
func (h *Hub) Publish(topic string, msg []byte) {
	h.mu.Lock()
	subs := h.topics[topic]
	var dropped []*subscriber
	for sub := range subs {
		if sub.closed {
			continue
		}
		select {
		case sub.ch <- msg:
		default:
			sub.closed = true
			close(sub.ch)
			dropped = append(dropped, sub)
		}
	}
	last := false
	if len(dropped) > 0 {
		for _, sub := range dropped {
			delete(subs, sub)
		}
		if len(subs) == 0 {
			delete(h.topics, topic)
			last = true
		}
	}
	onUnsub := h.onUnsub
	h.mu.Unlock()

	if last && onUnsub != nil {
		onUnsub(topic)
	}
}

// Close drops every subscriber (closing their channels) and refuses new ones.
// Called on shutdown so long-lived WS handlers unblock and the HTTP server's
// graceful Shutdown doesn't wait on them.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.shut = true
	for topic, subs := range h.topics {
		for sub := range subs {
			if !sub.closed {
				sub.closed = true
				close(sub.ch)
			}
		}
		delete(h.topics, topic)
	}
}

// HasSubscribers reports whether the topic currently has any subscriber.
func (h *Hub) HasSubscribers(topic string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.topics[topic]) > 0
}

func (h *Hub) remove(topic string, sub *subscriber) {
	h.mu.Lock()
	last := false
	subs := h.topics[topic]
	if subs != nil {
		if _, ok := subs[sub]; ok {
			delete(subs, sub)
			if !sub.closed {
				sub.closed = true
				close(sub.ch)
			}
		}
		if len(subs) == 0 {
			delete(h.topics, topic)
			last = true
		}
	}
	onUnsub := h.onUnsub
	h.mu.Unlock()

	if last && onUnsub != nil {
		onUnsub(topic)
	}
}
