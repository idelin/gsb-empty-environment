package netsec

import "math"

// maxSeqSpan is the maximum forward distance a subflow sequence number may
// advance relative to the authenticated checkpoint (2^31).
const maxSeqSpan = 1 << 31

// Entry is a single DSS mapping submitted by a subflow.
type Entry struct {
	SubflowID uint32 // unique subflow identifier
	SSN       uint32 // 32-bit subflow sequence start
	DSN       uint64 // 64-bit connection-level data sequence start (never wraps)
	Length    uint16 // 16-bit mapping length
	FIN       bool   // binds the end-of-data-stream DSN
	Payload   []byte // must satisfy len(Payload) == int(Length)
	AuthGen   uint64 // authentication generation, passed to the Authenticator
}

// Authenticator decides whether an entry is cryptographically acceptable.
// The ledger only commits an entry after the Authenticator approves it.
type Authenticator func(e Entry) bool

// Result describes the outcome of a Submit call.
type Result int

const (
	Accepted              Result = iota
	RejectedPayloadLen           // len(Payload) != Length
	RejectedZeroLength           // zero-length mapping without FIN
	RejectedOverflow             // DSN + Length overflows uint64
	RejectedSubflowFrozen        // subflow was frozen by a prior conflict
	RejectedSeqWindow            // SSN outside the 2^31 window of the checkpoint
	RejectedAfterEnd             // mapping extends beyond the FIN-bound end
	RejectedAuth                 // authenticator rejected the entry
	RejectedConflict             // overlapping byte conflict; subflow frozen
	RejectedFINConflict          // FIN disagrees on the end DSN; subflow frozen
)

func (r Result) String() string {
	switch r {
	case Accepted:
		return "Accepted"
	case RejectedPayloadLen:
		return "RejectedPayloadLen"
	case RejectedZeroLength:
		return "RejectedZeroLength"
	case RejectedOverflow:
		return "RejectedOverflow"
	case RejectedSubflowFrozen:
		return "RejectedSubflowFrozen"
	case RejectedSeqWindow:
		return "RejectedSeqWindow"
	case RejectedAfterEnd:
		return "RejectedAfterEnd"
	case RejectedAuth:
		return "RejectedAuth"
	case RejectedConflict:
		return "RejectedConflict"
	case RejectedFINConflict:
		return "RejectedFINConflict"
	}
	return "Unknown"
}

type subflow struct {
	checkpoint  uint32
	established bool
	frozen      bool
}

type segment struct {
	start, end uint64 // [start, end) in DSN space
	data       []byte // len(data) == end-start
	src        map[uint32]struct{}
}

// Ledger is the connection-level DSS mapping ledger.
type Ledger struct {
	auth      Authenticator
	subflows  map[uint32]*subflow
	segs      []*segment // sorted, non-overlapping
	end       uint64
	hasEnd    bool
	delivered uint64
}

// NewLedger creates a ledger using auth as the authentication callback.
// A nil auth accepts every entry.
func NewLedger(auth Authenticator) *Ledger {
	return &Ledger{auth: auth, subflows: make(map[uint32]*subflow)}
}

// Submit validates and, on success, commits one DSS mapping to the ledger.
func (l *Ledger) Submit(e Entry) Result {
	if len(e.Payload) != int(e.Length) {
		return RejectedPayloadLen
	}
	if e.Length == 0 && !e.FIN {
		return RejectedZeroLength
	}
	if e.DSN > math.MaxUint64-uint64(e.Length) {
		return RejectedOverflow
	}
	end := e.DSN + uint64(e.Length)

	sf := l.subflows[e.SubflowID]
	if sf != nil && sf.frozen {
		return RejectedSubflowFrozen
	}
	if e.FIN && l.hasEnd && l.end != end {
		l.freeze(e.SubflowID)
		return RejectedFINConflict
	}
	if l.hasEnd && end > l.end {
		return RejectedAfterEnd
	}
	if sf != nil && sf.established {
		if delta := e.SSN - sf.checkpoint; delta >= maxSeqSpan {
			return RejectedSeqWindow
		}
	}
	if l.auth != nil && !l.auth(e) {
		return RejectedAuth
	}
	if e.Length > 0 && l.conflicts(e.DSN, e.Payload) {
		l.freeze(e.SubflowID)
		return RejectedConflict
	}

	if sf == nil {
		sf = &subflow{}
		l.subflows[e.SubflowID] = sf
	}
	sf.established = true
	sf.checkpoint = e.SSN + uint32(e.Length)
	if e.FIN {
		l.hasEnd = true
		l.end = end
	}
	if e.Length > 0 {
		l.insert(e.DSN, e.Payload, e.SubflowID)
	}
	return Accepted
}

