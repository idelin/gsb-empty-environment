// Package receiver implements a rolling-code access-control receiver with
// a write-ahead log. A valid code triggers the unlock callback at most
// once, even across power loss: the accept record is fsynced before the
// callback runs, and on recovery pending unlocks are re-issued with the
// same idempotent operation number.
package receiver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	// ErrCrashed is returned after a simulated power-loss point; the
	// receiver is halted and must be re-opened to recover.
	ErrCrashed = errors.New("receiver: halted after power loss")
	// ErrBadFrame is returned for frames with the wrong length.
	ErrBadFrame = errors.New("receiver: malformed frame")
)

const (
	tagLen   = 16
	FrameLen = 8 + 4 + tagLen
)

// Frame is one rolling-code transmission: device ID, monotonic counter and
// a truncated HMAC-SHA256 tag over both.
type Frame struct {
	DeviceID uint64
	Counter  uint32
	Tag      [tagLen]byte
}

// Sign builds a valid frame for deviceID/counter under key.
func Sign(key []byte, deviceID uint64, counter uint32) Frame {
	f := Frame{DeviceID: deviceID, Counter: counter}
	mac := hmac.New(sha256.New, key)
	var msg [12]byte
	binary.BigEndian.PutUint64(msg[0:8], deviceID)
	binary.BigEndian.PutUint32(msg[8:12], counter)
	mac.Write(msg[:])
	copy(f.Tag[:], mac.Sum(nil)[:tagLen])
	return f
}

func (f Frame) verify(key []byte) bool {
	want := Sign(key, f.DeviceID, f.Counter)
	return hmac.Equal(f.Tag[:], want.Tag[:])
}

// Marshal encodes the frame on the wire.
func (f Frame) Marshal() []byte {
	b := make([]byte, 0, FrameLen)
	b = binary.BigEndian.AppendUint64(b, f.DeviceID)
	b = binary.BigEndian.AppendUint32(b, f.Counter)
	return append(b, f.Tag[:]...)
}

// ParseFrame decodes a wire frame.
func ParseFrame(b []byte) (Frame, error) {
	if len(b) != FrameLen {
		return Frame{}, ErrBadFrame
	}
	var f Frame
	f.DeviceID = binary.BigEndian.Uint64(b[0:8])
	f.Counter = binary.BigEndian.Uint32(b[8:12])
	copy(f.Tag[:], b[12:])
	return f, nil
}

// UnlockOp is one idempotent unlock operation. OpID is stable for a given
// (device, counter), so a door controller can deduplicate retries after
// power loss.
type UnlockOp struct {
	DeviceID uint64
	Counter  uint32
	OpID     string
}

// UnlockFunc fires the door. It must be idempotent on UnlockOp.OpID and
// must not call back into the Receiver.
type UnlockFunc func(ctx context.Context, op UnlockOp) error

// CrashPoint identifies a simulated power-loss point (tests only).
type CrashPoint int

const (
	// CrashAfterAccept is after the accept record is fsynced but before
	// the unlock callback runs.
	CrashAfterAccept CrashPoint = iota + 1
	// CrashBeforeDone is after the unlock callback returned but before
	// the done record is written.
	CrashBeforeDone
)

// Options tunes a Receiver. Zero fields take defaults.
type Options struct {
	// Window is the sliding accept window size (default 16).
	Window uint32
	// ResyncWindow bounds how far ahead a two-code resync may jump
	// (default 32768).
	ResyncWindow uint32
	// CrashHook, if set, is consulted at each CrashPoint; returning true
	// simulates power loss (the receiver halts with ErrCrashed).
	CrashHook func(CrashPoint) bool
}

func (o Options) withDefaults() Options {
	if o.Window == 0 {
		o.Window = 16
	}
	if o.ResyncWindow == 0 {
		o.ResyncWindow = 1 << 15
	}
	return o
}

// KeyStore resolves a device ID to its shared secret.
type KeyStore interface {
	Key(deviceID uint64) ([]byte, bool)
}

// MapKeyStore is a static in-memory KeyStore.
type MapKeyStore map[uint64][]byte

func (m MapKeyStore) Key(deviceID uint64) ([]byte, bool) {
	k, ok := m[deviceID]
	return k, ok
}

// Result is the outcome of Submit.
type Result int

const (
	ResultUnlocked     Result = iota // code accepted, unlock fired
	ResultReplay                     // counter already consumed
	ResultOutOfWindow                // counter beyond the resync window
	ResultResyncArmed                // first of two resync codes stored
	ResultBadAuth                    // unknown device or bad tag
)

func (r Result) String() string {
	switch r {
	case ResultUnlocked:
		return "UNLOCKED"
	case ResultReplay:
		return "REPLAY"
	case ResultOutOfWindow:
		return "OUT_OF_WINDOW"
	case ResultResyncArmed:
		return "RESYNC_ARMED"
	case ResultBadAuth:
		return "BAD_AUTH"
	}
	return "UNKNOWN"
}

type deviceState struct {
	lastAccepted  uint32
	hasAccepted   bool
	resyncPending uint32
	hasResync     bool
	pending       map[string]uint32 // opID -> counter, accept without done
}

// Receiver verifies rolling codes against a sliding window and fires the
// unlock callback only after the accept record is durable. Safe for
// concurrent use by multiple reader goroutines.
type Receiver struct {
	mu     sync.Mutex
	wal    *wal
	keys   KeyStore
	unlock UnlockFunc
	opts   Options
	states map[uint64]*deviceState
	halted bool
}

