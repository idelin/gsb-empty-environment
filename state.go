// Package statedb provides a versioned key-value state store that is
// reconstructed from a base snapshot plus a chain of operation deltas.
//
// Every delta carries the digest of the state it applies to (BaseDigest)
// and the digest of the state it produces (Digest). Applying a delta that
// has already been applied is an idempotent no-op. A delta whose base
// digest does not match the current head is rejected, which prevents
// broken chains and prevents branch deltas from attaching to the wrong
// baseline. Application is atomic: a delta either commits fully or leaves
// the store untouched.
package statedb

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

// Digest is the content hash of a State.
type Digest [sha256.Size]byte

// ZeroDigest is the digest of the empty state, used as the base of the
// very first delta.
var ZeroDigest = DigestOf(NewState())

func (d Digest) String() string { return fmt.Sprintf("%x", d[:8]) }

// OpKind enumerates the mutations a delta may contain.
type OpKind byte

const (
	OpSet OpKind = iota // set key to value
	OpAdd               // add value to key (missing key starts at 0)
	OpDel               // delete key
)

// Op is a single mutation inside a Delta.
type Op struct {
	Kind OpKind
	Key  string
	Val  int64
}

// State is the materialized key-value state.
type State struct {
	kv map[string]int64
}

// NewState returns an empty state.
func NewState() *State { return &State{kv: make(map[string]int64)} }

// Clone returns a deep copy.
func (s *State) Clone() *State {
	c := &State{kv: make(map[string]int64, len(s.kv))}
	for k, v := range s.kv {
		c.kv[k] = v
	}
	return c
}

// Get reads a key.
func (s *State) Get(key string) (int64, bool) {
	v, ok := s.kv[key]
	return v, ok
}

// Len reports the number of keys.
func (s *State) Len() int { return len(s.kv) }

// Keys returns the sorted key set.
func (s *State) Keys() []string {
	keys := make([]string, 0, len(s.kv))
	for k := range s.kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// apply mutates the state with a single op.
func (s *State) apply(op Op) {
	switch op.Kind {
	case OpSet:
		s.kv[op.Key] = op.Val
	case OpAdd:
		s.kv[op.Key] += op.Val
	case OpDel:
		delete(s.kv, op.Key)
	}
}

// DigestOf computes the canonical content digest of a state.
func DigestOf(s *State) Digest {
	h := sha256.New()
	var buf [8]byte
	for _, k := range s.Keys() {
		h.Write([]byte(k))
		h.Write([]byte{0})
		binary.BigEndian.PutUint64(buf[:], uint64(s.kv[k]))
		h.Write(buf[:])
	}
	var d Digest
	copy(d[:], h.Sum(nil))
	return d
}