func (l *Ledger) freeze(id uint32) {
	sf := l.subflows[id]
	if sf == nil {
		sf = &subflow{}
		l.subflows[id] = sf
	}
	sf.frozen = true
}

// conflicts reports whether any byte of [dsn, dsn+len(payload)) differs
// from an already committed byte.
func (l *Ledger) conflicts(dsn uint64, payload []byte) bool {
	s, e := dsn, dsn+uint64(len(payload))
	for _, seg := range l.segs {
		if seg.end <= s {
			continue
		}
		if seg.start >= e {
			break
		}
		lo := max(seg.start, s)
		hi := min(seg.end, e)
		for i := lo; i < hi; i++ {
			if seg.data[i-seg.start] != payload[i-s] {
				return true
			}
		}
	}
	return false
}

// insert commits payload at dsn, merging the subflow into the source set of
// every overlapping byte and creating new segments for uncovered ranges.
// Existing segments are split at the entry boundaries so source sets stay
// accurate per byte.
func (l *Ledger) insert(dsn uint64, payload []byte, id uint32) {
	s, e := dsn, dsn+uint64(len(payload))
	out := make([]*segment, 0, len(l.segs)+2)
	cur := s
	i := 0
	for ; i < len(l.segs); i++ {
		if l.segs[i].end > s {
			break
		}
		out = append(out, l.segs[i])
	}
	for ; i < len(l.segs); i++ {
		seg := l.segs[i]
		if seg.start >= e {
			break
		}
		if seg.start < s {
			out = append(out, &segment{start: seg.start, end: s, data: seg.data[:s-seg.start], src: seg.src})
		} else if cur < seg.start {
			out = append(out, newSegment(cur, seg.start, payload[cur-s:seg.start-s], id))
		}
		lo := max(seg.start, s)
		hi := min(seg.end, e)
		src := make(map[uint32]struct{}, len(seg.src)+1)
		for k := range seg.src {
			src[k] = struct{}{}
		}
		src[id] = struct{}{}
		out = append(out, &segment{start: lo, end: hi, data: seg.data[lo-seg.start : hi-seg.start], src: src})
		if seg.end > e {
			out = append(out, &segment{start: e, end: seg.end, data: seg.data[e-seg.start:], src: seg.src})
		}
		cur = hi
	}
	if cur < e {
		out = append(out, newSegment(cur, e, payload[cur-s:e-s], id))
	}
	out = append(out, l.segs[i:]...)
	l.segs = out
}

func newSegment(start, end uint64, data []byte, id uint32) *segment {
	cp := make([]byte, len(data))
	copy(cp, data)
	return &segment{start: start, end: end, data: cp, src: map[uint32]struct{}{id: {}}}
}

// Deliver returns the contiguous, byte-consistent prefix starting at the
// next undelivered DSN. Every byte is delivered at most once; later
// retransmissions only merge source sets.
func (l *Ledger) Deliver() []byte {
	var out []byte
	for {
		var hit *segment
		for _, seg := range l.segs {
			if seg.start <= l.delivered && l.delivered < seg.end {
				hit = seg
				break
			}
		}
		if hit == nil {
			break
		}
		out = append(out, hit.data[l.delivered-hit.start:]...)
		l.delivered = hit.end
	}
	return out
}

// Delivered returns the next undelivered DSN.
func (l *Ledger) Delivered() uint64 { return l.delivered }

// Frozen reports whether the subflow was frozen by a conflict.
func (l *Ledger) Frozen(id uint32) bool {
	sf := l.subflows[id]
	return sf != nil && sf.frozen
}

// End returns the FIN-bound end-of-data-stream DSN, if known.
func (l *Ledger) End() (uint64, bool) { return l.end, l.hasEnd }

// Sources returns the sorted set of subflow IDs that contributed the byte
// at the given DSN, or nil if the DSN is not covered.
func (l *Ledger) Sources(dsn uint64) []uint32 {
	for _, seg := range l.segs {
		if seg.start <= dsn && dsn < seg.end {
			out := make([]uint32, 0, len(seg.src))
			for id := range seg.src {
				out = append(out, id)
			}
			for i := 1; i < len(out); i++ {
				for j := i; j > 0 && out[j-1] > out[j]; j-- {
					out[j-1], out[j] = out[j], out[j-1]
				}
			}
			return out
		}
	}
	return nil
}
