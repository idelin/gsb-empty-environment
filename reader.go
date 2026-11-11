package netsec

import (
	"fmt"
	"io"
	"sort"
)

// Proof certifies which byte ranges were hidden from a reader. Offsets refer
// to the immutable evidence, so proof ranges line up byte-for-byte with the
// evidence offsets. Label is always empty: a range proves that at least one
// guard label on each byte was unauthorized, without disclosing which one.
type Proof struct {
	Version    int64
	MaskByte   byte
	Authorized []string // sorted copy of the reader's granted labels
	Masked     []Range  // minimal, sorted, disjoint, half-open
}

// Reader is an io.ReaderAt pinned to one immutable Version and one set of
// authorized labels. Read calls never block on or observe later updates.
//
// All methods are safe for concurrent use.
type Reader struct {
	version    *Version
	origin     []byte
	maskByte   byte
	logical    int64
	authorized []string
	masked     []Range
}

var _ io.ReaderAt = (*Reader)(nil)

func labelSet(labels []string) map[string]struct{} {
	set := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		set[label] = struct{}{}
	}
	return set
}

func newReader(v *Version, authorized []string) *Reader {
	granted := make([]string, 0, len(authorized))
	seen := make(map[string]struct{}, len(authorized))
	for _, label := range authorized {
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		granted = append(granted, label)
	}
	sort.Strings(granted)

	authSet := make(map[string]struct{}, len(granted))
	for _, label := range granted {
		authSet[label] = struct{}{}
	}
	return &Reader{
		version:    v,
		origin:     v.origin,
		maskByte:   v.maskByte,
		logical:    v.logical,
		authorized: granted,
		masked:     maskedRanges(v.segs, authSet),
	}
}

// ReadAt implements io.ReaderAt. Offsets are absolute into the immutable
// evidence and never shift: hidden bytes are replaced in place by the fixed
// mask byte. Offsets beyond the physical backing but inside the logical
// evidence length read as zero bytes before masking. Reads past the logical
// end return io.EOF after filling the available prefix; a fully out-of-range
// read returns io.EOF with zero bytes.
func (r *Reader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("netsec: negative offset %d", off)
	}
	if off >= r.logical {
		return 0, io.EOF
	}

	end := off + int64(len(p))
	var err error
	if end > r.logical {
		end = r.logical
		err = io.EOF
	}

	// First fill the whole in-bounds window from the physical evidence, with
	// zero bytes for the sparse tail that has no backing, then overwrite every
	// intersecting hidden span with the fixed mask byte.
	nCopied := int(end - off)
	// Start from a clean window so unbacked sparse-tail offsets read as zero.
	clear(p[:nCopied])
	backed := int64(len(r.origin))
	if off < backed {
		copyEnd := end
		if copyEnd > backed {
			copyEnd = backed
		}
		copy(p[:copyEnd-off], r.origin[off:copyEnd])
	}
	for _, mr := range r.masked {
		if mr.End <= off {
			continue
		}
		if mr.Start >= end {
			break
		}
		lo := mr.Start
		if lo < off {
			lo = off
		}
		hi := mr.End
		if hi > end {
			hi = end
		}
		for i := lo - off; i < hi-off; i++ {
			p[i] = r.maskByte
		}
	}
	return nCopied, err
}

// Proof returns the masked-range proof for this reader. The returned value is
// a defensive copy and may be retained or mutated freely.
func (r *Reader) Proof() Proof {
	masked := make([]Range, len(r.masked))
	copy(masked, r.masked)
	authorized := make([]string, len(r.authorized))
	copy(authorized, r.authorized)
	return Proof{
		Version:    r.version.number,
		MaskByte:   r.maskByte,
		Authorized: authorized,
		Masked:     masked,
	}
}

// MaskedRanges returns a defensive copy of the minimal hidden byte ranges.
func (r *Reader) MaskedRanges() []Range {
	out := make([]Range, len(r.masked))
	copy(out, r.masked)
	return out
}

// Version returns the immutable version this reader is pinned to.
func (r *Reader) Version() *Version { return r.version }

// Authorized returns the sorted labels granted to this reader.
func (r *Reader) Authorized() []string {
	out := make([]string, len(r.authorized))
	copy(out, r.authorized)
	return out
}
