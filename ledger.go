package netsec

import (
	"errors"
	"sort"
	"sync"
)

// Sentinel errors returned by the ledger. Callers should match these with
// errors.Is rather than comparing strings.
var (
	// ErrInvalidModulus is returned when a ledger is constructed with a
	// modulus that is not a power of two or is smaller than two.
	ErrInvalidModulus = errors.New("netsec: packet-number modulus must be a power of two >= 2")
	// ErrEmptyAck is returned for an acknowledgment carrying no ranges.
	ErrEmptyAck = errors.New("netsec: acknowledgment contains no ranges")
	// ErrInvalidAckRange is returned for a malformed range (first > last) or
	// a packet number outside the connection's packet-number space.
	ErrInvalidAckRange = errors.New("netsec: acknowledgment range out of packet-number space")
	// ErrUnknownPacket is returned when an acknowledgment covers a packet
	// number that no live send record exists for (never sent, or already
	// retired). No state changes are made.
	ErrUnknownPacket = errors.New("netsec: acknowledgment covers unknown packet number")
	// ErrUnauthenticatedPath is returned when an acknowledgment arrives on a
	// path that the host has not registered as authenticated.
	ErrUnauthenticatedPath = errors.New("netsec: acknowledgment on unauthenticated or unknown path")
	// ErrUnknownPath is returned when a send or loss operation names a path
	// the ledger does not know about.
	ErrUnknownPath = errors.New("netsec: unknown path")
	// ErrDuplicatePacket is returned when a packet number that still has a
	// live send record is sent again without being retired first.
	ErrDuplicatePacket = errors.New("netsec: packet number already in flight")
	// ErrNonSequentialPacket is returned when packet numbers do not advance
	// by exactly one modulo the connection modulus.
	ErrNonSequentialPacket = errors.New("netsec: packet numbers must be sequential modulo modulus")
	// ErrWrapWhileInFlight is returned when packet numbers would wrap to a
	// reused slot while earlier packets are still unacknowledged.
	ErrWrapWhileInFlight = errors.New("netsec: cannot wrap packet numbers while packets are in flight")
)

// PathID identifies a path instance. A new migration creates a new PathID;
// IDs are never reused after RemovePath so acknowledgments stay attributable.
type PathID uint64

// FrameRef is an opaque reference to a frame payload owned by the session
// host. The ledger stores and returns references but never dereferences them.
type FrameRef struct {
	ID uint64
}

// AckRange is one inclusive packet-number range carried by an acknowledgment.
type AckRange struct {
	First uint64
	Last  uint64
}

// SendRecord is the immutable record kept for each sent packet. PacketNumber
// lives in the connection-level space; Path is the path instance the packet
// was actually sent on and Frames are the frame references it carried.
type SendRecord struct {
	PacketNumber uint64
	Epoch        uint64
	Path         PathID
	Frames       []FrameRef
}

type entry struct {
	pn     uint64
	epoch  uint64
	path   PathID
	frames []FrameRef
	acked  bool
}

// Ledger is the per-connection acknowledgment ledger. Packet numbers occupy
// one connection-level space even though each send record remembers the path
// instance and frame references used for that transmission. It is safe for
// concurrent use by the session host.
type Ledger struct {
	mu sync.Mutex

	mask    uint64
	modulus uint64
	epoch   uint64

	paths    map[PathID]bool
	live     map[uint64]*entry
	sentSet  intervalSet
	ackedSet intervalSet
	released map[FrameRef]struct{}

	haveLast bool
	lastPN   uint64

	largestAcked   uint64
	haveLargestAck bool
	sentCount      uint64
	ackCount       uint64
	releasedCount  uint64
}

// NewLedger creates a ledger over the full 64-bit packet-number space, which
// effectively never wraps in practice.
func NewLedger() *Ledger {
	// A zero modulus/mask means the full uint64 space: every uint64 value is
	// a legal packet number and natural uint64 overflow implements 2^64 wrap.
	return &Ledger{
		mask:     0,
		modulus:  0,
		paths:    make(map[PathID]bool),
		live:     make(map[uint64]*entry),
		released: make(map[FrameRef]struct{}),
	}
}