// Open opens (or recovers) a receiver on the WAL at walPath. Recovery
// replays the log, rebuilds the sliding windows, and re-issues any unlock
// whose accept record lacks a done record, using the original OpID so the
// door controller can deduplicate it.
func Open(ctx context.Context, walPath string, keys KeyStore, unlock UnlockFunc, opts *Options) (*Receiver, error) {
	o := Options{}.withDefaults()
	if opts != nil {
		merged := *opts
		if merged.Window == 0 {
			merged.Window = o.Window
		}
		if merged.ResyncWindow == 0 {
			merged.ResyncWindow = o.ResyncWindow
		}
		o = merged
	}
	w, recs, err := openWAL(walPath)
	if err != nil {
		return nil, err
	}
	r := &Receiver{
		wal:    w,
		keys:   keys,
		unlock: unlock,
		opts:   o,
		states: make(map[uint64]*deviceState),
	}
	for _, rec := range recs {
		st := r.stateFor(rec.deviceID)
		switch rec.typ {
		case recAccept:
			if !st.hasAccepted || rec.counter > st.lastAccepted {
				st.lastAccepted, st.hasAccepted = rec.counter, true
			}
			st.hasResync = false
			st.pending[rec.opID] = rec.counter
		case recDone:
			delete(st.pending, rec.opID)
		case recResync:
			st.resyncPending, st.hasResync = rec.counter, true
		}
	}
	// Re-issue pending unlocks idempotently, in deterministic order.
	for _, dev := range r.sortedDevices() {
		st := r.states[dev]
		for _, opID := range sortedKeys(st.pending) {
			op := UnlockOp{DeviceID: dev, Counter: st.pending[opID], OpID: opID}
			if err := unlock(ctx, op); err != nil {
				w.close()
				return nil, fmt.Errorf("receiver: recovery unlock %s: %w", opID, err)
			}
			if err := w.append(walRecord{typ: recDone, deviceID: dev, counter: op.Counter, opID: opID}); err != nil {
				w.close()
				return nil, err
			}
			delete(st.pending, opID)
		}
	}
	return r, nil
}

// Close flushes and closes the WAL.
func (r *Receiver) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.wal.close()
}

// Submit verifies one frame and, if accepted, durably records it and fires
// the unlock callback exactly once per (device, counter).
func (r *Receiver) Submit(ctx context.Context, f Frame) (Result, error) {
	key, ok := r.keys.Key(f.DeviceID)
	if !ok || !f.verify(key) {
		return ResultBadAuth, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.halted {
		return 0, ErrCrashed
	}
	st := r.stateFor(f.DeviceID)
	c := f.Counter

	switch {
	case st.hasAccepted && c <= st.lastAccepted:
		return ResultReplay, nil
	case !st.hasAccepted || c <= st.lastAccepted+r.opts.Window:
		// Inside the sliding window: accept below.
	case c <= st.lastAccepted+r.opts.ResyncWindow:
		// Beyond the window: require two consecutive counters to resync.
		if !(st.hasResync && c == st.resyncPending+1) {
			st.resyncPending, st.hasResync = c, true
			if err := r.wal.append(walRecord{typ: recResync, deviceID: f.DeviceID, counter: c}); err != nil {
				return 0, err
			}
			return ResultResyncArmed, nil
		}
	default:
		return ResultOutOfWindow, nil
	}

	opID := fmt.Sprintf("%016x:%08x", f.DeviceID, c)
	if err := r.wal.append(walRecord{typ: recAccept, deviceID: f.DeviceID, counter: c, opID: opID}); err != nil {
		return 0, err
	}
	// The accept is durable; advance the window before firing so no
	// racing reader can accept the same counter again.
	st.lastAccepted, st.hasAccepted = c, true
	st.hasResync = false
	st.pending[opID] = c

	if r.crash(CrashAfterAccept) {
		return 0, ErrCrashed
	}
	if err := r.unlock(ctx, UnlockOp{DeviceID: f.DeviceID, Counter: c, OpID: opID}); err != nil {
		return 0, fmt.Errorf("receiver: unlock %s: %w", opID, err)
	}
	if r.crash(CrashBeforeDone) {
		return 0, ErrCrashed
	}
	if err := r.wal.append(walRecord{typ: recDone, deviceID: f.DeviceID, counter: c, opID: opID}); err != nil {
		return 0, err
	}
	delete(st.pending, opID)
	return ResultUnlocked, nil
}

func (r *Receiver) crash(p CrashPoint) bool {
	if r.opts.CrashHook != nil && r.opts.CrashHook(p) {
		r.halted = true
		return true
	}
	return false
}

func (r *Receiver) stateFor(id uint64) *deviceState {
	st, ok := r.states[id]
	if !ok {
		st = &deviceState{pending: make(map[string]uint32)}
		r.states[id] = st
	}
	return st
}

func (r *Receiver) sortedDevices() []uint64 {
	devs := make([]uint64, 0, len(r.states))
	for d := range r.states {
		devs = append(devs, d)
	}
	sort.Slice(devs, func(i, j int) bool { return devs[i] < devs[j] })
	return devs
}

func sortedKeys(m map[string]uint32) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
