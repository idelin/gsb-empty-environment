package netsec

import (
	"fmt"
	"sync"
	"time"
)

// KeyState guards the receive path against key-sequence manipulation and
// replayed frames. It tracks the currently accepted key sequence, the time of
// the last accepted switch, and the highest accepted frame counter for every
// (key sequence, neighbor) pair.
//
// A KeyState is safe for concurrent use.
type KeyState struct {
	// A mutex is used instead of atomics because a frame check updates several
	// related fields that must change together.
	mu sync.Mutex

	// current is a high-water mark. It never decreases: a sequence that has
	// been retired must not become usable again.
	current    uint32
	lastSwitch time.Time
	protection time.Duration

	// counters is intentionally scoped to the current sequence. Frames for
	// older sequences are rejected before they can reach this map.
	counters map[counterKey]uint32
}

type counterKey struct {
	sequence uint32
	neighbor string
}

// New creates a KeyState whose current key sequence is initialSequence.
//
// The protection period starts at the time of construction. Use NewAt when
// the caller needs a deterministic clock.
func New(initialSequence uint32, protection time.Duration) *KeyState {
	return NewAt(initialSequence, protection, time.Now())
}

// NewAt creates a KeyState whose protection period starts at now.
func NewAt(initialSequence uint32, protection time.Duration, now time.Time) *KeyState {
	if protection < 0 {
		protection = 0
	}
	return &KeyState{
		current:    initialSequence,
		lastSwitch: now,
		protection: protection,
		counters:   make(map[counterKey]uint32),
	}
}

// KeyIndex returns the key index carried in a frame for the given sequence.
func KeyIndex(sequence uint32) uint8 {
	return uint8(sequence&0x7f) + 1
}

// CurrentSequence returns the currently accepted key sequence.
func (s *KeyState) CurrentSequence() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// CurrentKeyIndex returns the key index for the current key sequence.
func (s *KeyState) CurrentKeyIndex() uint8 {
	return KeyIndex(s.CurrentSequence())
}

// LastSwitch returns the time at which the current sequence was accepted.
func (s *KeyState) LastSwitch() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSwitch
}

// ProtectionPeriod returns the minimum interval between key switches.
func (s *KeyState) ProtectionPeriod() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.protection
}

// AcceptFrame validates a frame received from neighbor at now. On success it
// returns the derived key index and records the frame counter. On failure it
// returns an error and leaves the state unchanged.
//
// The following rules are enforced:
//   - The sequence must either equal the current sequence or advance it by
//     exactly one. Skipping ahead is rejected.
//   - A switch is rejected while less than the protection period has elapsed
//     since the last accepted switch.
//   - Frame counters must strictly increase for each (sequence, neighbor)
//     pair.
//   - Sequences below the current sequence are retired and can never be
//     re-enabled, even when their key index aliases a newer sequence.
func (s *KeyState) AcceptFrame(neighbor string, sequence uint32, frameCounter uint32, now time.Time) (uint8, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case sequence < s.current:
		return KeyIndex(sequence), fmt.Errorf("%w: received sequence %d, current sequence %d",
			ErrSequenceRetired, sequence, s.current)
	case sequence > s.current:
		// At MaxUint32 there is no representable successor, so this branch
		// cannot overflow: a larger sequence cannot exist.
		if sequence != s.current+1 {
			return KeyIndex(sequence), fmt.Errorf("%w: received sequence %d, current sequence %d",
				ErrSequenceSkipped, sequence, s.current)
		}
		if now.Sub(s.lastSwitch) < s.protection {
			return KeyIndex(sequence), fmt.Errorf("%w: %v since last switch, need %v",
				ErrProtectionPeriod, now.Sub(s.lastSwitch), s.protection)
		}

		// Commit the switch only after every check has passed. Counters from
		// the retired sequence are discarded because its frames can no longer
		// be accepted.
		s.current = sequence
		s.lastSwitch = now
		s.counters = make(map[counterKey]uint32)
	}

	key := counterKey{sequence: sequence, neighbor: neighbor}
	if last, ok := s.counters[key]; ok && frameCounter <= last {
		return KeyIndex(sequence), fmt.Errorf("%w: sequence %d neighbor %q counter %d, previous %d",
			ErrFrameCounter, sequence, neighbor, frameCounter, last)
	}
	s.counters[key] = frameCounter
	return KeyIndex(sequence), nil
}
