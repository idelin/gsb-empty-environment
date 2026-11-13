package netsec

import (
	"fmt"
	"sort"
	"sync"
)

// Policy selects which copy of an overlapping byte is used by the target
// host's TCP stack.
type Policy int

// ReassemblyPolicy is an alias for Policy.
type ReassemblyPolicy = Policy

const (
	// FirstArrival keeps the byte from the segment that arrived first.
	FirstArrival Policy = iota
	// LastArrival replaces an existing byte with the byte from the segment
	// that arrived most recently.
	LastArrival
)

// Common aliases for the two reassembly policies.
const (
	FirstComeFirstServed = FirstArrival
	LastComeFirstServed  = LastArrival
	FirstWins            = FirstArrival
	LastWins             = LastArrival
	PolicyFirstArrival   = FirstArrival
	PolicyLastArrival    = LastArrival
)

func (p Policy) String() string {
	switch p {
	case FirstArrival:
		return "first-arrival"
	case LastArrival:
		return "last-arrival"
	default:
		return fmt.Sprintf("Policy(%d)", int(p))
	}
}

// Segment is one TCP segment's sequence number and payload.
type Segment struct {
	Seq  uint32
	Data []byte
}

// Conflict describes one contiguous run of overlapping bytes whose values
// disagree. Start and End are inclusive and exclusive sequence numbers in the
// TCP 32-bit sequence space, so End wraps to zero when the run crosses the
// wrap point. StartOffset and EndOffset are the corresponding stream offsets
// relative to the first segment observed; they do not wrap and are therefore
// useful for arithmetic.
type Conflict struct {
	Start       uint32
	End         uint32
	StartOffset int64
	EndOffset   int64
	Length      int
}

// ConflictHandler is called once for every conflict detected by Add.
type ConflictHandler func(Conflict)

type piece struct {
	start int64
	data  []byte
}

func (p piece) end() int64 {
	return p.start + int64(len(p.data))
}

type conflictSpan struct {
	start int64
	end   int64
}

// Reassembler maintains the reassembled byte stream for one TCP flow.
//
// Sequence numbers are compared with 32-bit modular arithmetic: a segment at
// 0 is correctly treated as following a segment near 0xffffffff. A Reassembler
// must not be copied after first use; its methods are safe for concurrent use.
type Reassembler struct {
	mu sync.Mutex

	policy      Policy
	baseSeq     uint32
	haveBase    bool
	pieces      []piece
	conflicts   []Conflict
	conflictHdl ConflictHandler
}

// NewReassembler returns a reassembler using the supplied target-host policy.
// It panics if the policy is not FirstArrival or LastArrival.
func NewReassembler(policy Policy) *Reassembler {
	if policy != FirstArrival && policy != LastArrival {
		panic(fmt.Sprintf("netsec: invalid reassembly policy %d", int(policy)))
	}
	return &Reassembler{policy: policy}
}

// New is shorthand for NewReassembler.
func New(policy Policy) *Reassembler {
	return NewReassembler(policy)
}

// Policy returns the configured reassembly policy.
func (r *Reassembler) Policy() Policy {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.policy
}

// SetConflictHandler installs a callback invoked for each conflict found by
// Add. Passing nil removes the handler.
func (r *Reassembler) SetConflictHandler(handler ConflictHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conflictHdl = handler
}

// Add inserts a segment and returns the conflicts caused by this segment.
// A conflict is emitted only for overlapping bytes that differ. Overlaps with
// identical content, and non-overlapping gaps filled later, do not produce
// alerts.
func (r *Reassembler) Add(seq uint32, data []byte) []Conflict {
	if len(data) == 0 {
		return nil
	}

	r.mu.Lock()
	if !r.haveBase {
		r.baseSeq = seq
		r.haveBase = true
	}
	offset := r.offset(seq)
	spans := r.findConflicts(offset, data)
	var conflicts []Conflict
	if len(spans) != 0 {
		conflicts = make([]Conflict, len(spans))
		for i, span := range spans {
			conflicts[i] = r.makeConflict(span)
		}
	}
	if len(conflicts) != 0 {
		r.conflicts = append(r.conflicts, conflicts...)
	}
	if r.policy == LastArrival {
		r.overwrite(offset, data)
	} else {
		r.fillGaps(offset, data)
	}
	handler := r.conflictHdl
	r.mu.Unlock()

	if handler != nil {
		for _, conflict := range conflicts {
			handler(conflict)
		}
	}
	return conflicts
}

// AddSegment is an explicit alias for Add.
func (r *Reassembler) AddSegment(seq uint32, data []byte) []Conflict {
	return r.Add(seq, data)
}

// Feed inserts a Segment.
func (r *Reassembler) Feed(segment Segment) []Conflict {
	return r.Add(segment.Seq, segment.Data)
}

// Stream returns the contiguous prefix of the reassembled stream, starting at
// the lowest sequence number observed. Bytes after an unfilled gap are omitted
// until the gap is filled by a later segment.
func (r *Reassembler) Stream() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.pieces) == 0 {
		return nil
	}
	first := r.pieces[0]
	cursor := first.start
	result := make([]byte, 0, int(first.end()-cursor))
	for _, p := range r.pieces {
		if p.start > cursor {
			break
		}
		if p.end() <= cursor {
			continue
		}
		result = append(result, p.data[cursor-p.start:]...)
		cursor = p.end()
	}
	return result
}

