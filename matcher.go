package blockmatch

import (
	"bytes"
	"crypto/sha256"
)

// OpKind distinguishes the two kinds of output operations.
type OpKind int

const (
	// Literal covers bytes of the new stream that matched nothing in the
	// old data; the bytes are carried in Op.Data.
	Literal OpKind = iota
	// Match covers bytes of the new stream identical to old[Offset:Offset+Length].
	Match
)

// Op is one output operation. Applied in order, the ops cover the whole new
// stream exactly once: no gaps, no overlaps.
type Op struct {
	Kind   OpKind
	Offset int    // Match: offset into the old data. Literal: offset into the new stream.
	Length int    // number of new-stream bytes covered
	Data   []byte // Literal only: the unmatched bytes themselves
}

// Options configures indexing and matching. The same Options must be used
// for BuildIndex and (via the Index) for the Matcher.
type Options struct {
	// BlockSize is the sliding-window / block length in bytes.
	BlockSize int
	// WeakBits truncates the rolling fingerprint to this many bits when
	// used as an index key (0 keeps all 64 bits). Deliberately small values
	// are useful to force fingerprint collisions in tests; correctness does
	// not depend on it because candidates are always re-verified.
	WeakBits uint
}

func (o Options) weakKey(h uint64) uint64 {
	if o.WeakBits == 0 || o.WeakBits >= 64 {
		return h
	}
	return h & ((uint64(1) << o.WeakBits) - 1)
}

// block is one indexed chunk of the old data.
type block struct {
	strong [sha256.Size]byte
	offset int
	length int
	data   []byte // reference into the old data, used for content verification
}

// Index maps weak fingerprints of old-data blocks to candidate blocks.
type Index struct {
	opts   Options
	byWeak map[uint64][]block
}

// BuildIndex chunks old data into non-overlapping BlockSize blocks and
// indexes them by weak fingerprint. A trailing partial block is not
// indexed (a full window can never equal it).
func BuildIndex(old []byte, opts Options) *Index {
	if opts.BlockSize <= 0 {
		panic("blockmatch: BlockSize must be positive")
	}
	idx := &Index{opts: opts, byWeak: make(map[uint64][]block)}
	for off := 0; off+opts.BlockSize <= len(old); off += opts.BlockSize {
		chunk := old[off : off+opts.BlockSize]
		key := opts.weakKey(Fingerprint(chunk))
		idx.byWeak[key] = append(idx.byWeak[key], block{
			strong: sha256.Sum256(chunk),
			offset: off,
			length: opts.BlockSize,
			data:   chunk,
		})
	}
	return idx
}

// Options returns the options the index was built with.
func (idx *Index) Options() Options { return idx.opts }

// Matcher incrementally matches a new byte stream against an Index,
// emitting Literal and Match ops.
type Matcher struct {
	idx     *Index
	roller  *Roller
	count   int // bytes currently in the window (<= block size)
	pos     int // bytes consumed from the new stream
	emitPos int // new-stream bytes covered by emitted ops
	pending []byte
	ops     []Op
}

// NewMatcher returns a Matcher using the given index.
func NewMatcher(idx *Index) *Matcher {
	return &Matcher{idx: idx, roller: NewRoller(idx.opts.BlockSize)}
}

// Write feeds the next chunk of the new stream. Chunks may be arbitrarily
// sized; matching is identical to feeding one contiguous stream.
func (m *Matcher) Write(p []byte) {
	size := m.idx.opts.BlockSize
	for _, b := range p {
		if m.count == size {
			// The byte about to slide out of the full window is not part
			// of any future match window, so it becomes literal for now.
			m.pending = append(m.pending, m.roller.window[m.roller.wpos])
		}
		h := m.roller.Roll(b)
		if m.count < size {
			m.count++
		}
		m.pos++
		if m.count == size {
			if blk, ok := m.lookup(h); ok {
				m.flushLiteral()
				m.ops = append(m.ops, Op{Kind: Match, Offset: blk.offset, Length: blk.length})
				m.emitPos += blk.length
				m.roller.Reset(size)
				m.count = 0
			}
		}
	}
}

// lookup resolves a weak fingerprint hit to a verified block. A weak hit
// only yields candidates; a candidate must match the strong digest AND the
// exact window bytes before it is accepted.
func (m *Matcher) lookup(h uint64) (block, bool) {
	cands := m.idx.byWeak[m.idx.opts.weakKey(h)]
	if len(cands) == 0 {
		return block{}, false
	}
	win := m.roller.Bytes()
	strong := sha256.Sum256(win)
	for _, c := range cands {
		if c.strong != strong {
			continue
		}
		if bytes.Equal(c.data, win) {
			return c, true
		}
	}
	return block{}, false
}

// flushLiteral emits the accumulated unmatched bytes as one Literal op.
func (m *Matcher) flushLiteral() {
	if len(m.pending) == 0 {
		return
	}
	data := make([]byte, len(m.pending))
	copy(data, m.pending)
	m.ops = append(m.ops, Op{Kind: Literal, Offset: m.emitPos, Length: len(data), Data: data})
	m.emitPos += len(data)
	m.pending = m.pending[:0]
}

// windowTail returns the bytes still sitting in the (possibly partial)
// window, in chronological order.
func (m *Matcher) windowTail() []byte {
	if m.count == m.roller.size {
		return m.roller.Bytes()
	}
	out := make([]byte, m.count)
	copy(out, m.roller.window[:m.count])
	return out
}

// Finish flushes any remaining unmatched bytes (including the tail shorter
// than one window) and returns all ops. The Matcher is reset and may be
// reused with the same index.
func (m *Matcher) Finish() []Op {
	m.pending = append(m.pending, m.windowTail()...)
	m.flushLiteral()
	ops := m.ops
	m.ops = nil
	m.roller.Reset(m.roller.size)
	m.count = 0
	m.pos = 0
	m.emitPos = 0
	return ops
}

// Reset switches the matcher to a new index, which may use a different
// block length. Bytes already consumed but not yet emitted are preserved
// as pending literals, so stream coverage is maintained across the change.
func (m *Matcher) Reset(idx *Index) {
	m.pending = append(m.pending, m.windowTail()...)
	m.idx = idx
	m.roller.Reset(idx.opts.BlockSize)
	m.count = 0
}

// MatcherState is a serializable snapshot for pause/resume.
type MatcherState struct {
	Roller  RollerState
	Count   int
	Pos     int
	EmitPos int
	Pending []byte
	Ops     []Op
}

// Snapshot captures the full matcher state so matching can be paused and
// resumed later, possibly in another process.
func (m *Matcher) Snapshot() MatcherState {
	pending := make([]byte, len(m.pending))
	copy(pending, m.pending)
	ops := make([]Op, len(m.ops))
	for i, op := range m.ops {
		ops[i] = op
		if op.Data != nil {
			d := make([]byte, len(op.Data))
			copy(d, op.Data)
			ops[i].Data = d
		}
	}
	return MatcherState{
		Roller:  m.roller.Snapshot(),
		Count:   m.count,
		Pos:     m.pos,
		EmitPos: m.emitPos,
		Pending: pending,
		Ops:     ops,
	}
}

// RestoreMatcher rebuilds a Matcher from a snapshot. The index must be the
// same one (or an equivalent one) used when the snapshot was taken.
func RestoreMatcher(idx *Index, st MatcherState) *Matcher {
	return &Matcher{
		idx:     idx,
		roller:  RestoreRoller(st.Roller),
		count:   st.Count,
		pos:     st.Pos,
		emitPos: st.EmitPos,
		pending: st.Pending,
		ops:     st.Ops,
	}
}
