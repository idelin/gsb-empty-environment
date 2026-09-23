// Package proof implements chunk-level and object-level integrity proofs for an
// append-style content package.
//
// Threat model: an untrusted transport/file stores the package bytes. Only the
// 32-byte root summary is trusted out of band. Any subset of chunks may be read
// and verified locally before the whole package has been seen; the package may
// only be declared complete after every required object, the exact chunk count,
// object order and the reference graph have all been checked against the root.
//
// All decoding is explicit binary parsing over the standard library. No host
// code is ever loaded or executed: there are no gob, encoding/ascii85 plugins,
// interface payloads, reflect-based dispatch or similar mechanisms.
package proof

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Scope is a declared serialization range. A decoder given a scope enforces the
// size and structural limits of that range, so an oversized blob for a small
// range is rejected before memory is allocated for its claims.
type Scope uint8

const (
	ScopeUnknown Scope = iota
	// ScopeChunk decodes one content chunk: tag + index + bytes.
	ScopeChunk
	// ScopeObject decodes one canonical object record.
	ScopeObject
	// ScopeManifest decodes one canonical manifest.
	ScopeManifest
)

func (s Scope) String() string {
	switch s {
	case ScopeChunk:
		return "chunk"
	case ScopeObject:
		return "object"
	case ScopeManifest:
		return "manifest"
	default:
		return "unknown"
	}
}

// Hard, per-scope limits. They are intentionally modest constants: the point is
// that decode validates the claimed length against the scope limit BEFORE
// allocating slices of the advertised size.
const (
	MaxChunkBytes    = 1 << 20  // 1 MiB of payload in one chunk
	MaxObjectBytes   = 64 << 20 // 64 MiB payload in one object record
	MaxManifestBytes = 16 << 20 // 16 MiB canonical manifest
	MaxIDBytes       = 256
	MaxIDCount       = 1 << 20 // at most ~1M objects/chunks/refs
)

var (
	ErrScopeLimit         = errors.New("proof: serialized blob exceeds scope limit")
	ErrTruncated          = errors.New("proof: truncated serialized record")
	ErrTrailingBytes      = errors.New("proof: trailing bytes after record")
	ErrBadTag             = errors.New("proof: wrong record tag")
	ErrBadCount           = errors.New("proof: count exceeds hard limit or overflows")
	ErrNegativeLength     = errors.New("proof: negative length")
	ErrUnknownScope       = errors.New("proof: unknown scope")
	ErrEmptySerialization = errors.New("proof: empty serialization")
)

// CheckScope validates a claimed byte length for a scope before any allocation
// happens. n is signed because callers frequently hold a length from a file
// header; a negative value must never be converted to a huge uint.
func CheckScope(scope Scope, n int64) error {
	if n < 0 {
		return ErrNegativeLength
	}
	var max int64
	switch scope {
	case ScopeChunk:
		max = int64(MaxChunkBytes) + 64
	case ScopeObject:
		max = int64(MaxObjectBytes) + 1<<20
	case ScopeManifest:
		max = MaxManifestBytes
	default:
		return ErrUnknownScope
	}
	if n > max {
		return fmt.Errorf("%w: scope=%s claimed=%d max=%d", ErrScopeLimit, scope, n, max)
	}
	return nil
}

// scopeReader is a bounds-checked cursor over one serialized record. Every read
// validates that enough bytes remain; slice-returning helpers take a max count
// so a corrupt count field cannot force a giant make().
type scopeReader struct {
	scope Scope
	buf   []byte
	pos   int
}

func newScopeReader(scope Scope, buf []byte) (*scopeReader, error) {
	if err := CheckScope(scope, int64(len(buf))); err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return nil, ErrEmptySerialization
	}
	return &scopeReader{scope: scope, buf: buf}, nil
}

func (r *scopeReader) remaining() int { return len(r.buf) - r.pos }

func (r *scopeReader) tag(want [4]byte) error {
	if r.remaining() < 4 {
		return ErrTruncated
	}
	var got [4]byte
	copy(got[:], r.buf[r.pos:r.pos+4])
	r.pos += 4
	if got != want {
		return fmt.Errorf("%w: got %q want %q", ErrBadTag, string(got[:]), string(want[:]))
	}
	return nil
}

func (r *scopeReader) u32() (uint32, error) {
	if r.remaining() < 4 {
		return 0, ErrTruncated
	}
	v := binary.BigEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v, nil
}

func (r *scopeReader) u64() (uint64, error) {
	if r.remaining() < 8 {
		return 0, ErrTruncated
	}
	v := binary.BigEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return v, nil
}

func (r *scopeReader) fixed(n int, what string) ([]byte, error) {
	if n < 0 || r.remaining() < n {
		return nil, fmt.Errorf("%w: %s need %d", ErrTruncated, what, n)
	}
	out := make([]byte, n)
	copy(out, r.buf[r.pos:r.pos+n])
	r.pos += n
	return out, nil
}

// lp32 reads a u32 length-prefixed field bounded by maxBytes. The length is
// checked against both the remaining bytes and maxBytes BEFORE make().
func (r *scopeReader) lp32(maxBytes int, what string) ([]byte, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	if int64(n) > int64(maxBytes) || int(n) > r.remaining() {
		return nil, fmt.Errorf("%w: %s len=%d", ErrScopeLimit, what, n)
	}
	return r.fixed(int(n), what)
}

func (r *scopeReader) lpStr(maxBytes int, what string) (string, error) {
	b, err := r.lp32(maxBytes, what)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// count32 reads an element count and returns it, rejecting values that would
// overflow the remaining bytes when each element needs at least minElem bytes,
// or that exceed maxCount. Call this before make([]T, count).
func (r *scopeReader) count32(maxCount, minElem int, what string) (int, error) {
	n, err := r.u32()
	if err != nil {
		return 0, err
	}
	if int64(n) > int64(maxCount) || uint64(n)*uint64(minElem) > uint64(r.remaining()) {
		return 0, fmt.Errorf("%w: %s count=%d", ErrBadCount, what, n)
	}
	return int(n), nil
}

func (r *scopeReader) u32List(maxCount int, what string) ([]uint32, error) {
	n, err := r.count32(maxCount, 1, what)
	if err != nil {
		return nil, err
	}
	out := make([]uint32, n)
	for i := range out {
		if out[i], err = r.u32(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *scopeReader) end() error {
	if r.pos != len(r.buf) {
		return fmt.Errorf("%w: %d unconsumed bytes", ErrTrailingBytes, r.remaining())
	}
	return nil
}

// --- canonical writers (one implementation, shared by builder and re-encode
// checks, so wire bytes are never ambiguous) ---

func putU32(b []byte, v uint32) { binary.BigEndian.PutUint32(b, v) }
func putU64(b []byte, v uint64) { binary.BigEndian.PutUint64(b, v) }

func appendTag(b []byte, t [4]byte) []byte { return append(b, t[:]...) }

func appendU32(b []byte, v uint32) []byte {
	var buf [4]byte
	putU32(buf[:], v)
	return append(b, buf[:]...)
}

func appendU64(b []byte, v uint64) []byte {
	var buf [8]byte
	putU64(buf[:], v)
	return append(b, buf[:]...)
}

func appendLP32(b, v []byte) []byte {
	b = appendU32(b, uint32(len(v)))
	return append(b, v...)
}

func appendLPStr(b []byte, v string) []byte {
	b = appendU32(b, uint32(len(v)))
	return append(b, v...)
}
