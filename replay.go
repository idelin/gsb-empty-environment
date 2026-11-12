// Package netsec replays a measured-boot event log against a set of
// measurement registers so that a remote attestation verifier can decide
// whether a signed register quote really follows from the supplied log.
//
// Every event is folded into exactly one register through Extend. The hash
// input binds the register index, the event type and the event-data length
// explicitly, so an event cannot be moved between registers, retyped, or
// have its data length confused without changing the resulting digest.
//
// Registers evolve independently. A register may be reset (its value
// returns to zero) only when an event whose type appears in
// Config.StartupTypes is replayed into it.
//
// Replay reports the first log entry whose claimed post-event register
// state disagrees with the recomputed state (the first inconsistent
// prefix), then keeps replaying so that the final recomputed register set
// is always returned for comparison with the signed quote.
package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
)

// EventType classifies a measured event. Type values are part of the hash
// input, so two events carrying identical data but different types extend
// a register differently.
type EventType uint16

const (
	// EventBoot is an ordinary measurement event; it only extends.
	EventBoot EventType = 0x0001
	// EventConfig records configuration material; it only extends.
	EventConfig EventType = 0x0002
	// EventApp records application or workload material; it only extends.
	EventApp EventType = 0x0003
	// EventStartup is an authorized launch event. Replaying it resets its
	// target register to zero before the event itself is extended.
	EventStartup EventType = 0x8000
)

// String returns a stable identifier for the built-in event types.
func (t EventType) String() string {
	switch t {
	case EventBoot:
		return "boot"
	case EventConfig:
		return "config"
	case EventApp:
		return "app"
	case EventStartup:
		return "startup"
	default:
		return "unknown"
	}
}

// Entry is one measured event in the log.
type Entry struct {
	// Register selects the independently evolving register to extend.
	Register uint32
	// Type is bound into the hash extension.
	Type EventType
	// Data is the measured payload; its length is bound into the digest.
	Data []byte
	// State optionally records the claimed full register set immediately
	// after this entry. A nil entry means no per-entry state is claimed;
	// such entries are only checked against the final quote.
	State [][]byte
}

// Config parameterises a replay.
type Config struct {
	// NewHash selects the register digest. It defaults to SHA-256.
	NewHash func() hash.Hash
	// NumRegisters is the number of independently evolving registers.
	// Zero defaults to four.
	NumRegisters uint32
	// StartupTypes lists the event types authorized to reset a register.
	// nil defaults to {EventStartup}.
	StartupTypes []EventType
	// Quote holds the signed final register values to verify. When nil or
	// empty the final comparison is skipped and only per-entry states are
	// checked.
	Quote map[uint32][]byte
}

// QuoteValue pairs a register index with the final value quoted for it.
type QuoteValue struct {
	Register uint32
	Expected []byte
	Actual   []byte
}

// Mismatch identifies the first observed inconsistency.
type Mismatch struct {
	// Entry is the zero-based log index of the first inconsistent entry,
	// or -1 when the only inconsistency is the final quote.
	Entry int
	// Register is the first register whose value disagreed.
	Register uint32
	// Kind is "entry-state" for a claimed per-entry state or "quote" for
	// the signed final register value.
	Kind     string
	Expected []byte
	Actual   []byte
}

// Result is the outcome of a replay.
type Result struct {
	// Prefix is the length of the longest log prefix that agrees with the
	// claimed state at every checked entry: all entries [0, Prefix) are
	// consistent. It equals len(log) only when every claimed per-entry
	// state matched.
	Prefix int
	// Final is the recomputed value of every register after the whole log
	// has been replayed, regardless of where the first mismatch occurred.
	Final [][]byte
	// Mismatch describes the first inconsistency and is nil when both the
	// per-entry states and the final quote are consistent.
	Mismatch *Mismatch
}

// OK reports whether the log fully reproduced every claimed state and the
// signed final register set.
func (r *Result) OK() bool {
	return r != nil && r.Mismatch == nil
}

var (
	// ErrBadRegister is returned when an entry addresses a register
	// outside the configured range.
	ErrBadRegister = errors.New("netsec: entry references an unknown register")
	// ErrUnknownType is returned when an entry carries an event type that
	// is not a known measurement type.
	ErrUnknownType = errors.New("netsec: unknown event type")
	// ErrDigestSize is returned when a claimed digest has the wrong size.
	ErrDigestSize = errors.New("netsec: claimed digest has the wrong size")
	// ErrStateShape is returned when a claimed per-entry state vector
	// does not cover exactly the configured register set.
	ErrStateShape = errors.New("netsec: claimed state vector has the wrong length")
	// ErrIncompleteQuote is returned when the quote omits a register.
	ErrIncompleteQuote = errors.New("netsec: quote is missing a register")
)

