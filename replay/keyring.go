package replay

import (
	"fmt"
	"time"
)

// Packet is one received tunnel data-plane packet as seen by the
// anti-replay layer. Authentic must report whether the packet already
// passed its cryptographic authentication (AEAD/MAC) check; replay
// filtering never replaces authentication.
type Packet struct {
	// Index is the 32-bit receive index of the session the packet was
	// encrypted under.
	Index uint32
	// Counter is the 64-bit monotonic sequence number within the
	// session.
	Counter uint64
	// Authentic reports successful cryptographic authentication.
	Authentic bool
}

// ReceiveResult is the verdict for one packet.
type ReceiveResult struct {
	// Accepted is true only when every gate passed.
	Accepted bool
	// Reason is RejectNone on acceptance.
	Reason RejectReason
	// Promoted is true when this packet was the first valid packet of
	// the Next session and therefore triggered the generation switch.
	Promoted bool
	// SessionIndex is the index the packet was attributed to, or zero
	// when no matching slot exists.
	SessionIndex uint32
}

// Err maps a rejection reason to its sentinel error, or nil when the
// packet was accepted.
func (r ReceiveResult) Err() error {
	if r.Reason == RejectNone {
		return nil
	}
	return reasonToErr[r.Reason]
}

// KeyRing holds the three coexisting session slots during a rekey:
// Previous, Current and Next. All operations hold a single mutex, so the
// generation switch is atomic relative to concurrent receivers.
type KeyRing struct {
	mu       chan struct{}
	previous *Session
	current  *Session
	next     *Session
	now      func() time.Time
}

// NewKeyRing creates an empty key ring. If now is nil, time.Now is used.
func NewKeyRing(now func() time.Time) *KeyRing {
	if now == nil {
		now = time.Now
	}
	return &KeyRing{
		mu:  make(chan struct{}, 1),
		now: now,
	}

}

func (k *KeyRing) lock()   { k.mu <- struct{}{} }
func (k *KeyRing) unlock() { <-k.mu }

// Bootstrap installs the initial Current session. It fails if a Current
// slot already exists.
func (k *KeyRing) Bootstrap(cfg SessionConfig) (*Session, error) {
	cfg.Now = k.clockOrDefault(cfg.Now)
	sess, err := NewSession(cfg)
	if err != nil {
		return nil, err
	}

	k.lock()
	defer k.unlock()
	if k.current != nil {
		return nil, fmt.Errorf("replay: key ring already bootstrapped with index %d", k.current.index)
	}
	k.current = sess
	return sess, nil
}

// CreateNext opens a pending Next session while keeping Current and
// Previous untouched. It fails while another Next slot is unconfirmed.
func (k *KeyRing) CreateNext(cfg SessionConfig) (*Session, error) {
	cfg.Now = k.clockOrDefault(cfg.Now)
	sess, err := NewSession(cfg)
	if err != nil {
		return nil, err
	}

	k.lock()
	defer k.unlock()
	if k.current == nil {
		return nil, fmt.Errorf("replay: cannot create next session before bootstrap")
	}
	if k.next != nil {
		return nil, fmt.Errorf("replay: next session %d is still pending confirmation", k.next.index)
	}
	k.next = sess
	return sess, nil
}

// Receive authenticates and replay-checks one packet against the live
// session slots.
//
// Authentication is evaluated first: a forged packet is rejected without
// touching any session state and can never promote Next. A packet is
// attributed to whichever live slot carries its index. Only when the
// first packet addressed to Next passes authentication and replay checks
// does the atomic generation switch run: Next becomes Current, the old
// Current becomes Previous, and the older Previous is discarded.
func (k *KeyRing) Receive(p Packet) ReceiveResult {
	if !p.Authentic {
		return ReceiveResult{Reason: RejectForge}
	}

	k.lock()
	defer k.unlock()

	sess, slot := k.lookup(p.Index)
	if sess == nil {
		return ReceiveResult{Reason: RejectNoSession, SessionIndex: p.Index}
	}

	reason := sess.Process(p.Counter)
	if reason != RejectNone {
		return ReceiveResult{Reason: reason, SessionIndex: sess.index}
	}

	result := ReceiveResult{Accepted: true, Reason: RejectNone, SessionIndex: sess.index}
	if slot == slotNext {
		k.promote(sess)
		result.Promoted = true
	}
	return result
}

type sessionSlot int

const (
	slotPrevious sessionSlot = iota
	slotCurrent
	slotNext
)

func (k *KeyRing) lookup(index uint32) (*Session, sessionSlot) {
	if k.next != nil && k.next.index == index {
		return k.next, slotNext
	}
	if k.current != nil && k.current.index == index {
		return k.current, slotCurrent
	}
	if k.previous != nil && k.previous.index == index {
		return k.previous, slotPrevious
	}
	return nil, slotPrevious
}

// promote performs the three-generation rotation. The oldest Previous is
// simply dropped; callers must already hold the mutex.
func (k *KeyRing) promote(next *Session) {
	k.previous = k.current
	k.current = next
	k.next = nil
}

// Previous returns the live previous session, or nil.
func (k *KeyRing) Previous() *Session {
	k.lock()
	defer k.unlock()
	return k.previous
}

// Current returns the live current session, or nil before Bootstrap.
func (k *KeyRing) Current() *Session {
	k.lock()
	defer k.unlock()
	return k.current
}

// Next returns the pending next session, or nil.
func (k *KeyRing) Next() *Session {
	k.lock()
	defer k.unlock()
	return k.next
}

func (k *KeyRing) clockOrDefault(provided func() time.Time) func() time.Time {
	if provided != nil {
		return provided
	}
	return k.now
}
