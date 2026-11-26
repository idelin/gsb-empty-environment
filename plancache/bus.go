package plancache

import "sync"

// InvalidationEvent is a host-visible privilege/catalog change. Only
// immediate events pierce a pinned transaction snapshot.
type InvalidationEvent struct {
	Type      string // "revoke", "rls", "ddl"
	Immediate bool
	Principal PrincipalID // empty means every principal
	ViewIDs   []ViewID
	ObjectIDs []ObjectID
	Fence     int64
}

// InvalidationBus is the monotonically ordered invalidation log. Every
// published event gets a strictly increasing fence value; plans record the
// fence at enqueue time and inspect later events before executing.
type InvalidationBus struct {
	mu      sync.Mutex
	fence   int64
	history []InvalidationEvent
	subs    []chan InvalidationEvent
}

// NewInvalidationBus creates an empty bus.
func NewInvalidationBus() *InvalidationBus {
	return &InvalidationBus{}
}

// Publish assigns the next fence and fans the event out to subscribers.
func (b *InvalidationBus) Publish(e InvalidationEvent) InvalidationEvent {
	b.mu.Lock()
	b.fence++
	e.Fence = b.fence
	b.history = append(b.history, e)
	subs := append([]chan InvalidationEvent(nil), b.subs...)
	b.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- e:
		default: // never block the publisher; consumers also read Since()
		}
	}
	return e
}

// Fence returns the current highest fence value.
func (b *InvalidationBus) Fence() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.fence
}

// Since returns immediate events published after the given fence whose
// target set intersects the given principal / manifest. An empty manifest
// matches every event relevant to the principal (or global events), which
// is used as a transaction-wide hard stop after immediate invalidation.
func (b *InvalidationBus) Since(fence int64, p PrincipalID, m Manifest) []InvalidationEvent {
	b.mu.Lock()
	history := append([]InvalidationEvent(nil), b.history...)
	b.mu.Unlock()

	var out []InvalidationEvent
	for _, e := range history {
		if e.Fence <= fence || !e.Immediate {
			continue
		}
		if e.Principal != "" && e.Principal != p {
			continue
		}
		if isEmptyManifest(m) || intersectsEvent(e, m) {
			out = append(out, e)
		}
	}
	return out
}

func isEmptyManifest(m Manifest) bool {
	return len(m.Views) == 0 && len(m.Tables) == 0 && m.Caller == ""
}

func intersectsEvent(e InvalidationEvent, m Manifest) bool {
	if len(e.ViewIDs) == 0 && len(e.ObjectIDs) == 0 {
		return true // principal- or global-scoped event
	}
	for _, vid := range e.ViewIDs {
		for _, mv := range m.Views {
			if mv.ViewID == vid {
				return true
			}
		}
	}
	for _, oid := range e.ObjectIDs {
		for _, mt := range m.Tables {
			if mt.ObjectID == oid {
				return true
			}
		}
	}
	return false
}

// Subscribe registers a buffered event channel. The returned function
// unsubscribes and closes the channel.
func (b *InvalidationBus) Subscribe() (<-chan InvalidationEvent, func()) {
	ch := make(chan InvalidationEvent, 64)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, sub := range b.subs {
			if sub == ch {
				b.subs = append(b.subs[:i], b.subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, cancel
}