// Extend folds one event into a register value.
//
// The hash input is the concatenation of:
//
//	prev      the current register value (Hash.Size bytes)
//	index     the register index, 8 bytes big-endian
//	etype     the event type, 2 bytes big-endian
//	dlen      the 8-byte big-endian length of data
//	data      the raw event payload
//
// Binding index, type and length means the same payload produces distinct
// digests across registers and event types, and length-confusion attacks
// cannot collide.
func Extend(h func() hash.Hash, prev []byte, index uint32, etype EventType, data []byte) []byte {
	digest := h()
	size := digest.Size()
	if len(prev) != size {
		panic("netsec: Extend called with a previous value of the wrong size")
	}
	var head [18]byte
	binary.BigEndian.PutUint64(head[0:8], uint64(index))
	binary.BigEndian.PutUint16(head[8:10], uint16(etype))
	binary.BigEndian.PutUint64(head[10:18], uint64(len(data)))
	digest.Write(prev)
	digest.Write(head[:])
	digest.Write(data)
	return digest.Sum(nil)
}

func knownType(t EventType) bool {
	switch t {
	case EventBoot, EventConfig, EventApp, EventStartup:
		return true
	default:
		return false
	}
}

// Replay replays log under cfg and returns the first inconsistent prefix
// together with the final recomputed register values. Replay never aborts
// at the first mismatch: it records it and continues, so Final always
// reflects the complete log.
func Replay(log []Entry, cfg Config) (*Result, error) {
	newHash := cfg.NewHash
	if newHash == nil {
		newHash = sha256.New
	}
	numRegisters := cfg.NumRegisters
	if numRegisters == 0 {
		numRegisters = 4
	}
	startup := make(map[EventType]bool, len(cfg.StartupTypes)+1)
	if cfg.StartupTypes == nil {
		startup[EventStartup] = true
	} else {
		for _, t := range cfg.StartupTypes {
			startup[t] = true
		}
	}

	zero := make([]byte, newHash().Size())
	registers := make([][]byte, numRegisters)
	for i := range registers {
		registers[i] = append([]byte(nil), zero...)
	}

	result := &Result{Prefix: len(log), Final: registers}

	for entryIndex, entry := range log {
		if entry.Register >= numRegisters {
			return nil, ErrBadRegister
		}
		if !knownType(entry.Type) {
			return nil, ErrUnknownType
		}

		if startup[entry.Type] {
			// An authorized launch event is the only way a register is
			// allowed to return to zero; the launch event itself is then
			// measured like any other extension.
			registers[entry.Register] = append([]byte(nil), zero...)
		}
		registers[entry.Register] = Extend(newHash, registers[entry.Register], entry.Register, entry.Type, entry.Data)

		if entry.State != nil && result.Mismatch == nil {
			if len(entry.State) != int(numRegisters) {
				return nil, ErrStateShape
			}
			for regIndex, claimed := range entry.State {
				if claimed == nil {
					continue
				}
				if len(claimed) != len(zero) {
					return nil, ErrDigestSize
				}
				if !equalDigest(registers[regIndex], claimed) {
					result.Prefix = entryIndex
					result.Mismatch = &Mismatch{
						Entry:    entryIndex,
						Register: uint32(regIndex),
						Kind:     "entry-state",
						Expected: append([]byte(nil), claimed...),
						Actual:   append([]byte(nil), registers[regIndex]...),
					}
					break
				}
			}
		}
	}

	if len(cfg.Quote) > 0 && result.Mismatch == nil {
		for regIndex := uint32(0); regIndex < numRegisters; regIndex++ {
			quoted, ok := cfg.Quote[regIndex]
			if !ok {
				return nil, ErrIncompleteQuote
			}
			if len(quoted) != len(zero) {
				return nil, ErrDigestSize
			}
			if !equalDigest(registers[regIndex], quoted) {
				result.Prefix = len(log)
				result.Mismatch = &Mismatch{
					Entry:    -1,
					Register: regIndex,
					Kind:     "quote",
					Expected: append([]byte(nil), quoted...),
					Actual:   append([]byte(nil), registers[regIndex]...),
				}
				break
			}
		}
	}

	return result, nil
}

func equalDigest(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
