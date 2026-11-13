package tcpao

import (
	"fmt"
	"sync"
	"time"
)

// Segment is the TCP-AO-relevant part of an inbound segment. MACData is the
// transport-authenticated input excluding the KeyID and RNextKeyID bytes; the
// selector prepends those identifiers before computing the MAC. The transport
// should therefore pass the same MACData it used when constructing the
// outbound segment.
type Segment struct {
	KeyID      KeyID
	RNextKeyID KeyID
	MAC        []byte
	MACData    []byte
}

// Result describes the outcome of an authenticated inbound segment.
type Result struct {
	// Accepted is true only when KeyID was registered, its receive validity
	// covered the segment time, and its MAC verified.
	Accepted bool

	// KeyID is the inbound key that was selected by KeyID.
	KeyID KeyID

	// RNextKeyID is the next-key request carried by the segment.
	RNextKeyID KeyID

	// SendKeyID is the selected send key after processing the segment.
	SendKeyID KeyID

	// Switched is true when a MAC-verified RNextKeyID caused SendKeyID to
	// change.
	Switched bool

	// RNextError explains why a verified segment's RNextKeyID did not cause a
	// switch. It is nil when no switch was requested, or when the switch
	// succeeded.
	RNextError error
}

// Selector owns a set of registered keys and the currently selected send key.
// It is safe for concurrent use.
type Selector struct {
	mu        sync.RWMutex
	keys      map[KeyID]Key
	sendKeyID KeyID
}

// NewSelector creates a selector with sendKeyID as the initial send key. All
// supplied keys are registered atomically; an invalid or duplicate key aborts
// construction. sendKeyID must be one of the supplied keys.
func NewSelector(sendKeyID KeyID, keys ...Key) (*Selector, error) {
	if !sendKeyID.Valid() {
		return nil, fmt.Errorf("%w: zero send key id", ErrNoSendKey)
	}

	s := &Selector{
		keys:      make(map[KeyID]Key, len(keys)),
		sendKeyID: sendKeyID,
	}
	for _, key := range keys {
		if err := s.register(key); err != nil {
			return nil, err
		}
	}
	if _, ok := s.keys[sendKeyID]; !ok {
		return nil, fmt.Errorf("%w: send key %d is not registered", ErrNoSendKey, sendKeyID)
	}
	return s, nil
}

// Register adds a key. It rejects duplicate KeyIDs instead of silently
// replacing existing key material.
func (s *Selector) Register(key Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.register(key)
}

func (s *Selector) register(key Key) error {
	normalized, err := key.normalized()
	if err != nil {
		return err
	}
	if _, exists := s.keys[normalized.ID]; exists {
		return fmt.Errorf("%w: %d", ErrDuplicateKey, normalized.ID)
	}
	s.keys[normalized.ID] = normalized
	return nil
}

// Unregister removes a key. If it is the selected send key, subsequent sends
// fail closed until a valid RNextKeyID selects another registered key.
func (s *Selector) Unregister(id KeyID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[id]; !ok {
		return false
	}
	delete(s.keys, id)
	return true
}

// Registered reports whether id currently has key material.
func (s *Selector) Registered(id KeyID) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.keys[id]
	return ok
}

// SendKeyID returns the currently selected outbound KeyID.
func (s *Selector) SendKeyID() KeyID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sendKeyID
}

// SendKey returns a copy of the selected send key after checking its send
// validity at the supplied time. The copy prevents callers from mutating the
// registered key material.
func (s *Selector) SendKey(at time.Time) (Key, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.sendKeyID.Valid() {
		return Key{}, ErrNoSendKey
	}
	key, ok := s.keys[s.sendKeyID]
	if !ok {
		return Key{}, fmt.Errorf("%w: %d", ErrSendKeyUnknown, s.sendKeyID)
	}
	switch key.SendValidity.state(at) {
	case validityFuture:
		return Key{}, fmt.Errorf("%w: %d", ErrSendKeyNotYetValid, s.sendKeyID)
	case validityExpired:
		return Key{}, fmt.Errorf("%w: %d", ErrSendKeyExpired, s.sendKeyID)
	default:
		return key.clone(), nil
	}
}

// Receive validates an inbound segment at at and applies a switch request only
// when the segment's MAC proves that request. A returned error means the
// segment must be discarded. If the MAC is valid but RNextKeyID is not usable,
// the segment is accepted and Result.RNextError explains why no switch
// occurred.
func (s *Selector) Receive(seg Segment, at time.Time) (Result, error) {
	result, err := s.authenticate(seg, at)
	if err != nil {
		return result, err
	}

	// Authentication has succeeded. Only now may the RNextKeyID option have
	// any effect on outbound state.
	result.Accepted = true

	s.mu.Lock()
	defer s.mu.Unlock()

	if seg.RNextKeyID != NoKey && seg.RNextKeyID != s.sendKeyID {
		result.RNextError = s.switchSendKey(seg.RNextKeyID, at)
		if result.RNextError == nil {
			result.Switched = true
		}
	}
	result.SendKeyID = s.sendKeyID
	return result, nil
}

func (s *Selector) authenticate(seg Segment, at time.Time) (Result, error) {
	result := Result{
		KeyID:      seg.KeyID,
		RNextKeyID: seg.RNextKeyID,
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	key, ok := s.keys[seg.KeyID]
	result.SendKeyID = s.sendKeyID

	if !ok {
		return result, fmt.Errorf("%w: %d", ErrUnknownKey, seg.KeyID)
	}

	switch key.ReceiveValidity.state(at) {
	case validityFuture:
		return result, fmt.Errorf("%w: %d", ErrReceiveKeyNotYetValid, seg.KeyID)
	case validityExpired:
		return result, fmt.Errorf("%w: %d", ErrReceiveKeyExpired, seg.KeyID)
	}

	if !verifyMAC(key.Algorithm, key.ReceiveKey, seg.KeyID, seg.RNextKeyID, seg.MACData, seg.MAC) {
		return result, fmt.Errorf("%w: key %d", ErrAuthenticationFailed, seg.KeyID)
	}
	return result, nil
}

func (s *Selector) switchSendKey(next KeyID, at time.Time) error {
	key, ok := s.keys[next]
	if !ok {
		return fmt.Errorf("%w: %d", ErrNextKeyUnknown, next)
	}
	switch key.SendValidity.state(at) {
	case validityFuture:
		return fmt.Errorf("%w: %d", ErrNextKeyNotYetValid, next)
	case validityExpired:
		return fmt.Errorf("%w: %d", ErrNextKeySendExpired, next)
	default:
		s.sendKeyID = next
		return nil
	}
}