// NewLedgerWithModulus creates a ledger whose packet-number space is
// [0, modulus). A small power-of-two modulus is primarily useful to exercise
// wrap behavior deterministically in tests.
func NewLedgerWithModulus(modulus uint64) *Ledger {
	if modulus < 2 || modulus&(modulus-1) != 0 {
		panic(ErrInvalidModulus)
	}
	return &Ledger{
		mask:     modulus - 1,
		modulus:  modulus,
		paths:    make(map[PathID]bool),
		live:     make(map[uint64]*entry),
		released: make(map[FrameRef]struct{}),
	}
}

// AddPath registers a path instance as authenticated and eligible to carry
// acknowledgments.
func (l *Ledger) AddPath(p PathID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paths[p] = true
}

// RemovePath marks a path instance as gone. Acknowledgments arriving on it
// afterwards are rejected; in-flight send records retain their original path
// attribution until acknowledged or retired.
func (l *Ledger) RemovePath(p PathID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.paths, p)
}

// PathAuthenticated reports whether p is currently registered.
func (l *Ledger) PathAuthenticated(p PathID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.paths[p]
}

// WasSent reports whether pn has a live send record (a packet that was
// really transmitted and has not been retired).
func (l *Ledger) WasSent(pn uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.live[pn]
	return ok
}

// WasAcked reports whether pn has been acknowledged at least once.
func (l *Ledger) WasAcked(pn uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ackedSet.contains(pn)
}

// SentRanges returns the normalized interval set of live send records.
func (l *Ledger) SentRanges() []AckRange {
	l.mu.Lock()
	defer l.mu.Unlock()
	return exportRanges(l.sentSet.ranges)
}

// AckedRanges returns the normalized interval set of acknowledged packet
// numbers, including those whose send records have been retired.
func (l *Ledger) AckedRanges() []AckRange {
	l.mu.Lock()
	defer l.mu.Unlock()
	return exportRanges(l.ackedSet.ranges)
}

func exportRanges(in []interval) []AckRange {
	out := make([]AckRange, len(in))
	for i, r := range in {
		out[i] = AckRange{First: r.first, Last: r.last}
	}
	return out
}

// Sent records that packet pn was transmitted on path path carrying frames.
// Packet numbers must advance sequentially modulo the modulus; wrapping is
// permitted only after every earlier packet has been acknowledged and
// retired, so a reused slot can never alias a live record.
func (l *Ledger) Sent(pn uint64, path PathID, frames []FrameRef) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.paths[path] {
		return ErrUnknownPath
	}
	if l.modulus != 0 && pn > l.mask {
		return ErrInvalidAckRange
	}
	if l.haveLast {
		want := l.lastPN + 1
		if l.modulus != 0 {
			want &= l.mask
		}
		if pn != want {
			return ErrNonSequentialPacket
		}
		if pn <= l.lastPN && len(l.live) != 0 {
			return ErrWrapWhileInFlight
		}
		if pn <= l.lastPN {
			l.epoch++
		}
	}
	if _, exists := l.live[pn]; exists {
		return ErrDuplicatePacket
	}

	frameCopy := append([]FrameRef(nil), frames...)
	l.live[pn] = &entry{
		pn:     pn,
		epoch:  l.epoch,
		path:   path,
		frames: frameCopy,
	}
	l.sentSet.add(pn, pn)
	l.lastPN = pn
	l.haveLast = true
	l.sentCount++
	return nil
}

