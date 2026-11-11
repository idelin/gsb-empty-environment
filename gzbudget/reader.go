// Package gzbudget implements a budget-controlled reader for gzip
// multi-member streams.
//
// Audit systems often treat a nested (multi-member) gzip payload as one
// small request because the compressed size is tiny. Reader defeats that
// by accounting every layer: it records compressed and decompressed bytes
// per member, enforces a global expansion budget and a maximum nesting
// depth (member count), and reports the first member that exceeds a limit
// or is corrupt, even when member headers/trailers span read blocks or a
// checksum fails mid-stream.
//
// The Read interface is strictly limited: once the budget is exhausted it
// returns a sticky *Error and never expands another byte.
package gzbudget

import (
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
)

// Sentinel causes carried by *Error, matchable with errors.Is.
var (
	ErrBudgetExceeded = errors.New("gzbudget: global expansion budget exceeded")
	ErrDepthExceeded  = errors.New("gzbudget: maximum nesting depth exceeded")
)

// Kind classifies a terminal *Error.
type Kind int

const (
	// KindCorrupt means the member is damaged: bad header, bad checksum,
	// truncated stream, or an underlying I/O failure.
	KindCorrupt Kind = iota
	// KindBudget means the global expansion budget was exhausted.
	KindBudget
	// KindDepth means the nesting depth (member count) limit was hit.
	KindDepth
)

func (k Kind) String() string {
	switch k {
	case KindCorrupt:
		return "corrupt"
	case KindBudget:
		return "budget"
	case KindDepth:
		return "depth"
	}
	return "unknown"
}

// Error pinpoints the first member that exceeded a limit or was corrupt.
type Error struct {
	Kind   Kind  // what went wrong
	Member int   // index of the offending member (0-based)
	Offset int64 // compressed offset in the source where the member starts
	Err    error // underlying cause; ErrBudgetExceeded / ErrDepthExceeded / gzip error
}

