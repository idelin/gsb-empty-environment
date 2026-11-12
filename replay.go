package netsec

import (
	"bytes"
	"encoding/binary"
	"sync"
)

// TAI64NSize is the byte length of a TAI64N timestamp as carried inside
// WireGuard handshake initiation and response messages.
const TAI64NSize = 12

// TAI64N is a TAI64N timestamp: 8 big-endian TAI seconds followed by 4
// big-endian nanoseconds. Lexicographic byte order matches chronological
// order, so timestamps can be compared directly.
type TAI64N [TAI64NSize]byte

// NewTAI64N builds a TAI64N timestamp from TAI seconds and nanoseconds.
func NewTAI64N(seconds uint64, nanoseconds uint32) TAI64N {
	var ts TAI64N
	binary.BigEndian.PutUint64(ts[0:8], seconds)
	binary.BigEndian.PutUint32(ts[8:12], nanoseconds)
	return ts
}

// Compare returns -1, 0 or +1 depending on whether t orders before, equal
// to, or after other.
func (t TAI64N) Compare(other TAI64N) int {
	return bytes.Compare(t[:], other[:])
}

// HandshakeReplay tracks the largest handshake timestamp accepted from a
// single peer. It exists to reject replayed initiation/response messages
// without letting unauthenticated messages poison the state: a timestamp is
// only recorded once MAC1 verification, static public key decryption and
// timestamp decryption have all succeeded.
//
// The handshake handling loop should:
//  1. optionally call CheckTimestamp as a cheap early reject,
//  2. verify MAC1, decrypt the static public key, decrypt the timestamp,
//  3. call CommitTimestamp only if every step succeeded.
type HandshakeReplay struct {
	mu     sync.Mutex
	max    TAI64N
	hasMax bool
}

// CheckTimestamp reports whether ts is strictly greater than the largest
// timestamp already accepted from this peer. It does not mutate state, so a
// forged message with a huge timestamp that later fails authentication
// cannot advance the recorded maximum.
func (h *HandshakeReplay) CheckTimestamp(ts TAI64N) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.acceptsLocked(ts)
}

// CommitTimestamp records ts as the largest accepted timestamp. It must only
// be called after MAC1 verification, static public key decryption and
// timestamp decryption have all passed. The strict-greater check is
// re-evaluated under the lock; it returns false if ts is not acceptable
// (for example a concurrent commit raced ahead).
func (h *HandshakeReplay) CommitTimestamp(ts TAI64N) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.acceptsLocked(ts) {
		return false
	}
	h.max = ts
	h.hasMax = true
	return true
}

func (h *HandshakeReplay) acceptsLocked(ts TAI64N) bool {
	return !h.hasMax || ts.Compare(h.max) > 0
}

// ReplayWindowSize is the fixed number of transport data counters tracked by
// the sliding window, matching the WireGuard receive window.
const ReplayWindowSize = 2048

// SlidingWindow is a fixed-size replay window over transport data counters.
// Counters are only recorded via Mark, which the caller must invoke solely
// after AEAD authentication succeeds; Check never mutates state, so packets
// that fail authentication do not consume their counter.
type SlidingWindow struct {
	mu      sync.Mutex
	highest uint64
	bitmap  [ReplayWindowSize / 64]uint64
	started bool
}

// Check reports whether counter is currently acceptable: not yet recorded
// and not older than the window. It does not mutate state.
func (w *SlidingWindow) Check(counter uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.acceptableLocked(counter)
}

// Mark records counter as received. It must only be called after the AEAD
// authentication of the packet carrying counter succeeded. It returns false
// if counter is a replay or has fallen behind the window.
func (w *SlidingWindow) Mark(counter uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.acceptableLocked(counter) {
		return false
	}
	if !w.started {
		w.started = true
		w.highest = counter
		w.setBitLocked(0)
		return true
	}
	if counter > w.highest {
		shift := counter - w.highest
		if shift >= ReplayWindowSize {
			w.bitmap = [ReplayWindowSize / 64]uint64{}
		} else {
			w.shiftLocked(shift)
		}
		w.highest = counter
		w.setBitLocked(0)
		return true
	}
	w.setBitLocked(w.highest - counter)
	return true
}

// acceptableLocked reports whether counter may be accepted. Bit i of the
// bitmap tracks counter (highest - i).
func (w *SlidingWindow) acceptableLocked(counter uint64) bool {
	if !w.started || counter > w.highest {
		return true
	}
	back := w.highest - counter
	if back >= ReplayWindowSize {
		return false
	}
	return w.bitmap[back/64]&(uint64(1)<<(back%64)) == 0
}

func (w *SlidingWindow) setBitLocked(back uint64) {
	w.bitmap[back/64] |= uint64(1) << (back % 64)
}

// shiftLocked moves the bitmap back positions towards higher counters.
func (w *SlidingWindow) shiftLocked(back uint64) {
	words := back / 64
	bits := back % 64
	for i := len(w.bitmap) - 1; i >= 0; i-- {
		var v uint64
		if src := i - int(words); src >= 0 {
			v = w.bitmap[src] << bits
			if bits != 0 && src-1 >= 0 {
				v |= w.bitmap[src-1] >> (64 - bits)
			}
		}
		w.bitmap[i] = v
	}
}