// Data is an alias for Stream.
func (r *Reassembler) Data() []byte {
	return r.Stream()
}

// Conflicts returns every conflict observed so far, in arrival order.
func (r *Reassembler) Conflicts() []Conflict {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]Conflict, len(r.conflicts))
	copy(result, r.conflicts)
	return result
}

// Reassemble is a convenience function for reassembling a complete segment
// list and returning both the stream prefix and all conflicts.
func Reassemble(policy Policy, segments []Segment) ([]byte, []Conflict) {
	r := NewReassembler(policy)
	for _, segment := range segments {
		r.Add(segment.Seq, segment.Data)
	}
	return r.Stream(), r.Conflicts()
}

func (r *Reassembler) offset(seq uint32) int64 {
	// int32 arithmetic performs the required 32-bit wrap-aware comparison.
	return int64(int32(seq - r.baseSeq))
}

func (r *Reassembler) makeConflict(span conflictSpan) Conflict {
	return Conflict{
		Start:       uint32(int64(r.baseSeq) + span.start),
		End:         uint32(int64(r.baseSeq) + span.end),
		StartOffset: span.start,
		EndOffset:   span.end,
		Length:      int(span.end - span.start),
	}
}

func (r *Reassembler) findConflicts(offset int64, data []byte) []conflictSpan {
	end := offset + int64(len(data))
	var spans []conflictSpan

	for _, p := range r.pieces {
		if p.end() <= offset {
			continue
		}
		if p.start >= end {
			break
		}

		overlapStart := max(offset, p.start)
		overlapEnd := min(end, p.end())
		var runStart int64
		inRun := false
		for pos := overlapStart; pos < overlapEnd; pos++ {
			oldByte := p.data[pos-p.start]
			newByte := data[pos-offset]
			if oldByte != newByte {
				if !inRun {
					runStart = pos
					inRun = true
				}
				continue
			}
			if inRun {
				spans = append(spans, conflictSpan{start: runStart, end: pos})
				inRun = false
			}
		}
		if inRun {
			spans = append(spans, conflictSpan{start: runStart, end: overlapEnd})
		}
	}

	return mergeSpans(spans)
}

func mergeSpans(spans []conflictSpan) []conflictSpan {
	if len(spans) < 2 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].start < spans[j].start
	})
	merged := spans[:1]
	for _, span := range spans[1:] {
		last := &merged[len(merged)-1]
		if span.start <= last.end {
			if span.end > last.end {
				last.end = span.end
			}
			continue
		}
		merged = append(merged, span)
	}
	return merged
}

// fillGaps implements first-arrival semantics by adding only bytes not
// already covered by an earlier segment.
func (r *Reassembler) fillGaps(offset int64, data []byte) {
	end := offset + int64(len(data))
	cursor := offset
	var additions []piece

	for _, p := range r.pieces {
		if p.end() <= cursor {
			continue
		}
		if p.start >= end {
			break
		}
		if p.start > cursor {
			lo := cursor
			hi := min(p.start, end)
			additions = append(additions, piece{
				start: lo,
				data:  cloneBytes(data[lo-offset : hi-offset]),
			})
		}
		if p.end() > cursor {
			cursor = p.end()
		}
		if cursor >= end {
			break
		}
	}
	if cursor < end {
		additions = append(additions, piece{
			start: cursor,
			data:  cloneBytes(data[cursor-offset:]),
		})
	}

	if len(additions) != 0 {
		r.pieces = append(r.pieces, additions...)
		r.coalesce()
	}
}

// overwrite implements last-arrival semantics by replacing all bytes in the
// new segment's range.
func (r *Reassembler) overwrite(offset int64, data []byte) {
	end := offset + int64(len(data))
	var kept []piece

	for _, p := range r.pieces {
		if p.end() <= offset || p.start >= end {
			kept = append(kept, p)
			continue
		}
		if p.start < offset {
			kept = append(kept, piece{
				start: p.start,
				data:  cloneBytes(p.data[:offset-p.start]),
			})
		}
		if p.end() > end {
			kept = append(kept, piece{
				start: end,
				data:  cloneBytes(p.data[end-p.start:]),
			})
		}
	}
	kept = append(kept, piece{start: offset, data: cloneBytes(data)})
	r.pieces = kept
	r.coalesce()
}

func (r *Reassembler) coalesce() {
	sort.Slice(r.pieces, func(i, j int) bool {
		return r.pieces[i].start < r.pieces[j].start
	})

	out := r.pieces[:0]
	for _, p := range r.pieces {
		if len(out) == 0 {
			out = append(out, p)
			continue
		}
		last := &out[len(out)-1]
		if p.start > last.end() {
			out = append(out, p)
			continue
		}
		if p.end() <= last.end() {
			continue
		}
		overlap := last.end() - p.start
		last.data = append(last.data, p.data[overlap:]...)
	}
	r.pieces = out
}

func cloneBytes(data []byte) []byte {
	result := make([]byte, len(data))
	copy(result, data)
	return result
}