func (e *Error) Error() string {
	return fmt.Sprintf("gzbudget: member %d at compressed offset %d: %s: %v",
		e.Member, e.Offset, e.Kind, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Limits configures a Reader. Non-positive values disable a limit.
type Limits struct {
	// MaxTotalOutput is the global expansion budget: the maximum number of
	// decompressed bytes produced across all members. <= 0 means unlimited.
	MaxTotalOutput int64
	// MaxMembers is the maximum nesting depth, i.e. how many concatenated
	// gzip members may be expanded. <= 0 means unlimited.
	MaxMembers int
}

// MemberStats records one layer (member) of the stream.
type MemberStats struct {
	Index             int   // 0-based member index (nesting depth - 1)
	CompressedOffset  int64 // absolute compressed offset of the member header
	CompressedBytes   int64 // compressed bytes consumed (header..trailer)
	DecompressedBytes int64 // expanded bytes produced by this member
}

// Reader expands a gzip multi-member stream under budget control.
type Reader struct {
	src   *countingReader // counts bytes pulled from the underlying source
	br    *bufio.Reader   // decouples gzip from source block boundaries
	meter *byteMeter      // counts bytes consumed by the gzip layer
	lim   Limits

	cur    *gzip.Reader // decompressor of the in-flight member
	member int          // index of the next member to start
	stats  []MemberStats
	out    int64 // total decompressed bytes produced

	eof bool  // clean end of stream reached
	err error // sticky terminal error
}

// NewReader wraps src, which must yield a sequence of concatenated gzip
// members. src may deliver data in arbitrarily small blocks; member
// headers and trailers may span block boundaries.
func NewReader(src io.Reader, lim Limits) *Reader {
	cr := &countingReader{r: src}
	br := bufio.NewReader(cr)
	return &Reader{
		src:   cr,
		br:    br,
		meter: &byteMeter{r: br},
		lim:   lim,
	}
}

// Read expands at most the remaining budget. After a terminal error it
// returns that same error forever without expanding or pulling any
// further bytes.
func (r *Reader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.eof {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.lim.MaxTotalOutput > 0 {
		rem := r.lim.MaxTotalOutput - r.out
		if rem <= 0 {
			// Budget exhausted. Only a clean end-of-stream (no further
			// members pending) is still allowed; anything else is a
			// refusal and no further byte is expanded.
			if r.cur == nil && r.streamDone() {
				r.eof = true
				return 0, io.EOF
			}
			r.err = &Error{Kind: KindBudget, Member: r.member, Offset: r.meter.n, Err: ErrBudgetExceeded}
			return 0, r.err
		}
		if int64(len(p)) > rem {
			p = p[:int(rem)]
		}
	}
	for {
		if r.cur == nil {
			if err := r.startMember(); err != nil {
				if err == io.EOF {
					r.eof = true
					return 0, io.EOF
				}
				r.err = err
				return 0, r.err
			}
		}
		n, err := r.cur.Read(p)
		if n > 0 {
			r.out += int64(n)
			r.stats[len(r.stats)-1].DecompressedBytes += int64(n)
		}
		if err == io.EOF {
			r.closeMember()
			if n > 0 {
				return n, nil
			}
			continue
		}
		if err != nil {
			r.syncMember()
			r.err = &Error{
				Kind:   KindCorrupt,
				Member: r.member,
				Offset: r.stats[len(r.stats)-1].CompressedOffset,
				Err:    err,
			}
			return n, r.err
		}
		if n > 0 {
			return n, nil
		}
	}
}

// startMember peeks for another member and opens its decompressor.
func (r *Reader) startMember() error {
	if _, err := r.br.Peek(1); err != nil {
		if err == io.EOF {
			return io.EOF
		}
		return &Error{Kind: KindCorrupt, Member: r.member, Offset: r.meter.n, Err: err}
	}
	if r.lim.MaxMembers > 0 && r.member >= r.lim.MaxMembers {
		return &Error{Kind: KindDepth, Member: r.member, Offset: r.meter.n, Err: ErrDepthExceeded}
	}
	off := r.meter.n
	gz, err := gzip.NewReader(r.meter)
	if err != nil {
		return &Error{Kind: KindCorrupt, Member: r.member, Offset: off, Err: err}
	}
	gz.Multistream(false) // one member per decompressor; we chain manually
	r.cur = gz
	r.stats = append(r.stats, MemberStats{Index: r.member, CompressedOffset: off})
	return nil
}

// closeMember finalizes accounting for the in-flight member.
func (r *Reader) closeMember() {
	r.syncMember()
	r.cur.Close()
	r.cur = nil
	r.member++
}

// syncMember folds the bytes consumed so far into the current member's
// stats, including for members that later turn out to be corrupt.
func (r *Reader) syncMember() {
	st := &r.stats[len(r.stats)-1]
	st.CompressedBytes = r.meter.n - st.CompressedOffset
}

// streamDone reports whether no further member data is pending.
func (r *Reader) streamDone() bool {
	_, err := r.br.Peek(1)
	return err == io.EOF
}

// Members returns a snapshot of the per-layer accounting gathered so far,
// including a partially consumed corrupt member.
func (r *Reader) Members() []MemberStats {
	out := make([]MemberStats, len(r.stats))
	copy(out, r.stats)
	return out
}

// TotalOutput returns the decompressed bytes produced so far.
func (r *Reader) TotalOutput() int64 { return r.out }

// CompressedConsumed returns the compressed bytes consumed by the gzip
// layer so far (the logical stream offset).
func (r *Reader) CompressedConsumed() int64 { return r.meter.n }

// SourceReadBytes returns the bytes physically pulled from the underlying
// source, which may run ahead of CompressedConsumed due to buffering.
func (r *Reader) SourceReadBytes() int64 { return r.src.n }

// countingReader counts bytes pulled from the underlying source.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// byteMeter counts bytes handed to the gzip layer. It implements
// io.ByteReader so compress/flate consumes exactly the bytes of each
// member and never reads ahead across a member boundary.
type byteMeter struct {
	r *bufio.Reader
	n int64
}

func (m *byteMeter) Read(p []byte) (int, error) {
	n, err := m.r.Read(p)
	m.n += int64(n)
	return n, err
}

func (m *byteMeter) ReadByte() (byte, error) {
	b, err := m.r.ReadByte()
	if err == nil {
		m.n++
	}
	return b, err
}
