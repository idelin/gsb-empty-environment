package blockmatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
)

// SegmentKind tells whether a Segment is a matched block or an unmatched gap.
type SegmentKind int

const (
	// Gap marks bytes of the target stream with no verified match.
	Gap SegmentKind = iota
	// Match marks a block of the target stream verified against the source.
	Match
)

// Segment is one piece of the output tiling. Segments returned across Feed
// and Flush calls are ordered, non-overlapping, and together cover exactly
// the bytes fed so far.
type Segment struct {
	Kind   SegmentKind
	Offset int64 // offset in the target stream
	Length int64
	// SourceOffset is the offset in the reference source; valid only when
	// Kind == Match.
	SourceOffset int64
}

// Matcher consumes a target stream and reports which parts match blocks of
// the indexed reference source. It is a sliding-window, rsync-style matcher:
// the window advances one byte at a time until a verified match is found,
// then skips a whole block.
//
// Verification is layered: a weak rolling-fingerprint hit only yields a
// candidate, which is confirmed by the strong SHA-256 digest and finally by
// a byte-for-byte comparison. Fingerprint collisions therefore never leak
// into the output.
//
// The zero value is not usable; construct with NewMatcher or Restore.
type Matcher struct {
	idx       *Index
	blockSize int

	window   []byte // undecided bytes starting at winStart (len <= blockSize)
	pending  []byte // fed bytes not yet pulled into the window
	winStart int64  // absolute stream offset of window[0]
	gapStart int64  // start of the current unmatched run

	hash rollingHash
}

// NewMatcher creates a streaming matcher over idx using the given block
// length. The index is extended to that block length if necessary.
func NewMatcher(idx *Index, blockSize int) (*Matcher, error) {
	if err := idx.AddBlockSize(blockSize); err != nil {
		return nil, err
	}
	return &Matcher{idx: idx, blockSize: blockSize}, nil
}

// Feed consumes the next chunk of the target stream and returns any segments
// finalized by these bytes. Bytes still inside the sliding window remain
// undecided until more input arrives or Flush is called.
func (m *Matcher) Feed(p []byte) []Segment {
	if len(m.pending) == 0 {
		m.pending = nil // release the backing array between chunks
	}
	m.pending = append(m.pending, p...)
	return m.process()
}

// Flush ends the current stream: every undecided byte becomes a Gap. The
// matcher stays usable; further Feed calls continue at the same offsets.
func (m *Matcher) Flush() []Segment {
	end := m.winStart + int64(len(m.window)) + int64(len(m.pending))
	var segs []Segment
	if end > m.gapStart {
		segs = append(segs, Segment{Kind: Gap, Offset: m.gapStart, Length: end - m.gapStart})
	}
	m.window = nil
	m.pending = nil
	m.hash.reset()
	m.winStart = end
	m.gapStart = end
	return segs
}

// SetBlockSize changes the block length used for subsequent matching. Bytes
// already buffered are kept: if the window is longer than the new size its
// tail is pushed back onto the pending queue, so no input is ever lost or
// double counted. The index is extended to the new block length.
func (m *Matcher) SetBlockSize(n int) error {
	if n < 1 {
		return fmt.Errorf("blockmatch: invalid block size %d", n)
	}
	if err := m.idx.AddBlockSize(n); err != nil {
		return err
	}
	if n == m.blockSize {
		return nil
	}
	if len(m.window) > n {
		excess := append([]byte(nil), m.window[n:]...)
		m.pending = append(excess, m.pending...)
		m.window = m.window[:n]
	}
	m.blockSize = n
	m.recomputeHash()
	return nil
}

// BlockSize returns the current block length.
func (m *Matcher) BlockSize() int { return m.blockSize }

// process drains pending input, emitting finalized segments.
func (m *Matcher) process() []Segment {
	var segs []Segment
	for {
		for len(m.window) < m.blockSize && len(m.pending) > 0 {
			b := m.pending[0]
			m.pending = m.pending[1:]
			m.window = append(m.window, b)
			m.hash.append(b)
		}
		if len(m.window) < m.blockSize {
			break // window not full; wait for more input or Flush
		}
		if ref, ok := m.lookup(); ok {
			if m.winStart > m.gapStart {
				segs = append(segs, Segment{Kind: Gap, Offset: m.gapStart, Length: m.winStart - m.gapStart})
			}
			segs = append(segs, Segment{
				Kind:         Match,
				Offset:       m.winStart,
				Length:       int64(m.blockSize),
				SourceOffset: ref.Offset,
			})
			m.winStart += int64(m.blockSize)
			m.gapStart = m.winStart
			m.window = m.window[:0]
			m.hash.reset()
			continue
		}
		// No verified match: slide the window by one byte.
		out := m.window[0]
		if len(m.pending) > 0 {
			in := m.pending[0]
			m.pending = m.pending[1:]
			m.window = append(m.window, in)
			m.window = m.window[1:]
			m.hash.roll(out, in)
		} else {
			m.window = m.window[1:]
			m.hash.dropFirst(out)
		}
		m.winStart++
	}
	return segs
}

// lookup resolves the current full window against the index. A weak
// fingerprint hit is only a candidate: it must survive the strong digest
// check and a byte-for-byte comparison before a match is returned.
func (m *Matcher) lookup() (BlockRef, bool) {
	weak := m.hash.value() & m.idx.weakMask
	cands := m.idx.candidates(m.blockSize, weak)
	if len(cands) == 0 {
		return BlockRef{}, false
	}
	strong := sha256.Sum256(m.window)
	for _, c := range cands {
		if c.Strong != strong {
			continue
		}
		src := m.idx.source[c.Offset : c.Offset+int64(m.blockSize)]
		if bytes.Equal(src, m.window) {
			return c, true
		}
	}
	return BlockRef{}, false
}

func (m *Matcher) recomputeHash() {
	m.hash.reset()
	for _, b := range m.window {
		m.hash.append(b)
	}
}

// matcherState is the serializable form of a Matcher, for pause/resume.
type matcherState struct {
	BlockSize int
	WinStart  int64
	GapStart  int64
	Window    []byte
	Pending   []byte
}

// Snapshot serializes the matcher's full state so matching can be paused and
// resumed later, possibly in another process. The index is not included;
// Restore must be given an equivalent one.
func (m *Matcher) Snapshot() ([]byte, error) {
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(matcherState{
		BlockSize: m.blockSize,
		WinStart:  m.winStart,
		GapStart:  m.gapStart,
		Window:    m.window,
		Pending:   m.pending,
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Restore recreates a matcher from a Snapshot. The rolling hash is rebuilt
// from the buffered window bytes.
func Restore(idx *Index, data []byte) (*Matcher, error) {
	var st matcherState
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&st); err != nil {
		return nil, fmt.Errorf("blockmatch: restore: %v", err)
	}
	if st.BlockSize < 1 {
		return nil, fmt.Errorf("blockmatch: restore: invalid block size %d", st.BlockSize)
	}
	if err := idx.AddBlockSize(st.BlockSize); err != nil {
		return nil, err
	}
	m := &Matcher{
		idx:       idx,
		blockSize: st.BlockSize,
		window:    st.Window,
		pending:   st.Pending,
		winStart:  st.WinStart,
		gapStart:  st.GapStart,
	}
	m.recomputeHash()
	return m, nil
}