// AckRanges processes an authenticated acknowledgment arriving on path. Every
// acknowledged packet must have a live send record; a single unknown number
// rejects the whole acknowledgment without side effects. Each distinct frame
// reference is returned at most once, even when a frame was carried by
// several packets or the same packet is acknowledged repeatedly.
func (l *Ledger) AckRanges(path PathID, ranges []AckRange) (released []FrameRef, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.paths[path] {
		return nil, ErrUnauthenticatedPath
	}
	if len(ranges) == 0 {
		return nil, ErrEmptyAck
	}

	for _, r := range ranges {
		if r.First > r.Last || (l.modulus != 0 && (r.First > l.mask || r.Last > l.mask)) {
			return nil, ErrInvalidAckRange
		}
	}

	for _, r := range ranges {
		for pn, ok := r.First, true; ok; pn, ok = nextPN(pn, r.Last) {
			if _, exists := l.live[pn]; !exists {
				return nil, ErrUnknownPacket
			}
		}
	}

	for _, r := range ranges {
		for pn, ok := r.First, true; ok; pn, ok = nextPN(pn, r.Last) {
			rec := l.live[pn]
			if !rec.acked {
				rec.acked = true
				l.ackedSet.add(pn, pn)
				l.ackCount++
				if !l.haveLargestAck || pn > l.largestAcked {
					l.largestAcked = pn
					l.haveLargestAck = true
				}
			}
			for _, fr := range rec.frames {
				if _, seen := l.released[fr]; !seen {
					l.released[fr] = struct{}{}
					released = append(released, fr)
					l.releasedCount++
				}
			}
		}
	}
	return released, nil
}

// MarkLost declares the given packet numbers lost and returns the frame
// references they carried that have not been released yet, so the host can
// retransmit them. Unknown or already-acknowledged packets are an error and
// leave state untouched.
func (l *Ledger) MarkLost(pns ...uint64) ([]FrameRef, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, pn := range pns {
		rec, ok := l.live[pn]
		if !ok || rec.acked {
			return nil, ErrUnknownPacket
		}
	}

	seen := make(map[FrameRef]struct{})
	var out []FrameRef
	for _, pn := range pns {
		for _, fr := range l.live[pn].frames {
			if _, released := l.released[fr]; released {
				continue
			}
			if _, dup := seen[fr]; dup {
				continue
			}
			seen[fr] = struct{}{}
			out = append(out, fr)
		}
	}
	return out, nil
}

// RetireAcked evicts every acknowledged send record so its packet-number
// slot can be reused after a wrap. Unacknowledged records are retained.
// Returns the number of records retired.
func (l *Ledger) RetireAcked() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	retired := 0
	for pn, rec := range l.live {
		if !rec.acked {
			continue
		}
		delete(l.live, pn)
		l.sentSet.remove(pn, pn)
		retired++
	}
	return retired
}

// Record returns a copy of the send record for pn and true, or false when no
// live record exists.
func (l *Ledger) Record(pn uint64) (SendRecord, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.live[pn]
	if !ok {
		return SendRecord{}, false
	}
	return SendRecord{
		PacketNumber: rec.pn,
		Epoch:        rec.epoch,
		Path:         rec.path,
		Frames:       append([]FrameRef(nil), rec.frames...),
	}, true
}

// InFlight returns the packet numbers that still lack an acknowledgment, in
// ascending order.
func (l *Ledger) InFlight() []uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	var out []uint64
	for pn, rec := range l.live {
		if !rec.acked {
			out = append(out, pn)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// LargestAcked returns the largest acknowledged packet number and true, or
// false before any acknowledgment.
func (l *Ledger) LargestAcked() (uint64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.largestAcked, l.haveLargestAck
}

// Epoch returns the current packet-number epoch. The epoch increments each
// time numbers wrap, which is only possible after retirement.
func (l *Ledger) Epoch() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.epoch
}

// Modulus returns the packet-number modulus. A return value of zero denotes
// the full 2^64 space, which cannot be represented in a uint64.
func (l *Ledger) Modulus() uint64 {
	return l.modulus
}

// SentCount returns the number of send records accepted.
func (l *Ledger) SentCount() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sentCount
}

// AckedCount returns the number of distinct packets acknowledged.
func (l *Ledger) AckedCount() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ackCount
}

// ReleasedCount returns the number of distinct frame references released.
func (l *Ledger) ReleasedCount() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.releasedCount
}

// nextPN advances pn toward the inclusive stop without overflowing: it
// returns the next value and true, or zero and false once pn == stop.
func nextPN(pn, stop uint64) (uint64, bool) {
	if pn >= stop {
		return 0, false
	}
	return pn + 1, true
}
