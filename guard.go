package netsec

import "errors"

// MaxSeq is the last usable record sequence number under one key.
const MaxSeq = ^uint64(0)

var (
	// ErrSequenceExhausted is returned when a record arrives after the
	// sequence space was consumed without a key update. Accepting it
	// would reuse sequence number 0 under the same key.
	ErrSequenceExhausted = errors.New("netsec: sequence number space exhausted; key update or close required")
	// ErrClosed is returned for any operation on a closed guard.
	ErrClosed = errors.New("netsec: record layer closed")
)

// State is the state of the record-sequence state machine.
type State int

const (
	// StateReady means records under the current key are accepted.
	StateReady State = iota
	// StateExhausted means the sequence space is consumed; the TLS
	// stack must call KeyUpdate or Close before the next record.
	StateExhausted
	// StateClosed is terminal; no further records are accepted.
	StateClosed
)

func (s State) String() string {
	switch s {
	case StateReady:
		return "ready"
	case StateExhausted:
		return "exhausted"
	case StateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// Guard tracks record sequence numbers for one direction of a
// connection and rejects records that would wrap the sequence number.
type Guard struct {
	next  uint64 // next expected sequence number under the current key
	state State
	epoch uint64 // key epoch, incremented on every KeyUpdate
}

// NewGuard returns a Guard starting at sequence number 0.
func NewGuard() *Guard {
	return &Guard{state: StateReady}
}

// ImportState seeds the guard with state delivered by the TLS stack:
// nextSeq is the next sequence number the stack expects under the
// current key. Importing MaxSeq means exactly one record remains
// before the sequence space is exhausted.
func (g *Guard) ImportState(nextSeq uint64) {
	g.next = nextSeq
	if g.state != StateClosed {
		g.state = StateReady
	}
}

// OnRecord is called by the TLS stack when a record protected with the
// current key arrives. It consumes one sequence number. The record
// carrying MaxSeq is the last one accepted; any further record fails
// with ErrSequenceExhausted until KeyUpdate is called.
func (g *Guard) OnRecord() error {
	switch g.state {
	case StateClosed:
		return ErrClosed
	case StateExhausted:
		return ErrSequenceExhausted
	}
	if g.next == MaxSeq {
		g.state = StateExhausted
		return nil
	}
	g.next++
	return nil
}

// KeyUpdate installs a new key: the sequence number restarts at 0 and
// records flow again. It fails on a closed guard.
func (g *Guard) KeyUpdate() error {
	if g.state == StateClosed {
		return ErrClosed
	}
	g.epoch++
	g.next = 0
	g.state = StateReady
	return nil
}

// Close terminates the guard; subsequent records and key updates fail.
func (g *Guard) Close() {
	g.state = StateClosed
}

// State reports the current state machine state.
func (g *Guard) State() State { return g.state }

// Next reports the next expected sequence number under the current key.
func (g *Guard) Next() uint64 { return g.next }

// Epoch reports the key epoch, incremented by every KeyUpdate.
func (g *Guard) Epoch() uint64 { return g.epoch }
