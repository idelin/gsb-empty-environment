package segcrc

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

func missingError(name string) error {
	return fmt.Errorf("%w for segment %q", ErrMissingData, name)
}

func corruptError(name string) error {
	return fmt.Errorf("%w in segment %q", ErrCorruptData, name)
}

// readChunk is the minimum allocation needed to verify one segment.
const readChunk = 64 * 1024

// Source supplies the bytes of a named segment. A nil reader or
// (nil, false) means the data is missing; (non-nil, true) means the data is
// present even if reading later fails or yields the wrong bytes.
type Source interface {
	Open(name string) (io.Reader, bool)
}

// SourceFunc adapts a function to Source.
type SourceFunc func(name string) (io.Reader, bool)

func (f SourceFunc) Open(name string) (io.Reader, bool) { return f(name) }

// ReadSegment hashes the bytes of name up to s.Length bytes and verifies them
// against s. Missing and corrupt data are reported separately:
//
//   - ErrMissingData (wrapped): the source has no reader for name.
//   - ErrCorruptData  (wrapped): bytes are present but the digest differs or
//     the stream ends before s.Length bytes.
//   - ErrLenOverflow  (wrapped): declared length exceeds quota.
//
// The read buffer is allocated only after the length budget is validated, so
// an attacker-controlled huge declared length can never trigger a huge
// allocation: streaming reads are capped at readChunk regardless of length,
// and a declared length above quota is rejected before any allocation.
func ReadSegment(src Source, name string, s Summary, quota int64) (Summary, error) {
	if s.Length < 0 {
		return Summary{}, ErrNegativeLen
	}
	if s.Length == 0 {
		if s.CRC != 0 {
			return Summary{}, fmt.Errorf("%w: zero-length segment with non-zero crc", ErrBadSummary)
		}
		return Empty, nil
	}
	got, err := HashSegment(src, name, s.Length, quota)
	if err != nil {
		return got, err
	}
	if got != s {
		return got, fmt.Errorf("%w: digest %08x != declared %08x",
			corruptError(name), got.CRC, s.CRC)
	}
	return got, nil
}

// HashSegment streams exactly length bytes of name and returns their summary.
// It does not compare against a declared CRC, so it is used to produce
// summaries for newly written or replaced segments. As with ReadSegment the
// 64KiB buffer is allocated only after the length passes the quota check, and
// missing data (no reader) is distinguished from short/corrupt reads.
func HashSegment(src Source, name string, length, quota int64) (Summary, error) {
	if length < 0 {
		return Summary{}, ErrNegativeLen
	}
	if quota < 0 {
		return Summary{}, fmt.Errorf("%w: quota negative", ErrLenOverflow)
	}
	if length > quota {
		return Summary{}, fmt.Errorf("%w: segment %q needs %d bytes, quota %d",
			ErrLenOverflow, name, length, quota)
	}
	if length == 0 {
		return Empty, nil
	}
	r, ok := src.Open(name)
	if !ok || r == nil {
		return Summary{}, missingError(name)
	}
	buf := make([]byte, readChunk)
	h := NewHasher()
	var read int64
	for read < length {
		want := int(length - read)
		if want > len(buf) {
			want = len(buf)
		}
		n, err := io.ReadFull(r, buf[:want])
		if _, werr := h.Write(buf[:n]); werr != nil {
			return Summary{}, werr
		}
		read += int64(n)
		if err != nil {
			if err == io.ErrUnexpectedEOF || err == io.EOF {
				return Summary{}, fmt.Errorf("%w: expected %d bytes got %d",
					corruptError(name), length, read)
			}
			return Summary{}, fmt.Errorf("segcrc: read %q: %w", name, err)
		}
	}
	return h.Summary(), nil
}

// VerifyAll re-checks only the given segments against their declared summaries
// and returns their freshly built Joiner summary. Unchanged segments need not
// be passed here at all; callers keep their previous summaries in the Joiner.
// The buffer-per-segment cost is bounded by readChunk and by quota bytes of
// logical length accepted before allocation.
func VerifyAll(src Source, quota int64, segs ...Segment) (map[string]Summary, error) {
	got := make(map[string]Summary, len(segs))
	for _, seg := range segs {
		s, err := ReadSegment(src, seg.Name, seg.Summary, quota)
		if err != nil {
			return nil, err
		}
		got[seg.Name] = s
	}
	return got, nil
}

// Encode renders s as "crc-hex:length-dec", e.g. "cbf5315b:11".
func (s Summary) Encode() string {
	return fmt.Sprintf("%08x:%d", s.CRC, s.Length)
}

// ParseSummary decodes the form produced by Encode. Malformed input yields
// ErrBadSummary; missing data is never produced here.
func ParseSummary(text string) (Summary, error) {
	crcPart, lenPart, ok := strings.Cut(strings.TrimSpace(text), ":")
	if !ok || len(crcPart) != 8 || len(lenPart) == 0 {
		return Summary{}, fmt.Errorf("%w: %q", ErrBadSummary, text)
	}
	crc, err := strconv.ParseUint(crcPart, 16, 32)
	if err != nil {
		return Summary{}, fmt.Errorf("%w: crc %q: %v", ErrBadSummary, crcPart, err)
	}
	n, err := strconv.ParseInt(lenPart, 10, 64)
	if err != nil {
		return Summary{}, fmt.Errorf("%w: length %q: %v", ErrBadSummary, lenPart, err)
	}
	if n < 0 {
		return Summary{}, fmt.Errorf("%w: %w: negative length", ErrBadSummary, ErrNegativeLen)
	}
	s := Summary{CRC: uint32(crc), Length: n}
	if n == 0 && crc != 0 {
		return Summary{}, fmt.Errorf("%w: zero length with non-zero crc", ErrBadSummary)
	}
	return s, nil
}
